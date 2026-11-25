//go:build integration

// #3056 — the exception lifecycle-stamp CHECKs. Asserts the migration
// applies over rows written exactly as the demo seeder wrote them before
// #3056 (expired, no stamps, no audit rows), stamps them from the audit
// log where one dates the transition and from the documented stand-in
// otherwise, and leaves the CHECKs enforced. Every case rolls back.

package db_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	exceptionStampsUp   = "../../migrations/sql/20261309000000_exception_lifecycle_stamps.sql"
	exceptionStampsDown = "../../migrations/sql/20261309000000_exception_lifecycle_stamps.down.sql"
)

func TestExceptionLifecycleStampsMigration_OldDemoSeedRows(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; the migration test needs the migrate role")
	}
	up, err := os.ReadFile(exceptionStampsUp)
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	down, err := os.ReadFile(exceptionStampsDown)
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tx, err := adminPool.Begin(ctx)
	if err != nil {
		t.Fatalf("admin Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}

	exec(string(down))
	tenant := uuid.NewString()
	exec(`INSERT INTO tenants (id, name, is_bootstrap_tenant) VALUES ($1, $2, false)`, tenant, "stamps-3056-"+tenant[:8])
	controlID := uuid.NewString()
	mustInsertControl(ctx, t, tx, tenant, controlID, "IAC-01")

	day := 24 * time.Hour
	requestedAt := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	approvedAt := requestedAt.Add(7 * day)
	expiresAt := requestedAt.Add(180 * day)
	updatedAt := requestedAt.Add(200 * day)

	// The old demo seeder's expired row: approved, never stamped active or
	// expired, no audit rows.
	oldDemo := uuid.NewString()
	exec(`
		INSERT INTO exceptions (
			id, tenant_id, control_id, scope_cell_predicate, justification,
			requested_by, requested_at, approved_by, approved_at,
			expires_at, updated_at, status
		)
		VALUES ($1, $2, $3, '{"op":"true"}'::jsonb, 'old demo seed', 'demo@demo.example', $4,
		        'approver@demo.example', $5, $6, $7, 'expired')
	`, oldDemo, tenant, controlID, requestedAt, approvedAt, expiresAt, updatedAt)
	// An expired row whose expiry write came before expires_at's stand-in
	// would: updated_at is the earlier of the two.
	earlyUpdate := uuid.NewString()
	exec(`
		INSERT INTO exceptions (
			id, tenant_id, control_id, scope_cell_predicate, justification,
			requested_by, requested_at, effective_from, expires_at, updated_at, status
		)
		VALUES ($1, $2, $3, '{"op":"true"}'::jsonb, 'early update', 'demo@demo.example', $4,
		        $5, $6, $7, 'expired')
	`, earlyUpdate, tenant, controlID, requestedAt, requestedAt.Add(day), expiresAt, expiresAt.Add(-day))
	// An active row with no stamp but an audit row that dates it.
	audited := uuid.NewString()
	activatedAt := approvedAt.Add(day)
	exec(`
		INSERT INTO exceptions (
			id, tenant_id, control_id, scope_cell_predicate, justification,
			requested_by, requested_at, approved_by, approved_at, expires_at, status
		)
		VALUES ($1, $2, $3, '{"op":"true"}'::jsonb, 'audited', 'demo@demo.example', $4,
		        'approver@demo.example', $5, $6, 'active')
	`, audited, tenant, controlID, requestedAt, approvedAt, expiresAt)
	exec(`
		INSERT INTO exception_audit_log (id, tenant_id, exception_id, action, actor, from_state, to_state, occurred_at)
		VALUES ($1, $2, $3, 'activated', 'approver@demo.example', 'approved', 'active', $4)
	`, uuid.NewString(), tenant, audited, activatedAt)

	if _, err := tx.Exec(ctx, string(up)); err != nil {
		t.Fatalf("up migration over old demo-seed rows: %v", err)
	}

	stamps := func(id string) (activated, expired *time.Time) {
		t.Helper()
		if err := tx.QueryRow(ctx,
			`SELECT activated_at, expired_at FROM exceptions WHERE id = $1`, id,
		).Scan(&activated, &expired); err != nil {
			t.Fatalf("read stamps of %s: %v", id, err)
		}
		return activated, expired
	}
	same := func(got *time.Time, want time.Time) bool { return got != nil && got.Equal(want) }

	if a, e := stamps(oldDemo); !same(a, approvedAt) || !same(e, expiresAt) {
		t.Errorf("old demo row: activated_at = %v, expired_at = %v; want approved_at %s and expires_at %s", a, e, approvedAt, expiresAt)
	}
	if a, e := stamps(earlyUpdate); !same(a, requestedAt.Add(day)) || !same(e, expiresAt.Add(-day)) {
		t.Errorf("early-update row: activated_at = %v, expired_at = %v; want effective_from and updated_at", a, e)
	}
	if a, e := stamps(audited); !same(a, activatedAt) || e != nil {
		t.Errorf("audited row: activated_at = %v, expired_at = %v; want the audit time %s and NULL", a, e, activatedAt)
	}

	// The CHECKs hold: an unstamped active row is refused after the
	// migration.
	_, err = tx.Exec(ctx, `
		INSERT INTO exceptions (
			id, tenant_id, control_id, scope_cell_predicate, justification,
			requested_by, expires_at, status
		)
		VALUES ($1, $2, $3, '{"op":"true"}'::jsonb, 'unstamped', 'demo@demo.example', $4, 'active')
	`, uuid.NewString(), tenant, controlID, expiresAt)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != pgErrCheckViolation || pgErr.ConstraintName != "exceptions_activated_has_stamp" {
		t.Fatalf("unstamped active row after the migration: want %s to refuse it, got %v", "exceptions_activated_has_stamp", err)
	}
}
