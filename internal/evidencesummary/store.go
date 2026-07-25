package evidencesummary

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

// Store is the evidence-summarization read layer. It is a PURE read surface:
// every query it issues is a SELECT over an existing tenant-scoped table
// (controls, evidence_records). It adds no migration and no write path — the
// summary is never persisted (P0-502-4), and reading the evidence set never
// triggers evaluation or mutates the ledger (invariant #2).
//
// Every method opens a transaction, applies the tenant GUC via
// internal/tenancy, and runs queries inside that transaction so RLS policies
// see the tenant id (invariant #6). This mirrors gapexplain.Store.inTx /
// controldetail.Store.inTx.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wires a Store over the application pgx pool. The pool MUST be
// connected as the application role (NOSUPERUSER NOBYPASSRLS) so RLS is actually
// enforced — that is the load-bearing leg of the cross-tenant isolation
// guarantee (AC-10).
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// inTx opens a transaction, applies the tenant GUC, runs fn, commits on
// success. Mirrors gapexplain.Store.inTx.
func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("evidencesummary: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("evidencesummary: begin tx: %w", err)
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
		return fmt.Errorf("evidencesummary: commit: %w", err)
	}
	return nil
}

// EvidenceSet assembles the deterministic bounded evidence set for one control
// (AC-1) under the caller's RLS context: the control's title (which doubles as
// proof the control is tenant-owned and exists) plus the MaxCitedExcerpts
// most-recent CURRENT LIVE evidence records as cited excerpts (AC-2). Every
// field is read, never computed by a model.
//
// The retrieval is the existing control-detail evidence data path
// (ListEvidenceRecordsByControl, observed_at DESC, LIMIT) — current live
// evidence only, NOT a frozen audit-period sample population (P0-502-5,
// invariant #10). TotalCount is the full live count so the UI can render
// "showing N of M"; the summary itself is always over the bounded N
// (P0-502-8).
func (s *Store) EvidenceSet(ctx context.Context, controlID uuid.UUID) (EvidenceSet, error) {
	var out EvidenceSet
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		ctrl, err := q.GetControlByID(ctx, dbx.GetControlByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(controlID),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNoControl
			}
			return fmt.Errorf("evidencesummary: get control: %w", err)
		}
		out.ControlID = controlID
		out.ControlTitle = ctrl.Title

		total, err := q.CountEvidenceRecordsByControl(ctx, dbx.CountEvidenceRecordsByControlParams{
			TenantID:   pgUUID(tenantID),
			ControlID:  pgUUID(controlID),
			ControlRef: controlID.String(),
		})
		if err != nil {
			return fmt.Errorf("evidencesummary: count evidence: %w", err)
		}
		out.TotalCount = int(total)

		recs, err := q.ListEvidenceRecordsByControl(ctx, dbx.ListEvidenceRecordsByControlParams{
			TenantID:   pgUUID(tenantID),
			ControlID:  pgUUID(controlID),
			ControlRef: controlID.String(),
			Limit:      MaxCitedExcerpts,
			Offset:     0,
		})
		if err != nil {
			return fmt.Errorf("evidencesummary: list evidence: %w", err)
		}
		out.Records = make([]EvidenceFact, 0, len(recs))
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
			out.Records = append(out.Records, ef)
		}
		return nil
	})
	if err != nil {
		return EvidenceSet{}, err
	}
	return out, nil
}

// Resolve classifies a candidate cited ID by checking whether it names a
// tenant-owned control OR a tenant-owned evidence record visible under the
// caller's RLS context (AC-4). A cross-tenant ID resolves to neither — the
// RLS-scoped queries never return another tenant's row — so Resolve returns
// ok=false for it, which is the mechanism behind AC-10.
//
// The control check runs first (the summary always cites the control id). On a
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
			return fmt.Errorf("evidencesummary: resolve control: %w", err)
		}

		if _, err := q.GetEvidenceRecordByID(ctx, dbx.GetEvidenceRecordByIDParams{
			ID:       pgUUID(id),
			TenantID: pgUUID(tenantID),
		}); err == nil {
			out = Citation{Kind: KindEvidence, ID: id.String()}
			ok = true
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("evidencesummary: resolve evidence: %w", err)
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
