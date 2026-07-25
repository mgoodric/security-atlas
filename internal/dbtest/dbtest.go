//go:build integration

// Package dbtest is the shared integration-test DB/tenant harness for
// security-atlas. It extracts the pool / tenant-seed / tenant-context
// boilerplate that ~80 integration suites under internal/ each re-derive
// (slice 435; the rediscovery cost named in slice 353's Q-2).
//
// # Role model (load-bearing — preserves CLAUDE.md invariant #6)
//
// The harness reproduces the exact two-pool role model the canonical
// internal/db/integration_test.go TestMain established:
//
//   - NewAppPool   — connects as the RLS-enforcing application role
//     (atlas_app, NOSUPERUSER NOBYPASSRLS) via DATABASE_URL_APP. This is
//     the DEFAULT pool: every RLS-bound query and every cross-tenant
//     isolation assertion runs through it, so Row-Level Security is
//     actually exercised.
//   - NewMigratePool — connects as the privileged BYPASSRLS admin role
//     (DATABASE_URL; in CI the postgres superuser, on a self-host bundle
//     atlas_migrate). It exists ONLY for cleaning append-only tables the
//     app role intentionally cannot DELETE from (evidence_records,
//     evidence_audit_log, control_drift_snapshots — the slice-013
//     append-only RLS shape) and for seeding fixtures across tenants.
//
// The two constructors are SEPARATELY NAMED on purpose (slice 435 AC-3 /
// the Elevation-of-privilege guard): the harness never silently hands back
// a privileged pool where an app-role pool is expected. Privilege is
// opt-in and spelled out at the call site. A test that wants RLS enforced
// calls NewAppPool; a test that wants BYPASSRLS cleanup calls
// NewMigratePool. There is deliberately no single NewTestPool(role) seam
// that could default to — or be fat-fingered into — the privileged pool.
//
// # RLS fidelity
//
// WithTenantCtx sets app.current_tenant with semantics identical to the
// inline tenancy.WithTenant pattern it replaces — it tags the context, and
// the dbx-backed Store (or an explicit tenancy.ApplyTenant inside a tx)
// applies the GUC per transaction. The negative test in dbtest_test.go
// proves a cross-tenant read is STILL DENIED through a NewAppPool pool
// (AC-7, the Information-disclosure guard).
package dbtest

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// appDSNEnv is the env var carrying the application-role (atlas_app) DSN —
// NOSUPERUSER NOBYPASSRLS, so RLS is enforced.
const appDSNEnv = "DATABASE_URL_APP"

// migrateDSNEnv is the env var carrying the privileged BYPASSRLS DSN, used
// only for append-only cleanup and cross-tenant fixture seeding.
const migrateDSNEnv = "DATABASE_URL"

// poolDialTimeout bounds the connect attempt so a misconfigured DSN fails
// fast rather than hanging the suite.
const poolDialTimeout = 10 * time.Second

// NewAppPool opens the RLS-enforcing application-role pool (atlas_app) from
// DATABASE_URL_APP and registers a t.Cleanup that closes it. This is the
// DEFAULT pool for integration suites: RLS-bound reads and the cross-tenant
// isolation assertions run through it, so Row-Level Security is exercised
// exactly as production enforces it.
//
// If DATABASE_URL_APP is unset the test is skipped (the standard
// integration-suite contract), matching the inline appDSN()/openPool()
// idiom this replaces.
func NewAppPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return openPool(t, requireDSN(t, appDSNEnv))
}

// NewMigratePool opens the privileged BYPASSRLS admin pool from
// DATABASE_URL and registers a t.Cleanup that closes it. Use it ONLY for
// cleaning append-only tables the app role cannot DELETE from
// (evidence_records, evidence_audit_log, control_drift_snapshots) and for
// seeding fixtures across tenants. It is SEPARATELY NAMED from NewAppPool
// so a caller cannot reach the privileged pool by accident (AC-3, the
// Elevation-of-privilege guard) — never use it for an RLS assertion, or
// the assertion proves nothing.
//
// If DATABASE_URL is unset the test is skipped.
func NewMigratePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return openPool(t, requireDSN(t, migrateDSNEnv))
}

// requireDSN reads env and skips the test when it is unset — the canonical
// "DATABASE_URL_APP not set; skipping integration test" contract.
func requireDSN(t *testing.T, env string) string {
	t.Helper()
	v := os.Getenv(env)
	if v == "" {
		t.Skipf("%s not set; skipping integration test", env)
	}
	return v
}

// openPool dials a pgxpool with a bounded timeout and registers cleanup.
func openPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), poolDialTimeout)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("dbtest: pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// SeedTenant returns a brand-new tenant id (a fresh UUID per call, so no
// seed leaks across tests — the Tampering guard) and registers a t.Cleanup
// that DELETEs every row written under it, in the table order the caller
// supplies. The caller passes the cleanup tables in FK-safe order
// (children before parents); SeedTenant deletes `WHERE tenant_id = $1`
// through the supplied privileged pool.
//
// The privileged (migrate) pool is required for cleanup specifically
// because some tables are append-only under RLS for the app role
// (evidence_records, control_drift_snapshots — slice 013); a migrate pool
// is the only role that can DELETE them. Passing the migrate pool here is
// the explicit, named privilege escalation — it never happens implicitly.
//
// SeedTenant does NOT insert a tenant row: in this schema there is no
// standalone `tenants` table that RLS-scoped fixtures key off — the tenant
// id is a free UUID that rows carry in their tenant_id column and the
// app.current_tenant GUC selects on. This matches the inline freshTenant()
// idiom the migrated suites used.
func SeedTenant(t *testing.T, migrate *pgxpool.Pool, cleanupTables ...string) string {
	t.Helper()
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), poolDialTimeout)
		defer cancel()
		for _, table := range cleanupTables {
			// Table names are caller-supplied compile-time constants, never
			// user input; the tenant id is a bound parameter.
			stmt := "DELETE FROM " + table + " WHERE tenant_id = $1"
			if _, err := migrate.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("dbtest cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

// WithTenantCtx returns a context tagged with tenant via tenancy.WithTenant
// — the SAME path production uses to carry tenant identity. The dbx-backed
// Store (or an explicit tenancy.ApplyTenant inside a transaction) then sets
// the app.current_tenant GUC per transaction from this context. Semantics
// are identical to the inline `ctx, _ := tenancy.WithTenant(...)` pattern
// the migrated suites used; AC-2 verifies this behaviorally and AC-7 proves
// the resulting RLS denial holds.
//
// A non-UUID tenant fails the test loudly (tenancy.WithTenant rejects it),
// so a malformed tenant id cannot quietly bypass RLS.
func WithTenantCtx(t *testing.T, tenant string) context.Context {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("dbtest: WithTenant(%q): %v", tenant, err)
	}
	return ctx
}
