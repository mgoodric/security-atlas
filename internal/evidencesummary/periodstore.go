package evidencesummary

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// auditPeriodFrozenStatus is the audit_periods.status value that marks a period
// as frozen. Mirrors internal/audit/period.StatusFrozen without importing that
// package (this read layer only needs the literal).
const auditPeriodFrozenStatus = "frozen"

// ErrNoPeriod is returned by the PeriodStore when the audit period does not
// exist or is not visible to the requesting tenant (RLS). The handler maps it
// to a 404; there is no frozen population to summarize for a period the tenant
// cannot see.
var ErrNoPeriod = errors.New("evidencesummary: audit period not found")

// ErrPeriodNotFrozen is returned when the audit period exists but is still open
// (no frozen population yet). The period-scoped summary is a comprehension aid
// OVER the frozen sample population; an open period has none, so the handler
// surfaces this distinctly (the caller should use the live control-detail
// summary instead).
var ErrPeriodNotFrozen = errors.New("evidencesummary: audit period is not frozen")

// PeriodStore is the slice-749 FROZEN-population read layer — the audit-workspace
// sibling of *Store (the slice-502 live read layer). Like *Store it is a PURE
// read surface (SELECT only, no migration, no write path); the summary is never
// persisted (P0-502-4 carried forward) and the read never mutates the ledger
// (invariant #2). The ONE difference from *Store is the corpus: every evidence
// read here is bounded by the audit period's freeze horizon
// (observed_at <= frozen_at — invariant #10, P0-749-1), so a post-freeze (live)
// record can never enter the summarized set or the citable-id set.
//
// Every method opens a transaction, applies the tenant GUC via internal/tenancy,
// and runs queries inside that transaction so RLS policies see the tenant id
// (invariant #6) — identical to *Store.inTx, so cross-tenant isolation (AC-3) is
// the same load-bearing leg.
type PeriodStore struct {
	pool *pgxpool.Pool
}

// NewPeriodStore wires a PeriodStore over the application pgx pool. The pool MUST
// be connected as the application role (NOSUPERUSER NOBYPASSRLS) so RLS is
// actually enforced — that is the load-bearing leg of cross-tenant isolation
// (AC-3).
func NewPeriodStore(pool *pgxpool.Pool) *PeriodStore { return &PeriodStore{pool: pool} }

// inTx opens a transaction, applies the tenant GUC, runs fn, commits on success.
// Mirrors Store.inTx verbatim — the tenant GUC is what makes RLS scope the read.
func (s *PeriodStore) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
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

// PeriodEvidenceSet assembles the deterministic bounded FROZEN-population
// evidence set for one control within one frozen audit period (AC-1), under the
// caller's RLS context. It resolves the period (proving it is tenant-owned and
// reading its frozen_at horizon), refuses an open period (ErrPeriodNotFrozen),
// then reads the control's title plus the MaxCitedExcerpts most-recent evidence
// records bounded by observed_at <= frozen_at (invariant #10, P0-749-1). Every
// field is read, never computed by a model.
//
// The returned EvidenceSet is the SAME shape *Store returns (so the shared
// Service pipeline consumes it unchanged); the FrozenAt + AuditPeriodID horizon
// metadata travels alongside it for the UI label and for the horizon-bounded
// citation resolver.
func (s *PeriodStore) PeriodEvidenceSet(ctx context.Context, controlID, auditPeriodID uuid.UUID) (PeriodEvidenceSet, error) {
	var out PeriodEvidenceSet
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		period, err := q.GetAuditPeriodByID(ctx, dbx.GetAuditPeriodByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(auditPeriodID),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNoPeriod
			}
			return fmt.Errorf("evidencesummary: get audit period: %w", err)
		}
		// The period-scoped summary is defined OVER the frozen sample population
		// (invariant #10). An open period has no frozen population, so there is
		// nothing period-scoped to summarize — refuse rather than silently fall
		// through to live state (which would be exactly the live/frozen mixing
		// P0-749-1 forbids).
		if !(period.Status == auditPeriodFrozenStatus && period.FrozenAt.Valid) {
			return ErrPeriodNotFrozen
		}
		out.AuditPeriodID = auditPeriodID
		out.FrozenAt = period.FrozenAt.Time.UTC()

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

		total, err := q.CountEvidenceRecordsByControlBeforeHorizon(ctx, dbx.CountEvidenceRecordsByControlBeforeHorizonParams{
			TenantID:   pgUUID(tenantID),
			ControlID:  pgUUID(controlID),
			ControlRef: controlID.String(),
			FrozenAt:   period.FrozenAt,
		})
		if err != nil {
			return fmt.Errorf("evidencesummary: count frozen evidence: %w", err)
		}
		out.TotalCount = int(total)

		recs, err := q.ListEvidenceRecordsByControlBeforeHorizon(ctx, dbx.ListEvidenceRecordsByControlBeforeHorizonParams{
			TenantID:   pgUUID(tenantID),
			ControlID:  pgUUID(controlID),
			ControlRef: controlID.String(),
			FrozenAt:   period.FrozenAt,
			Limit:      MaxCitedExcerpts,
			Offset:     0,
		})
		if err != nil {
			return fmt.Errorf("evidencesummary: list frozen evidence: %w", err)
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
		return PeriodEvidenceSet{}, err
	}
	return out, nil
}

// ResolveBeforeHorizon classifies a candidate cited ID under the caller's RLS
// context, bounded by the period freeze horizon for EVIDENCE records: a
// post-freeze evidence record (observed_at > frozen_at) is NOT in the frozen
// population and MUST NOT be citable (P0-749-1, AC-5), even though it is a real
// tenant-owned row visible under RLS. The control check is unbounded (the
// control catalog is not period-versioned in v1); the evidence check applies the
// horizon via the horizon-bounded ledger read.
//
// A cross-tenant ID resolves to neither (RLS hides the other tenant's rows), so
// it returns ok=false — the mechanism behind AC-3.
//
// This is the period sibling of Store.Resolve. It is reached through the
// periodResolver adapter, which binds the (controlID, frozenAt) horizon the
// Service's citation gate needs. The grounding gate (allowedIDs over the frozen
// EvidenceSet) is the first, cheaper frozen-population gate; this resolver is the
// defense-in-depth second gate so a post-freeze id fails BOTH.
func (s *PeriodStore) ResolveBeforeHorizon(ctx context.Context, id, controlID uuid.UUID, frozenAt time.Time) (Citation, bool, error) {
	var (
		out Citation
		ok  bool
	)
	horizon := pgtype.Timestamptz{Time: frozenAt, Valid: true}
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

		// Horizon-bounded evidence membership: read the frozen-population records
		// for this control and membership-test the candidate id. A post-freeze id
		// is absent from this set, so it resolves to ok=false (P0-749-1). We page
		// the full frozen population (not just the top-N excerpts) so a legitimate
		// in-population id beyond the excerpt bound still resolves — the grounding
		// gate already capped what the model could see, this gate only confirms
		// tenant + frozen-population membership.
		const pageSize = 500
		offset := int32(0)
		for {
			recs, err := q.ListEvidenceRecordsByControlBeforeHorizon(ctx, dbx.ListEvidenceRecordsByControlBeforeHorizonParams{
				TenantID:   pgUUID(tenantID),
				ControlID:  pgUUID(controlID),
				ControlRef: controlID.String(),
				FrozenAt:   horizon,
				Limit:      pageSize,
				Offset:     offset,
			})
			if err != nil {
				return fmt.Errorf("evidencesummary: resolve frozen evidence: %w", err)
			}
			for _, r := range recs {
				if uuid.UUID(r.ID.Bytes) == id {
					out = Citation{Kind: KindEvidence, ID: id.String()}
					ok = true
					return nil
				}
			}
			if len(recs) < pageSize {
				break
			}
			offset += pageSize
		}
		ok = false
		return nil
	})
	if err != nil {
		return Citation{}, false, err
	}
	return out, ok, nil
}
