package evidencesummary

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// PortfolioStore is the slice-750 cross-control read layer — the portfolio
// sibling of *Store (the slice-502 single-control live read layer). Like *Store
// it is a PURE read surface (SELECT only, no migration, no write path); the
// summary is never persisted (P0-502-4 carried forward) and the read never
// mutates the ledger (invariant #2).
//
// The ONE structural difference from *Store is the corpus: it resolves a FILTERED
// control SET (by control-family or by framework, reusing the existing
// control-filter query paths), caps it to MaxControlsPerSummary controls, then
// reads each control's MaxRecordsPerControl most-recent CURRENT LIVE records — the
// TWO-LEVEL bound (AC-1, P0-750-2). It reuses the slice-502 ListEvidenceRecordsByControl
// / CountEvidenceRecordsByControl per-control reads (no new evidence query) plus
// ONE new control-set resolver query (ListActiveControlsForPortfolio).
//
// Every method opens a transaction, applies the tenant GUC via internal/tenancy,
// and runs queries inside that transaction so RLS policies see the tenant id
// (invariant #6) — identical to *Store.inTx, so cross-tenant isolation (AC-4) is
// the same load-bearing leg. The cited-id resolver is the slice-502 Store.Resolve
// shape (a cited id resolves to any tenant-owned control/evidence row); the
// grounding gate over portfolioAllowedIDs scopes citations to the summarized set.
type PortfolioStore struct {
	pool *pgxpool.Pool
	// resolver is the embedded single-control Store, reused verbatim as the
	// CitationResolver — a portfolio cited id resolves exactly as a single-control
	// cited id does (tenant-owned control OR evidence row under RLS, AC-4). No
	// portfolio-specific resolver is needed: the grounding gate already scopes
	// citations to the summarized controls' ids.
	resolver *Store
}

// NewPortfolioStore wires a PortfolioStore over the application pgx pool. The pool
// MUST be connected as the application role (NOSUPERUSER NOBYPASSRLS) so RLS is
// actually enforced — that is the load-bearing leg of cross-tenant isolation
// (AC-4).
func NewPortfolioStore(pool *pgxpool.Pool) *PortfolioStore {
	return &PortfolioStore{pool: pool, resolver: NewStore(pool)}
}

// Resolver returns the CitationResolver the PortfolioService should be wired with
// (the embedded single-control Store.Resolve). Exposed so the registrar wires the
// same *PortfolioStore for both the reader and the resolver, as the period + live
// surfaces do.
func (s *PortfolioStore) Resolver() CitationResolver { return s.resolver }

// inTx opens a transaction, applies the tenant GUC, runs fn, commits on success.
// Mirrors Store.inTx verbatim — the tenant GUC is what makes RLS scope the read.
func (s *PortfolioStore) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
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

// PortfolioSet assembles the deterministic TWO-LEVEL bounded cross-control
// evidence set for a filtered control set (AC-1) under the caller's RLS context.
// It resolves the control set (capped at MaxControlsPerSummary + 1 so the
// "K of N" total is honest when the filter matched more), then for each
// in-summary control reads the MaxRecordsPerControl most-recent CURRENT LIVE
// records + the full live count (for the deterministic rollup + per-control
// honesty). Every field is read, never computed by a model.
//
// All reads run in ONE transaction under the tenant GUC, so cross-tenant rows are
// invisible at every level of the corpus (AC-4).
func (s *PortfolioStore) PortfolioSet(ctx context.Context, filter PortfolioFilter) (PortfolioSet, error) {
	out := PortfolioSet{Filter: filter}
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		// Resolve a framework filter to its SCF anchor id set via the existing UCF
		// traversal (ListSCFAnchorsForVersion) — reusing the framework->anchor path
		// rather than inventing a control-by-framework mechanism.
		var anchorIDs []pgtype.UUID
		if filter.FrameworkVersionID != uuid.Nil {
			anchors, err := q.ListSCFAnchorsForVersion(ctx, dbx.ListSCFAnchorsForVersionParams{
				FrameworkVersionID: pgUUID(filter.FrameworkVersionID),
				Limit:              maxFrameworkAnchorScan,
				Offset:             0,
			})
			if err != nil {
				return fmt.Errorf("evidencesummary: list framework anchors: %w", err)
			}
			anchorIDs = make([]pgtype.UUID, 0, len(anchors))
			for _, a := range anchors {
				anchorIDs = append(anchorIDs, a.ID)
			}
			if len(anchorIDs) == 0 {
				// No anchors for this framework version -> no controls match. Return
				// an empty (but valid) set; the service degrades to ReasonNoEvidence.
				return nil
			}
		}

		var family *string
		if filter.Family != "" {
			f := filter.Family
			family = &f
		}

		// Cap at MaxControlsPerSummary + 1: the +1 row tells us the filter matched
		// MORE than the cap so the "K of N" total is honest, without a second COUNT
		// round-trip. We keep only the first MaxControlsPerSummary for the corpus.
		rows, err := q.ListActiveControlsForPortfolio(ctx, dbx.ListActiveControlsForPortfolioParams{
			TenantID:  pgUUID(tenantID),
			Limit:     int32(MaxControlsPerSummary + 1),
			Family:    family,
			AnchorIds: anchorIDs,
		})
		if err != nil {
			return fmt.Errorf("evidencesummary: list portfolio controls: %w", err)
		}

		out.TotalControls = len(rows)
		if len(rows) > MaxControlsPerSummary {
			rows = rows[:MaxControlsPerSummary]
		}

		out.Controls = make([]ControlEvidence, 0, len(rows))
		for _, row := range rows {
			ctrlID := uuid.UUID(row.ID.Bytes)
			ce := ControlEvidence{ControlID: ctrlID, ControlTitle: row.Title}

			total, err := q.CountEvidenceRecordsByControl(ctx, dbx.CountEvidenceRecordsByControlParams{
				TenantID:   pgUUID(tenantID),
				ControlID:  pgUUID(ctrlID),
				ControlRef: ctrlID.String(),
			})
			if err != nil {
				return fmt.Errorf("evidencesummary: count portfolio evidence: %w", err)
			}
			ce.TotalCount = int(total)

			recs, err := q.ListEvidenceRecordsByControl(ctx, dbx.ListEvidenceRecordsByControlParams{
				TenantID:   pgUUID(tenantID),
				ControlID:  pgUUID(ctrlID),
				ControlRef: ctrlID.String(),
				Limit:      MaxRecordsPerControl,
				Offset:     0,
			})
			if err != nil {
				return fmt.Errorf("evidencesummary: list portfolio evidence: %w", err)
			}
			ce.Records = make([]EvidenceFact, 0, len(recs))
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
				ce.Records = append(ce.Records, ef)
			}
			out.Controls = append(out.Controls, ce)
		}
		return nil
	})
	if err != nil {
		return PortfolioSet{}, err
	}
	// Belt-and-suspenders: the query already orders by bundle_id, id; keep the
	// corpus stable for the prompt + rollup regardless of any future read order.
	sortControlsForDeterminism(out.Controls)
	return out, nil
}

// maxFrameworkAnchorScan bounds the anchor-resolution read for a framework filter.
// Frameworks crosswalk to at most a few hundred SCF anchors; this cap keeps the
// anchor-id array (and therefore the ANY($1) control filter) bounded.
const maxFrameworkAnchorScan = 2000
