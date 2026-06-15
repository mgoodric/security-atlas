//go:build integration

// Integration tests for the slice 435 dbtest harness. These are the
// RLS-fidelity guards: they prove the harness preserves CLAUDE.md
// invariant #6 (tenant isolation enforced at the DB layer via RLS) rather
// than silently weakening it.
//
// Run with: go test -tags=integration -p 1 ./internal/dbtest/...
//
// Required env:
//   DATABASE_URL_APP  — atlas_app (NOSUPERUSER NOBYPASSRLS) DSN.
//   DATABASE_URL      — privileged BYPASSRLS DSN (append-only cleanup).

package dbtest_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// insertControlAs seeds one control row under tenant through the given
// app-role pool, inside a tenant-scoped transaction (set_config applies the
// app.current_tenant GUC, so the INSERT is RLS-checked). Returns the new
// control id.
func insertControlAs(t *testing.T, app *pgxpool.Pool, tenant string) string {
	t.Helper()
	controlID := uuid.NewString()
	ctx := dbtest.WithTenantCtx(t, tenant)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	tx, err := app.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		t.Fatalf("ApplyTenant: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO controls (id, tenant_id, scf_id, title, control_family, implementation_type, bundle_id)
		VALUES ($1, $2, 'IAC-435', 'dbtest control', 'IAC', 'automated', $3)
	`, controlID, tenant, "legacy_"+controlID); err != nil {
		t.Fatalf("INSERT controls: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	return controlID
}

// countControlAs counts visible rows for controlID under tenant, through
// the given app-role pool inside a tenant-scoped (rolled-back) transaction.
func countControlAs(t *testing.T, app *pgxpool.Pool, tenant, controlID string) int {
	t.Helper()
	ctx := dbtest.WithTenantCtx(t, tenant)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	tx, err := app.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		t.Fatalf("ApplyTenant: %v", err)
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM controls WHERE id = $1`, controlID).Scan(&n); err != nil {
		t.Fatalf("SELECT count: %v", err)
	}
	return n
}

// TestRLS_CrossTenantRead_DeniedThroughAppPool is the load-bearing AC-7
// guard. Write a row under tenant A, switch context to tenant B via
// dbtest.WithTenantCtx, and assert the read returns ZERO rows — through a
// dbtest.NewAppPool pool. If this passes, the harness preserves RLS; if it
// cannot be made to FAIL by deliberately weakening WithTenantCtx (e.g.
// returning tenant A's id for the B read), the test is wrong.
//
// The slice-435 deliberate-weakening sanity check confirmed the test DOES
// fail when WithTenantCtx is weakened to reuse tenant A's context for the
// tenant-B read (the cross-tenant read then sees 1 row). See the decisions
// log (docs/audit-log/435-dbtest-harness-decisions.md).
func TestRLS_CrossTenantRead_DeniedThroughAppPool(t *testing.T) {
	app := dbtest.NewAppPool(t)
	migrate := dbtest.NewMigratePool(t)

	tenantA := dbtest.SeedTenant(t, migrate, "controls")
	tenantB := dbtest.SeedTenant(t, migrate, "controls")

	controlID := insertControlAs(t, app, tenantA)

	// Sanity: tenant A sees its own row (proves the seed committed and the
	// read path works — so a zero from tenant B means RLS, not a missing row).
	if got := countControlAs(t, app, tenantA, controlID); got != 1 {
		t.Fatalf("tenant A saw %d rows for own control; expected 1", got)
	}

	// The guard: tenant B must see ZERO rows of tenant A's control.
	if got := countControlAs(t, app, tenantB, controlID); got != 0 {
		t.Fatalf("tenant B saw %d rows for tenant A's control through a dbtest app pool; RLS bypassed", got)
	}
}

// TestWithTenantCtx_SetsGUC verifies AC-2: WithTenantCtx + ApplyTenant set
// the app.current_tenant GUC observably, so the cross-tenant test above
// cannot pass for the wrong reason (a silently-unset GUC denies everything).
func TestWithTenantCtx_SetsGUC(t *testing.T) {
	app := dbtest.NewAppPool(t)
	tenant := uuid.NewString()

	ctx := dbtest.WithTenantCtx(t, tenant)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	tx, err := app.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		t.Fatalf("ApplyTenant: %v", err)
	}

	var got string
	if err := tx.QueryRow(ctx, `SELECT current_setting($1, true)`, tenancy.GUCName).Scan(&got); err != nil {
		t.Fatalf("current_setting: %v", err)
	}
	if got != tenant {
		t.Fatalf("GUC = %q, want %q", got, tenant)
	}
}

// TestAppPool_CannotBypassRLS verifies AC-3 / the EoP guard at the pool
// level: a NewAppPool pool with NO tenant GUC set sees zero rows (the
// no-default-allow invariant). A privileged pool would see the row; the app
// pool must not. This proves NewAppPool returns the RLS-enforcing role, not
// a BYPASSRLS pool.
func TestAppPool_CannotBypassRLS(t *testing.T) {
	app := dbtest.NewAppPool(t)
	migrate := dbtest.NewMigratePool(t)

	tenant := dbtest.SeedTenant(t, migrate, "controls")
	controlID := insertControlAs(t, app, tenant)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := app.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	// GUC deliberately unset: this transaction never calls ApplyTenant.
	var setting *string
	if err := tx.QueryRow(ctx, `SELECT current_setting($1, true)`, tenancy.GUCName).Scan(&setting); err != nil {
		t.Fatalf("current_setting: %v", err)
	}
	if setting != nil && *setting != "" {
		t.Fatalf("GUC was %q; test cannot prove RLS", *setting)
	}

	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM controls WHERE id = $1`, controlID).Scan(&n); err != nil {
		t.Fatalf("SELECT count: %v", err)
	}
	if n != 0 {
		t.Fatalf("app pool saw %d rows with no tenant GUC; NewAppPool is not RLS-enforcing", n)
	}
}

// TestMigratePool_BypassesRLS is the positive counterpart: the migrate pool
// (BYPASSRLS) DOES see the row with no tenant GUC set — confirming the two
// constructors return DIFFERENT roles, so the separation is real and the
// EoP guard is meaningful.
func TestMigratePool_BypassesRLS(t *testing.T) {
	app := dbtest.NewAppPool(t)
	migrate := dbtest.NewMigratePool(t)

	tenant := dbtest.SeedTenant(t, migrate, "controls")
	controlID := insertControlAs(t, app, tenant)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var n int
	if err := migrate.QueryRow(ctx, `SELECT count(*) FROM controls WHERE id = $1`, controlID).Scan(&n); err != nil {
		t.Fatalf("SELECT count: %v", err)
	}
	if n != 1 {
		t.Fatalf("migrate pool saw %d rows with no tenant GUC; expected 1 (BYPASSRLS). "+
			"NewMigratePool may not be the privileged role", n)
	}
}
