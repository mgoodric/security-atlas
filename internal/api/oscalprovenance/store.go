// Package oscalprovenance serves the slice-599 OSCAL resolved-chain
// provenance read endpoint:
//
//	GET /v1/oscal/imported-profiles/{id}/provenance
//
// Slice 578 records the resolved import chain as provenance in the
// `imported_catalog_audit_log.detail` JSON of the `profile_imported`
// success row: a `chain` array of {role, sha256, bytes} entries (entry
// profile + intermediate profiles + catalogs) plus a `chain_depth` count.
// Slice 578 WROTE that provenance; this slice READS it so an operator or
// auditor can prove exactly which documents (and their hashes) resolved a
// given imported profile baseline — the "diligence the diligence tool"
// provenance story for chained imports.
//
// The endpoint is a pure read over the existing append-only audit-log
// table — this slice adds NO migration and NO write surface. The read
// never touches the compliance-trestle bridge: the provenance is already
// persisted in Postgres, so the read path is testable with seeded DB rows
// alone (no bridge), which is what the integration suite exercises.
//
// The Store wraps the sqlc Queries with the tenancy plumbing required for
// RLS: it opens a transaction, applies the tenant GUC via internal/tenancy,
// and runs the query inside that transaction so RLS policies see the tenant
// id. This mirrors controldetail.Store.inTx (slice 064).
package oscalprovenance

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Store is the OSCAL provenance read layer. It holds no write methods —
// every query it issues is a SELECT.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wires a Store over the application pgx pool. The pool must be
// connected as the application role (NOSUPERUSER NOBYPASSRLS) so RLS is
// actually enforced.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// inTx opens a transaction, applies the tenant GUC, runs fn, commits on
// success. Mirrors controldetail.Store.inTx. The Store performs only reads,
// so the commit is a clean release of the read transaction.
func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("oscalprovenance: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("oscalprovenance: begin tx: %w", err)
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
		return fmt.Errorf("oscalprovenance: commit: %w", err)
	}
	return nil
}

// ProvenanceForBaseline reads the resolved-chain provenance row for one
// imported PROFILE baseline. A cross-tenant id, a non-profile id, or a
// baseline with no success-audit row returns pgx.ErrNoRows — the handler
// maps that to 404. The id resolution is RLS-scoped: a tenant only ever
// sees its own baselines' provenance (AC-3).
func (s *Store) ProvenanceForBaseline(ctx context.Context, baselineID uuid.UUID) (dbx.GetProfileImportProvenanceRow, error) {
	var row dbx.GetProfileImportProvenanceRow
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		var qerr error
		row, qerr = q.GetProfileImportProvenance(ctx, dbx.GetProfileImportProvenanceParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(baselineID),
		})
		return qerr
	})
	return row, err
}

// storeReader adapts the concrete *Store (which returns the dbx row type)
// to the handler's narrow provenanceReader seam (which returns the package
// row alias). The adapter keeps the unit tests able to inject a stub seam
// with no Postgres pool while the production wiring stays New(NewStore(pool)).
type storeReader struct{ s *Store }

func (sr storeReader) ProvenanceForBaseline(ctx context.Context, baselineID uuid.UUID) (provenanceRow, error) {
	return sr.s.ProvenanceForBaseline(ctx, baselineID)
}

// provenanceRow is the package-local alias for the sqlc row type the read
// returns. Aliasing keeps the handler + seam from importing dbx directly
// while staying a verbatim, conversion-free view of the generated row.
type provenanceRow = dbx.GetProfileImportProvenanceRow

// pgUUID converts a uuid.UUID to the pgtype.UUID the sqlc params expect.
func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}
