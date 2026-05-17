// Package controldetail serves the slice-064 control-detail backend read
// endpoints — the four per-control read paths the slice-041 control-detail
// view binds its placeholders to:
//
//	GET /v1/evidence?control_id=<id>     AC-1: paginated evidence-ledger rows
//	GET /v1/controls/{id}/policies       AC-2: linked policies
//	GET /v1/controls/{id}/risks          AC-3: linked risks + link weight
//	GET /v1/controls/{id}/history        AC-4: control_evaluations history
//
// Every endpoint is a pure read over an existing tenant-scoped table — this
// slice adds NO migration and NO write surface. The two ledger reads
// (evidence, history) are over append-only tables, so a GET never triggers
// evaluation and never mutates the record (constitutional invariant #2).
//
// The Store wraps the sqlc Queries with the tenancy plumbing required for
// RLS: every method opens a transaction, applies the tenant GUC via
// internal/tenancy, and runs queries inside that transaction so RLS policies
// see the tenant id. This mirrors eval.Store.inTx / risk.Store.inTx.
package controldetail

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Store is the control-detail read layer. It holds no write methods — every
// query it issues is a SELECT.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wires a Store over the application pgx pool. The pool must be
// connected as the application role (NOSUPERUSER NOBYPASSRLS) so RLS is
// actually enforced.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// inTx opens a transaction, applies the tenant GUC, runs fn, commits on
// success. Mirrors eval.Store.inTx / risk.Store.inTx. The Store performs
// only reads, so the commit is a clean release of the read transaction.
func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("controldetail: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("controldetail: begin tx: %w", err)
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
		return fmt.Errorf("controldetail: commit: %w", err)
	}
	return nil
}

// EvidenceForControl reads one page of evidence-ledger records resolved for
// controlID, bounded by the [since, until] observed_at window and the keyset
// cursor. It returns up to limit rows. Resolution reuses slice 012's
// control->evidence path: (control_id = $ OR control_ref = $) with the
// control ref being the UUID's string form.
func (s *Store) EvidenceForControl(ctx context.Context, controlID uuid.UUID, p evidencePage) ([]dbx.ListEvidenceForControlPagedRow, error) {
	var rows []dbx.ListEvidenceForControlPagedRow
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		var qerr error
		rows, qerr = q.ListEvidenceForControlPaged(ctx, dbx.ListEvidenceForControlPagedParams{
			TenantID:     pgUUID(tenantID),
			ControlID:    pgUUID(controlID),
			ControlRef:   controlID.String(),
			ObservedAt:   pgTimestamptz(p.since),
			ObservedAt_2: pgTimestamptz(p.until),
			CursorTs:     pgTimestamptz(p.cursor.ts),
			CursorID:     pgUUID(p.cursor.id),
			RowLimit:     p.pageRows + 1, // +1 probe row to detect a next page
		})
		return qerr
	})
	return rows, err
}

// EvidencePaged reads one page of the tenant-wide evidence ledger, bounded
// by the [since, until] observed_at window, the keyset cursor, and the
// optional filter set (kind, result, source_actor_type, source_actor_id).
// Used when GET /v1/evidence is called WITHOUT a control_id. Tenant
// isolation continues to ride on RLS plus the explicit tenant_id predicate
// (canvas invariant #6). Slice 106.
func (s *Store) EvidencePaged(ctx context.Context, p evidenceListPage) ([]dbx.ListEvidencePagedRow, error) {
	var rows []dbx.ListEvidencePagedRow
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		var qerr error
		rows, qerr = q.ListEvidencePaged(ctx, dbx.ListEvidencePagedParams{
			TenantID:        pgUUID(tenantID),
			ObservedAt:      pgTimestamptz(p.since),
			ObservedAt_2:    pgTimestamptz(p.until),
			Kind:            optString(p.kind),
			ResultFilter:    optString(p.result),
			SourceActorType: optString(p.sourceActorType),
			SourceActorID:   optString(p.sourceActorID),
			CursorTs:        pgTimestamptz(p.cursor.ts),
			CursorID:        pgUUID(p.cursor.id),
			RowLimit:        p.pageRows + 1, // +1 probe row to detect a next page
		})
		return qerr
	})
	return rows, err
}

// PoliciesForControl reads every policy whose linked_control_ids array
// contains controlID. The policy library is small per the canvas v1 scope,
// so this read is not paginated.
func (s *Store) PoliciesForControl(ctx context.Context, controlID uuid.UUID) ([]dbx.ListPoliciesLinkedToControlRow, error) {
	var rows []dbx.ListPoliciesLinkedToControlRow
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		var qerr error
		rows, qerr = q.ListPoliciesLinkedToControl(ctx, dbx.ListPoliciesLinkedToControlParams{
			TenantID:  pgUUID(tenantID),
			ControlID: pgUUID(controlID),
		})
		return qerr
	})
	return rows, err
}

// RisksForControl reads every risk linked to controlID via
// risk_control_links, joined to the risk row for title + scores. The risk
// register is small per the canvas v1 scope, so this read is not paginated.
func (s *Store) RisksForControl(ctx context.Context, controlID uuid.UUID) ([]dbx.ListRisksLinkedToControlRow, error) {
	var rows []dbx.ListRisksLinkedToControlRow
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		var qerr error
		rows, qerr = q.ListRisksLinkedToControl(ctx, dbx.ListRisksLinkedToControlParams{
			TenantID:  pgUUID(tenantID),
			ControlID: pgUUID(controlID),
		})
		return qerr
	})
	return rows, err
}

// HistoryForControl reads one page of control_evaluations rows for
// controlID, newest-first, bounded by the keyset cursor. It returns up to
// limit rows.
func (s *Store) HistoryForControl(ctx context.Context, controlID uuid.UUID, p historyPage) ([]dbx.ListControlEvaluationHistoryPagedRow, error) {
	var rows []dbx.ListControlEvaluationHistoryPagedRow
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		var qerr error
		rows, qerr = q.ListControlEvaluationHistoryPaged(ctx, dbx.ListControlEvaluationHistoryPagedParams{
			TenantID:  pgUUID(tenantID),
			ControlID: pgUUID(controlID),
			CursorTs:  pgTimestamptz(p.cursor.ts),
			CursorID:  pgUUID(p.cursor.id),
			RowLimit:  p.pageRows + 1, // +1 probe row to detect a next page
		})
		return qerr
	})
	return rows, err
}
