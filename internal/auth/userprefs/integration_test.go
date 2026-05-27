//go:build integration

// Integration tests for the slice 108 userprefs store
// (internal/auth/userprefs).
//
// Load-bearing functions + branches covered:
//
//   - Store.Get: empty user → DefaultMatrix (all enabled); populated
//     user → the persisted matrix layered on top of defaults; commits
//     the read-only transaction cleanly.
//   - Store.Upsert: full matrix insert; partial-matrix merge (PATCH
//     semantics); ErrUnknownEvent on whitelist miss; ErrUnknownChannel
//     on whitelist miss; pre-flight validation fires BEFORE any DB
//     write so a partial bad input leaves no partial state behind.
//   - RLS isolation: Tenant A cannot read Tenant B's preferences.
//
// `user_notification_preferences` IS tenant-scoped (slice 108
// migration ENABLEs and FORCEs RLS with the canonical four-policy
// pattern), so every test seeds via the admin pool (BYPASSRLS) and
// reads via the app pool with the tenant GUC applied through
// tenancy.WithTenant + tenancy.ApplyTenant (in the production code
// path).
//
// Run via: just test-integration  (sets DATABASE_URL_APP +
// DATABASE_URL).
package userprefs_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/auth/userprefs"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// openPools opens both the atlas_app (RLS-enforced) pool and the
// admin (BYPASSRLS) pool. Skips when either env var is unset.
func openPools(t *testing.T) (app, admin *pgxpool.Pool) {
	t.Helper()
	appDSN := os.Getenv("DATABASE_URL_APP")
	adminDSN := os.Getenv("DATABASE_URL")
	if appDSN == "" || adminDSN == "" {
		t.Skip("DATABASE_URL_APP or DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a, err := pgxpool.New(ctx, appDSN)
	if err != nil {
		t.Fatalf("pgxpool.New(app): %v", err)
	}
	t.Cleanup(a.Close)
	b, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatalf("pgxpool.New(admin): %v", err)
	}
	t.Cleanup(b.Close)
	return a, b
}

// seedUser inserts a fresh (tenant_id, user_id) pair via the admin
// pool (RLS-bypass) so the test scope is deterministic. Returns the
// IDs and registers cleanup that deletes the rows in dependency
// order.
func seedUser(t *testing.T, admin *pgxpool.Pool) (uuid.UUID, uuid.UUID) {
	t.Helper()
	tenantID := uuid.New()
	userID := uuid.New()
	ctx := context.Background()
	if _, err := admin.Exec(ctx, `
		INSERT INTO users (id, tenant_id, email, display_name, status, time_zone)
		VALUES ($1, $2, $3, $4, 'active', '')
	`, userID, tenantID, "ut-userprefs-"+userID.String()+"@example.test", "UT User"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		for _, stmt := range []string{
			`DELETE FROM user_notification_preferences WHERE tenant_id = $1`,
			`DELETE FROM users WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenantID); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenantID, userID
}

// tenantCtx wraps ctx with the tenant GUC binding the store's
// transactions need.
func tenantCtx(t *testing.T, ctx context.Context, tenantID uuid.UUID) context.Context {
	t.Helper()
	out, err := tenancy.WithTenant(ctx, tenantID.String())
	if err != nil {
		t.Fatalf("tenancy.WithTenant: %v", err)
	}
	return out
}

// TestGetEmptyReturnsDefaultMatrix: a fresh user with zero
// preference rows reads as all-events × all-channels = enabled.
func TestGetEmptyReturnsDefaultMatrix(t *testing.T) {
	app, admin := openPools(t)
	s := userprefs.NewStore(app)

	tenantID, userID := seedUser(t, admin)
	ctx := tenantCtx(t, context.Background(), tenantID)

	got, err := s.Get(ctx, tenantID, userID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := userprefs.DefaultMatrix()
	if len(got) != len(want) {
		t.Fatalf("Get: matrix has %d events, want %d", len(got), len(want))
	}
	for _, ev := range userprefs.Events {
		for _, ch := range userprefs.Channels {
			if !got[ev][ch] {
				t.Errorf("Get(empty user)[%q][%q] = false; want true (default)", ev, ch)
			}
		}
	}
}

// TestUpsertPersistsAndGetRoundTrips: an Upsert with a non-default
// cell is reflected on the subsequent Get.
func TestUpsertPersistsAndGetRoundTrips(t *testing.T) {
	app, admin := openPools(t)
	s := userprefs.NewStore(app)

	tenantID, userID := seedUser(t, admin)
	ctx := tenantCtx(t, context.Background(), tenantID)

	// Disable the policy_ack_due / email cell only.
	patch := userprefs.Preferences{
		"policy_ack_due": {"email": false},
	}
	if err := s.Upsert(ctx, tenantID, userID, patch); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := s.Get(ctx, tenantID, userID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got["policy_ack_due"]["email"] {
		t.Error("policy_ack_due / email: got true after Upsert(false)")
	}
	// Untouched cells must remain at the default (true).
	if !got["policy_ack_due"]["in_app"] {
		t.Error("policy_ack_due / in_app: got false; want true (default, not touched)")
	}
	if !got["audit_period_assignment"]["email"] {
		t.Error("audit_period_assignment / email: got false; want true (default, not touched)")
	}
}

// TestUpsertFullMatrix: applying the DefaultMatrix as a patch results
// in N writes (one per cell) and the Get reads back the same shape.
func TestUpsertFullMatrix(t *testing.T) {
	app, admin := openPools(t)
	s := userprefs.NewStore(app)

	tenantID, userID := seedUser(t, admin)
	ctx := tenantCtx(t, context.Background(), tenantID)

	// Make a non-trivial full matrix (alternating true/false) to
	// exercise both enabled values through the upsert path.
	patch := userprefs.Preferences{}
	for i, ev := range userprefs.Events {
		patch[ev] = map[string]bool{}
		for j, ch := range userprefs.Channels {
			patch[ev][ch] = (i+j)%2 == 0
		}
	}
	if err := s.Upsert(ctx, tenantID, userID, patch); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := s.Get(ctx, tenantID, userID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	for i, ev := range userprefs.Events {
		for j, ch := range userprefs.Channels {
			want := (i+j)%2 == 0
			if got[ev][ch] != want {
				t.Errorf("Get()[%q][%q] = %v, want %v", ev, ch, got[ev][ch], want)
			}
		}
	}
}

// TestUpsertOverwritesPriorValue: the underlying SQL is UPSERT (ON
// CONFLICT DO UPDATE); a second call to the same cell with the
// opposite enabled value persists.
func TestUpsertOverwritesPriorValue(t *testing.T) {
	app, admin := openPools(t)
	s := userprefs.NewStore(app)

	tenantID, userID := seedUser(t, admin)
	ctx := tenantCtx(t, context.Background(), tenantID)

	if err := s.Upsert(ctx, tenantID, userID, userprefs.Preferences{
		"control_drift": {"in_app": false},
	}); err != nil {
		t.Fatalf("Upsert 1: %v", err)
	}
	if err := s.Upsert(ctx, tenantID, userID, userprefs.Preferences{
		"control_drift": {"in_app": true},
	}); err != nil {
		t.Fatalf("Upsert 2: %v", err)
	}
	got, err := s.Get(ctx, tenantID, userID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got["control_drift"]["in_app"] {
		t.Error("control_drift / in_app: got false after Upsert(true); want true (upsert overwrites)")
	}
}

// TestUpsertRejectsUnknownEvent: a non-whitelisted event key returns
// ErrUnknownEvent without touching the DB.
func TestUpsertRejectsUnknownEvent(t *testing.T) {
	app, admin := openPools(t)
	s := userprefs.NewStore(app)

	tenantID, userID := seedUser(t, admin)
	ctx := tenantCtx(t, context.Background(), tenantID)

	patch := userprefs.Preferences{
		"unknown_event_key": {"email": false},
	}
	err := s.Upsert(ctx, tenantID, userID, patch)
	if !errors.Is(err, userprefs.ErrUnknownEvent) {
		t.Fatalf("Upsert(unknown event): err = %v, want ErrUnknownEvent", err)
	}
}

// TestUpsertRejectsUnknownChannel: a non-whitelisted channel key
// returns ErrUnknownChannel.
func TestUpsertRejectsUnknownChannel(t *testing.T) {
	app, admin := openPools(t)
	s := userprefs.NewStore(app)

	tenantID, userID := seedUser(t, admin)
	ctx := tenantCtx(t, context.Background(), tenantID)

	patch := userprefs.Preferences{
		"policy_ack_due": {"sms": false},
	}
	err := s.Upsert(ctx, tenantID, userID, patch)
	if !errors.Is(err, userprefs.ErrUnknownChannel) {
		t.Fatalf("Upsert(unknown channel): err = %v, want ErrUnknownChannel", err)
	}
}

// TestUpsertPreFlightValidationDoesNotPartiallyWrite: a patch with
// one valid cell + one invalid cell must NOT write the valid cell —
// the validation runs before any DB write.
func TestUpsertPreFlightValidationDoesNotPartiallyWrite(t *testing.T) {
	app, admin := openPools(t)
	s := userprefs.NewStore(app)

	tenantID, userID := seedUser(t, admin)
	ctx := tenantCtx(t, context.Background(), tenantID)

	patch := userprefs.Preferences{
		"policy_ack_due":   {"email": false}, // valid
		"bogus_event_name": {"email": false}, // invalid
	}
	if err := s.Upsert(ctx, tenantID, userID, patch); !errors.Is(err, userprefs.ErrUnknownEvent) {
		t.Fatalf("Upsert(mixed): err = %v, want ErrUnknownEvent", err)
	}

	// Check via the admin pool (RLS-bypass) — the row count must be 0.
	var count int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM user_notification_preferences WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, userID,
	).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 0 {
		t.Errorf("partial-write check: count = %d, want 0 (pre-flight should reject before any write)", count)
	}
}

// TestRLSIsolationBetweenTenants: Tenant A's Upsert + Get must not
// see Tenant B's preferences. Tenant B reads as DefaultMatrix even
// after Tenant A writes a non-default cell.
func TestRLSIsolationBetweenTenants(t *testing.T) {
	app, admin := openPools(t)
	s := userprefs.NewStore(app)

	tenantA, userA := seedUser(t, admin)
	tenantB, userB := seedUser(t, admin)

	ctxA := tenantCtx(t, context.Background(), tenantA)
	ctxB := tenantCtx(t, context.Background(), tenantB)

	// Tenant A writes a non-default cell.
	if err := s.Upsert(ctxA, tenantA, userA, userprefs.Preferences{
		"audit_period_assignment": {"in_app": false},
	}); err != nil {
		t.Fatalf("Upsert A: %v", err)
	}

	// Tenant B reads via its own context — RLS must hide Tenant A's
	// row. Tenant B's view is the default matrix.
	gotB, err := s.Get(ctxB, tenantB, userB)
	if err != nil {
		t.Fatalf("Get B: %v", err)
	}
	if !gotB["audit_period_assignment"]["in_app"] {
		t.Error("RLS leak: Tenant B sees Tenant A's disabled cell")
	}

	// Sanity: Tenant A still sees its own write.
	gotA, err := s.Get(ctxA, tenantA, userA)
	if err != nil {
		t.Fatalf("Get A: %v", err)
	}
	if gotA["audit_period_assignment"]["in_app"] {
		t.Error("Tenant A: own write lost (got true after Upsert(false))")
	}
}
