// Package dashboard serves the slice-066 dashboard backend read endpoints
// the slice-040 program dashboard view binds its panels to:
//
//	GET /v1/frameworks/posture   AC-1: per-framework-version posture
//	GET /v1/activity             AC-2: evidence-ingest activity feed
//
// (AC-4's upcoming rollup was retired by #2130; the dashboard's
// "Coming up" reads the compliance calendar, GET /v1/calendar.)
//
// (The fourth, AC-3's `sort=residual,age` on GET /v1/risks, extends the
// existing internal/api/risks + internal/risk packages additively — it is
// not part of this package.)
//
// Every endpoint is a pure read over existing tenant-scoped tables (or the
// slice-062 admin_audit_log_v view) — this slice adds NO migration and NO
// write surface. The activity feed reads an append-only ledger; the posture
// trend reads the append-only control_evaluations ledger. A GET never
// triggers evaluation and never mutates the record (constitutional
// invariant #2).
//
// The Store wraps the sqlc Queries with the tenancy plumbing required for
// RLS: every method opens a transaction, applies the tenant GUC via
// internal/tenancy, and runs queries inside that transaction so RLS
// policies see the tenant id. This mirrors controldetail.Store.inTx /
// risk.Store.inTx.
package dashboard

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Store is the dashboard read layer. It holds no write methods — every
// query it issues is a SELECT.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wires a Store over the application pgx pool. The pool must be
// connected as the application role (NOSUPERUSER NOBYPASSRLS) so RLS is
// actually enforced.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// inTx opens a transaction, applies the tenant GUC, runs fn, commits on
// success. Mirrors controldetail.Store.inTx / risk.Store.inTx. The Store
// performs only reads, so the commit is a clean release of the read
// transaction.
func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("dashboard: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("dashboard: begin tx: %w", err)
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
		return fmt.Errorf("dashboard: commit: %w", err)
	}
	return nil
}

// FrameworkPosture reads per-framework-version posture: coverage_pct, the
// freshness composite, and the 90-day trend delta. trendCutoff is the
// 90-day-ago timestamp, computed in Go so the SQL stays static.
func (s *Store) FrameworkPosture(ctx context.Context, trendCutoff pgtype.Timestamptz) ([]dbx.FrameworkPostureRow, error) {
	var rows []dbx.FrameworkPostureRow
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		var qerr error
		rows, qerr = q.FrameworkPosture(ctx, dbx.FrameworkPostureParams{
			TenantID:    pgUUID(tenantID),
			EvaluatedAt: trendCutoff,
		})
		return qerr
	})
	return rows, err
}

// PostureTargets reads the tenant's posture targets (#2136), one per
// framework version that has one.
func (s *Store) PostureTargets(ctx context.Context) ([]dbx.FrameworkPostureTarget, error) {
	var rows []dbx.FrameworkPostureTarget
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		var qerr error
		rows, qerr = q.ListFrameworkPostureTargets(ctx, pgUUID(tenantID))
		return qerr
	})
	return rows, err
}

// ActivityFeed reads one page of the evidence-ingest activity feed,
// newest-first, bounded by the keyset cursor. It returns up to limit rows.
func (s *Store) ActivityFeed(ctx context.Context, cursor keyset, pageRows int32) ([]dbx.ListEvidenceActivityRow, error) {
	var rows []dbx.ListEvidenceActivityRow
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		var qerr error
		rows, qerr = q.ListEvidenceActivity(ctx, dbx.ListEvidenceActivityParams{
			TenantID: pgUUID(tenantID),
			CursorTs: pgTimestamptz(cursor.ts),
			CursorID: cursor.id,
			RowLimit: pageRows + 1, // +1 probe row to detect a next page
		})
		return qerr
	})
	return rows, err
}

// ActivityFeedFirstPage is the cross-package convenience wrapper for
// the dashboard's activity feed's first page. Slice 269 (dashboard
// export) is the first caller — the export composes the same view
// the user sees in the live dashboard, which is "the first page,
// newest-first". The handler at `/v1/activity` consumes the keyset
// cursor; cross-package callers (slice 269) cannot construct a
// keyset because the type is package-private (the wire shape is
// intentionally opaque), so this wrapper bridges the gap without
// leaking the type.
//
// `limit` is the page size (the caller's responsibility to bound).
func (s *Store) ActivityFeedFirstPage(ctx context.Context, limit int32) ([]dbx.ListEvidenceActivityRow, error) {
	return s.ActivityFeed(ctx, firstPageActivity(), limit)
}
