package gapexplain

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Store is the gap-explanation read layer. It is a PURE read surface: every
// query it issues is a SELECT over an existing tenant-scoped table
// (evidence_freshness, controls, evidence_records). It adds no migration and
// no write path — the explanation is never persisted (P0-444-4), and reading
// the rollup never triggers evaluation or mutates the ledger (invariant #2).
//
// Every method opens a transaction, applies the tenant GUC via
// internal/tenancy, and runs queries inside that transaction so RLS policies
// see the tenant id (invariant #6). This mirrors
// controldetail.Store.inTx / freshness.Store.inTx.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wires a Store over the application pgx pool. The pool MUST be
// connected as the application role (NOSUPERUSER NOBYPASSRLS) so RLS is
// actually enforced — that is the load-bearing leg of the cross-tenant
// isolation guarantee (AC-10).
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// inTx opens a transaction, applies the tenant GUC, runs fn, commits on
// success. Mirrors controldetail.Store.inTx.
func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("gapexplain: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("gapexplain: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	q := dbx.New(tx)
	if err := fn(ctx, q, tenantID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("gapexplain: commit: %w", err)
	}
	return nil
}

// Rollup assembles the deterministic per-control gap rollup (AC-1) under the
// caller's RLS context: the control's title, its freshness facts from the
// slice-016 evidence_freshness read-model, and a bounded set of the freshest
// evidence records as cited excerpts (AC-2). Every field is read, never
// computed by a model.
//
// The control title comes from GetControlByID (which doubles as the
// proof the control itself is tenant-owned and exists). The freshness facts
// come from GetEvidenceFreshnessByControl. The excerpts come from
// ListEvidenceRecordsByControl, capped at maxCitedExcerpts.
func (s *Store) Rollup(ctx context.Context, controlID uuid.UUID) (Rollup, error) {
	var out Rollup
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		ctrl, err := q.GetControlByID(ctx, dbx.GetControlByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(controlID),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNoRollup
			}
			return fmt.Errorf("gapexplain: get control: %w", err)
		}
		out.ControlID = controlID
		out.ControlTitle = ctrl.Title
		if ctrl.FreshnessClass != nil {
			out.FreshnessClass = *ctrl.FreshnessClass
		}

		fresh, err := q.GetEvidenceFreshnessByControl(ctx, dbx.GetEvidenceFreshnessByControlParams{
			TenantID:  pgUUID(tenantID),
			ControlID: pgUUID(controlID),
		})
		switch {
		case err == nil:
			if fresh.FreshnessClass != nil {
				out.FreshnessClass = *fresh.FreshnessClass
			}
			out.IsStale = fresh.IsStale
			out.EvidenceCount = int(fresh.EvidenceCount)
			if fresh.LatestObservedAt.Valid {
				t := fresh.LatestObservedAt.Time.UTC()
				out.LatestObservedAt = &t
			}
			if fresh.ValidUntil.Valid {
				t := fresh.ValidUntil.Time.UTC()
				out.ValidUntil = &t
			}
		case errors.Is(err, pgx.ErrNoRows):
			// The freshness read-model has not been refreshed for this control
			// yet. The control is, by the slice-016 definition, not currently
			// fresh — there is no observation on record. Treat as stale with
			// zero evidence so the rollup is still meaningful to explain.
			out.IsStale = true
		default:
			return fmt.Errorf("gapexplain: get freshness: %w", err)
		}

		recs, err := q.ListEvidenceRecordsByControl(ctx, dbx.ListEvidenceRecordsByControlParams{
			TenantID:   pgUUID(tenantID),
			ControlID:  pgUUID(controlID),
			ControlRef: controlID.String(),
			Limit:      maxCitedExcerpts,
			Offset:     0,
		})
		if err != nil {
			return fmt.Errorf("gapexplain: list evidence: %w", err)
		}
		out.Evidence = make([]EvidenceFact, 0, len(recs))
		for _, r := range recs {
			ef := EvidenceFact{
				EvidenceID: uuid.UUID(r.ID.Bytes),
				Result:     string(r.Result),
			}
			if r.EvidenceKind != nil {
				ef.EvidenceKind = *r.EvidenceKind
			}
			if r.ObservedAt.Valid {
				ef.ObservedAt = r.ObservedAt.Time.UTC()
			}
			out.Evidence = append(out.Evidence, ef)
		}
		return nil
	})
	if err != nil {
		return Rollup{}, err
	}
	return out, nil
}

// Resolve classifies a candidate cited ID by checking whether it names a
// tenant-owned control OR a tenant-owned evidence record visible under the
// caller's RLS context (AC-4). A cross-tenant ID resolves to neither — the
// RLS-scoped queries never return another tenant's row — so Resolve returns
// ok=false for it, which is the mechanism behind AC-10.
//
// The control check runs first (the rollup always cites the control id). On a
// miss it falls through to the evidence check. A genuine DB error (not a
// not-found) is returned so the caller can suppress conservatively.
func (s *Store) Resolve(ctx context.Context, id uuid.UUID) (Citation, bool, error) {
	var (
		out Citation
		ok  bool
	)
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		if _, err := q.GetControlByID(ctx, dbx.GetControlByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		}); err == nil {
			out = Citation{Kind: KindControl, ID: id.String()}
			ok = true
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("gapexplain: resolve control: %w", err)
		}

		if _, err := q.GetEvidenceRecordByID(ctx, dbx.GetEvidenceRecordByIDParams{
			ID:       pgUUID(id),
			TenantID: pgUUID(tenantID),
		}); err == nil {
			out = Citation{Kind: KindEvidence, ID: id.String()}
			ok = true
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("gapexplain: resolve evidence: %w", err)
		}

		// Neither a tenant-owned control nor a tenant-owned evidence record.
		ok = false
		return nil
	})
	if err != nil {
		return Citation{}, false, err
	}
	return out, ok, nil
}

func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}
