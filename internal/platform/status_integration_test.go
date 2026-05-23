//go:build integration

// Integration tests for slice 073: the platform_status singleton + the
// IsFirstInstall / MarkFirstSignin / ResetBootstrap helpers. Real
// Postgres only — the singleton CHECK constraint, RLS public-read, and
// the elevated-only write path are only meaningful against a real database.
// The DB is never mocked.
//
// Run with: go test -tags=integration -race ./internal/platform/...
//
// Required env:
//   DATABASE_URL      - migration role DSN (BYPASSRLS); used as the write
//                       pool and to verify the singleton constraint.
//   DATABASE_URL_APP  - application role DSN (NOSUPERUSER NOBYPASSRLS); used
//                       to verify the public_read RLS policy and to confirm
//                       atlas_app cannot write.

package platform_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/platform"
)

func adminDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL")
	if v == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	return v
}

func appDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL_APP")
	if v == "" {
		t.Skip("DATABASE_URL_APP not set; skipping integration test")
	}
	return v
}

func openPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

// resetPlatformStatus restores the singleton row to its post-migration
// state (both timestamps NULL). Tests that flip the marker call this in
// a t.Cleanup. Uses the migrate pool because atlas_app has no UPDATE
// policy.
func resetPlatformStatus(t *testing.T, admin *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	_, err := admin.Exec(ctx, `UPDATE platform_status
        SET first_signin_at = NULL, bootstrap_token_consumed_at = NULL`)
	if err != nil {
		t.Fatalf("reset platform_status: %v", err)
	}
}

// TestIsFirstInstall_PublicReadFromAppPool covers AC-14(a): the public_read
// RLS policy lets atlas_app read the singleton row, and a fresh post-
// migration state reports first_install=true.
func TestIsFirstInstall_PublicReadFromAppPool(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	resetPlatformStatus(t, admin)

	s := platform.NewStatus(app, admin)
	got, err := s.IsFirstInstall(context.Background())
	if err != nil {
		t.Fatalf("IsFirstInstall: %v", err)
	}
	if !got {
		t.Fatalf("IsFirstInstall = false; want true on fresh-install state")
	}
}

// TestMarkFirstSignin_FlipsMarker covers AC-14(b): MarkFirstSignin
// flips first_signin_at and the subsequent IsFirstInstall returns false.
func TestMarkFirstSignin_FlipsMarker(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	resetPlatformStatus(t, admin)
	t.Cleanup(func() { resetPlatformStatus(t, admin) })

	s := platform.NewStatus(app, admin)
	ts := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	did, err := s.MarkFirstSignin(context.Background(), ts)
	if err != nil {
		t.Fatalf("MarkFirstSignin: %v", err)
	}
	if !did {
		t.Fatalf("didWrite = false; want true on the first flip")
	}

	got, err := s.IsFirstInstall(context.Background())
	if err != nil {
		t.Fatalf("IsFirstInstall: %v", err)
	}
	if got {
		t.Fatalf("IsFirstInstall = true; want false after MarkFirstSignin")
	}
}

// TestMarkFirstSignin_Idempotent covers AC-14(c): a second
// MarkFirstSignin call is a no-op (didWrite=false, timestamp unchanged).
func TestMarkFirstSignin_Idempotent(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	resetPlatformStatus(t, admin)
	t.Cleanup(func() { resetPlatformStatus(t, admin) })

	s := platform.NewStatus(app, admin)
	firstTs := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	if _, err := s.MarkFirstSignin(context.Background(), firstTs); err != nil {
		t.Fatalf("first MarkFirstSignin: %v", err)
	}
	secondTs := firstTs.Add(time.Hour)
	did, err := s.MarkFirstSignin(context.Background(), secondTs)
	if err != nil {
		t.Fatalf("second MarkFirstSignin: %v", err)
	}
	if did {
		t.Fatalf("didWrite = true on second call; want false")
	}

	// Confirm the timestamp is the FIRST one, not the second.
	var stored time.Time
	err = admin.QueryRow(context.Background(), `SELECT first_signin_at FROM platform_status`).Scan(&stored)
	if err != nil {
		t.Fatalf("read platform_status: %v", err)
	}
	if !stored.Equal(firstTs) {
		t.Fatalf("first_signin_at = %v; want %v (first call's timestamp, idempotent)", stored, firstTs)
	}
}

// TestSingletonConstraint covers AC-14(d): the singleton_lock CHECK +
// PRIMARY KEY admits only one row.
func TestSingletonConstraint(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	resetPlatformStatus(t, admin)

	_, err := admin.Exec(context.Background(),
		`INSERT INTO platform_status (singleton_lock) VALUES (TRUE)`)
	if err == nil {
		t.Fatalf("second INSERT succeeded; expected primary-key collision")
	}
}

// TestAppPoolCannotWrite covers the load-bearing P0 safety property:
// atlas_app has no INSERT/UPDATE/DELETE policy on platform_status under
// FORCE ROW LEVEL SECURITY. An UPDATE attempt from the app pool reports
// zero rows affected (RLS-filtered).
func TestAppPoolCannotWrite(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	resetPlatformStatus(t, admin)
	t.Cleanup(func() { resetPlatformStatus(t, admin) })

	// The app pool may also lack UPDATE GRANT — either way the write must
	// not succeed. We accept either path: a permission error from the
	// GRANT layer, or an RLS-filtered zero-rows result.
	tag, err := app.Exec(context.Background(),
		`UPDATE platform_status SET first_signin_at = now()`)
	if err == nil && tag.RowsAffected() != 0 {
		t.Fatalf("app pool wrote %d rows to platform_status; want 0 (RLS or GRANT must block)", tag.RowsAffected())
	}

	// Confirm the read still sees the fresh-install state.
	s := platform.NewStatus(app, admin)
	got, err := s.IsFirstInstall(context.Background())
	if err != nil {
		t.Fatalf("IsFirstInstall after app-pool write attempt: %v", err)
	}
	if !got {
		t.Fatalf("IsFirstInstall = false; the app-pool write should not have taken effect")
	}
}

// TestResetBootstrap_RefusesWithoutForce covers AC-8's foot-gun gate.
func TestResetBootstrap_RefusesWithoutForce(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	resetPlatformStatus(t, admin)
	t.Cleanup(func() { resetPlatformStatus(t, admin) })

	s := platform.NewStatus(app, admin)
	if _, err := s.MarkFirstSignin(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("MarkFirstSignin: %v", err)
	}

	err := s.ResetBootstrap(context.Background(), false)
	if err == nil {
		t.Fatalf("ResetBootstrap without --force succeeded; want ErrResetForbidden")
	}
}

// TestResetBootstrap_ForceClearsBoth covers AC-8 with --force.
func TestResetBootstrap_ForceClearsBoth(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	resetPlatformStatus(t, admin)
	t.Cleanup(func() { resetPlatformStatus(t, admin) })

	s := platform.NewStatus(app, admin)
	if _, err := s.MarkFirstSignin(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("MarkFirstSignin: %v", err)
	}

	if err := s.ResetBootstrap(context.Background(), true); err != nil {
		t.Fatalf("ResetBootstrap --force: %v", err)
	}

	got, err := s.IsFirstInstall(context.Background())
	if err != nil {
		t.Fatalf("IsFirstInstall after reset: %v", err)
	}
	if !got {
		t.Fatalf("IsFirstInstall = false; want true after --force reset")
	}
}

// resetBootstrapFixtures clears `tenants` and `users` rows that the
// slice-210 BootstrapTenantID tests touch. Run as both setup and
// cleanup so each subtest sees a known-empty state. Uses the migrate
// pool because RLS would hide most rows from atlas_app.
func resetBootstrapFixtures(t *testing.T, admin *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	// users → user_roles + cascades. Wipe in dependency order.
	for _, stmt := range []string{
		`DELETE FROM user_tenants`,
		`DELETE FROM user_roles`,
		`DELETE FROM local_credentials`,
		`DELETE FROM users`,
		`DELETE FROM tenants`,
	} {
		if _, err := admin.Exec(ctx, stmt); err != nil {
			t.Fatalf("reset (%s): %v", stmt, err)
		}
	}
}

// TestStatus_BootstrapTenantID_PrimaryQueryHitsTenantsRow exercises
// the slice-210 primary lookup: when a `tenants` row exists with
// `is_bootstrap_tenant=true`, BootstrapTenantID returns its id without
// touching the users-fallback path.
func TestStatus_BootstrapTenantID_PrimaryQueryHitsTenantsRow(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	resetBootstrapFixtures(t, admin)
	t.Cleanup(func() { resetBootstrapFixtures(t, admin) })

	want := uuid.MustParse("d0000000-0000-4000-8000-000000000001")
	_, err := admin.Exec(context.Background(),
		`INSERT INTO tenants (id, name, is_bootstrap_tenant) VALUES ($1, 'Default Tenant', TRUE)`,
		want)
	if err != nil {
		t.Fatalf("seed tenants row: %v", err)
	}

	s := platform.NewStatus(app, admin)
	got, err := s.BootstrapTenantID(context.Background())
	if err != nil {
		t.Fatalf("BootstrapTenantID: %v", err)
	}
	if got != want {
		t.Fatalf("BootstrapTenantID = %s; want %s", got, want)
	}
}

// TestStatus_BootstrapTenantID_FallbackToUsers exercises the
// slice-210 fallback: with no `tenants` row, the method falls back to
// the oldest user's tenant_id. This is the path the live atlas-edge
// instance (pre-slice-210 seed.sql) walks until its next re-bootstrap.
func TestStatus_BootstrapTenantID_FallbackToUsers(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	resetBootstrapFixtures(t, admin)
	t.Cleanup(func() { resetBootstrapFixtures(t, admin) })

	want := uuid.MustParse("e0000000-0000-4000-8000-000000000001")
	// Insert a users row with NO matching tenants row (the pre-slice-210
	// state). The oldest user is by created_at; we rely on the column
	// default of now() — only one user exists, so it's trivially oldest.
	_, err := admin.Exec(context.Background(),
		`INSERT INTO users (id, tenant_id, email, display_name, status)
		 VALUES ($1, $2, $3, $4, 'active')`,
		uuid.MustParse("e0000000-0000-4000-8000-000000000099"),
		want,
		"bootstrap@example.com",
		"Bootstrap")
	if err != nil {
		t.Fatalf("seed users row: %v", err)
	}

	s := platform.NewStatus(app, admin)
	got, err := s.BootstrapTenantID(context.Background())
	if err != nil {
		t.Fatalf("BootstrapTenantID (fallback): %v", err)
	}
	if got != want {
		t.Fatalf("BootstrapTenantID = %s; want %s (fallback to oldest user's tenant)", got, want)
	}
}

// TestStatus_BootstrapTenantID_NoBootstrapAtAll exercises the empty
// state: no tenants, no users. Returns (uuid.Nil, nil) so the
// install-state handler omits the field gracefully.
func TestStatus_BootstrapTenantID_NoBootstrapAtAll(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	resetBootstrapFixtures(t, admin)
	t.Cleanup(func() { resetBootstrapFixtures(t, admin) })

	s := platform.NewStatus(app, admin)
	got, err := s.BootstrapTenantID(context.Background())
	if err != nil {
		t.Fatalf("BootstrapTenantID (empty install): %v", err)
	}
	if got != uuid.Nil {
		t.Fatalf("BootstrapTenantID = %s; want uuid.Nil", got)
	}
}
