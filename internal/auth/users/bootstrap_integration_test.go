//go:build integration

// Integration tests for slice 198 OIDC-first-install bootstrap.
//
// Verifies:
//   - Bootstrap on empty tenants table: all 5 rows land atomically
//     (tenants + users + user_roles + super_admins + me_audit_log).
//   - Second call (post-bootstrap): Bootstrapped=false; no new rows.
//   - Concurrent first-installers: exactly one bootstrap winner;
//     loser's Bootstrapped=false.
//
// Run via: just test-integration  (sets DATABASE_URL_APP + DATABASE_URL).
//
// CI-delta note (per slice 198 D4): these tests REQUIRE an empty
// tenants table on entry. The CI integration harness does NOT seed
// tenants (only migrations + bootstrap roles SQL are applied); the
// initial state is guaranteed empty. Each sub-test cleans up its
// bootstrap row + cascaded children so subsequent sub-tests start
// clean. The test file declares `go:build integration` so it
// compiles only when the integration tag is set.
package users_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/auth/users"
)

var (
	bootstrapAppPool   *pgxpool.Pool
	bootstrapAdminPool *pgxpool.Pool
)

func TestMain(m *testing.M) {
	appURL := os.Getenv("DATABASE_URL_APP")
	adminURL := os.Getenv("DATABASE_URL")
	if appURL == "" || adminURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL_APP or DATABASE_URL not set; skipping slice 198 bootstrap integration tests")
		os.Exit(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, err := pgxpool.New(ctx, appURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pgxpool.New app: %v\n", err)
		os.Exit(1)
	}
	bootstrapAppPool = p
	a, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pgxpool.New admin: %v\n", err)
		os.Exit(1)
	}
	bootstrapAdminPool = a
	code := m.Run()
	p.Close()
	a.Close()
	os.Exit(code)
}

// cleanupBootstrap removes ALL bootstrap-related rows so sub-tests can
// reset to the empty-tenants state. The cleanup order respects the
// implicit dependency chain: me_audit_log → user_roles → super_admins →
// users → tenants. Errors are ignored — the bootstrap state may have
// been partially-rolled-back and individual rows may not exist.
func cleanupBootstrap(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	// Delete in dependency-order; tenants last since users/user_roles
	// reference it. me_audit_log has FORCE RLS without a delete policy,
	// so we use the BYPASSRLS admin pool.
	stmts := []string{
		`DELETE FROM me_audit_log WHERE action = 'bootstrap_first_install'`,
		`DELETE FROM super_admins`,
		`DELETE FROM user_roles`,
		`DELETE FROM local_credentials`,
		`DELETE FROM users`,
		`DELETE FROM tenants`,
	}
	for _, s := range stmts {
		if _, err := bootstrapAdminPool.Exec(ctx, s); err != nil {
			t.Logf("cleanup %s: %v (continuing)", s, err)
		}
	}
}

// ===== AC-8a: bootstrap lands all 5 rows atomically =====

func TestBootstrap_EmptyTenants_LandsAllRows(t *testing.T) {
	cleanupBootstrap(t)
	t.Cleanup(func() { cleanupBootstrap(t) })

	store := users.NewStoreWithAuthPool(bootstrapAppPool, bootstrapAdminPool)
	in := users.BootstrapInput{
		Email:       "founder@example.com",
		DisplayName: "Founder",
		Issuer:      "https://idp.example.com/",
		Subject:     "oidc-subject-founder",
	}
	res, err := store.BootstrapFirstInstallOrUpsert(context.Background(), in)
	if err != nil {
		t.Fatalf("BootstrapFirstInstallOrUpsert: %v", err)
	}
	if !res.Bootstrapped {
		t.Fatalf("expected Bootstrapped=true on empty tenants; got false")
	}
	if res.TenantID == uuid.Nil {
		t.Fatalf("expected non-nil TenantID on bootstrap")
	}
	if res.UserID == uuid.Nil {
		t.Fatalf("expected non-nil UserID on bootstrap")
	}

	ctx := context.Background()

	// AC-8a verifications — one row in each downstream table.
	var tenantCount int
	if err := bootstrapAdminPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM tenants WHERE id = $1 AND is_bootstrap_tenant = true AND name = $2`,
		res.TenantID, users.DefaultBootstrapTenantName,
	).Scan(&tenantCount); err != nil {
		t.Fatalf("count tenants: %v", err)
	}
	if tenantCount != 1 {
		t.Errorf("tenants row count = %d, want 1", tenantCount)
	}

	var userCount int
	if err := bootstrapAdminPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM users WHERE id = $1 AND tenant_id = $2 AND idp_issuer = $3 AND idp_subject = $4`,
		res.UserID, res.TenantID, in.Issuer, in.Subject,
	).Scan(&userCount); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if userCount != 1 {
		t.Errorf("users row count = %d, want 1", userCount)
	}

	var roleCount int
	if err := bootstrapAdminPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM user_roles WHERE tenant_id = $1 AND user_id = $2 AND role = 'admin'`,
		res.TenantID, res.UserID.String(),
	).Scan(&roleCount); err != nil {
		t.Fatalf("count user_roles: %v", err)
	}
	if roleCount != 1 {
		t.Errorf("user_roles row count = %d, want 1", roleCount)
	}

	var superCount int
	if err := bootstrapAdminPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM super_admins WHERE user_id = $1 AND granted_via = 'bootstrap_first_install'`,
		res.UserID,
	).Scan(&superCount); err != nil {
		t.Fatalf("count super_admins: %v", err)
	}
	if superCount != 1 {
		t.Errorf("super_admins row count = %d, want 1", superCount)
	}

	var auditCount int
	if err := bootstrapAdminPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM me_audit_log WHERE tenant_id = $1 AND user_id = $2 AND action = 'bootstrap_first_install'`,
		res.TenantID, res.UserID,
	).Scan(&auditCount); err != nil {
		t.Fatalf("count me_audit_log: %v", err)
	}
	if auditCount != 1 {
		t.Errorf("me_audit_log row count = %d, want 1", auditCount)
	}
}

// ===== AC-8b: second call after first returns Bootstrapped=false =====

func TestBootstrap_PostFirstInstall_FallsThrough(t *testing.T) {
	cleanupBootstrap(t)
	t.Cleanup(func() { cleanupBootstrap(t) })

	store := users.NewStoreWithAuthPool(bootstrapAppPool, bootstrapAdminPool)

	// First call: bootstraps.
	in1 := users.BootstrapInput{
		Email:       "founder@example.com",
		DisplayName: "Founder",
		Issuer:      "https://idp.example.com/",
		Subject:     "oidc-subject-founder",
	}
	res1, err := store.BootstrapFirstInstallOrUpsert(context.Background(), in1)
	if err != nil {
		t.Fatalf("first bootstrap: %v", err)
	}
	if !res1.Bootstrapped {
		t.Fatalf("first bootstrap should have Bootstrapped=true")
	}

	// Second call (different OIDC subject): count gate fires; result
	// Bootstrapped=false. The caller (OIDC callback handler) falls
	// through to UpsertOIDC under the EXISTING tenant.
	in2 := users.BootstrapInput{
		Email:       "second@example.com",
		DisplayName: "Second",
		Issuer:      "https://idp.example.com/",
		Subject:     "oidc-subject-second",
	}
	res2, err := store.BootstrapFirstInstallOrUpsert(context.Background(), in2)
	if err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}
	if res2.Bootstrapped {
		t.Fatalf("second call should have Bootstrapped=false; got true")
	}
	if res2.TenantID != uuid.Nil {
		t.Errorf("second call TenantID = %s, want Nil", res2.TenantID)
	}
	if res2.UserID != uuid.Nil {
		t.Errorf("second call UserID = %s, want Nil", res2.UserID)
	}

	// Verify NO additional bootstrap rows landed.
	ctx := context.Background()
	var tenantCount int
	if err := bootstrapAdminPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM tenants WHERE is_bootstrap_tenant = true`,
	).Scan(&tenantCount); err != nil {
		t.Fatalf("count tenants: %v", err)
	}
	if tenantCount != 1 {
		t.Errorf("bootstrap-tenant count = %d, want 1 (no extra rows)", tenantCount)
	}

	var superCount int
	if err := bootstrapAdminPool.QueryRow(ctx, `SELECT COUNT(*) FROM super_admins`).Scan(&superCount); err != nil {
		t.Fatalf("count super_admins: %v", err)
	}
	if superCount != 1 {
		t.Errorf("super_admins count = %d, want 1 (no extra rows)", superCount)
	}
}

// ===== AC-8c: concurrent first-installers — exactly one wins =====

func TestBootstrap_ConcurrentFirstInstallers_SerializesViaUniqueIndex(t *testing.T) {
	cleanupBootstrap(t)
	t.Cleanup(func() { cleanupBootstrap(t) })

	store := users.NewStoreWithAuthPool(bootstrapAppPool, bootstrapAdminPool)

	const N = 5
	results := make([]users.BootstrapResult, N)
	errs := make([]error, N)
	var wg sync.WaitGroup
	wg.Add(N)

	// Use a barrier to start all goroutines as close to simultaneously
	// as possible. The first one through wins; the others observe the
	// 23505 unique_violation OR the count gate.
	start := make(chan struct{})
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			<-start
			in := users.BootstrapInput{
				Email:       fmt.Sprintf("racer-%d@example.com", i),
				DisplayName: fmt.Sprintf("Racer %d", i),
				Issuer:      "https://idp.example.com/",
				Subject:     fmt.Sprintf("oidc-subject-racer-%d", i),
			}
			results[i], errs[i] = store.BootstrapFirstInstallOrUpsert(context.Background(), in)
		}()
	}
	close(start)
	wg.Wait()

	// Verify no errors.
	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d returned err: %v", i, err)
		}
	}
	if t.Failed() {
		return
	}

	// Exactly one Bootstrapped=true.
	winners := 0
	losers := 0
	for _, res := range results {
		if res.Bootstrapped {
			winners++
		} else {
			losers++
		}
	}
	if winners != 1 {
		t.Errorf("winners = %d, want exactly 1", winners)
	}
	if losers != N-1 {
		t.Errorf("losers = %d, want %d", losers, N-1)
	}

	// Exactly one tenants row with is_bootstrap_tenant=true.
	var tenantCount int
	if err := bootstrapAdminPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM tenants WHERE is_bootstrap_tenant = true`,
	).Scan(&tenantCount); err != nil {
		t.Fatalf("count tenants: %v", err)
	}
	if tenantCount != 1 {
		t.Errorf("bootstrap-tenant count = %d, want 1 (partial UNIQUE index serializes)", tenantCount)
	}

	// Exactly one super_admins row.
	var superCount int
	if err := bootstrapAdminPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM super_admins`,
	).Scan(&superCount); err != nil {
		t.Fatalf("count super_admins: %v", err)
	}
	if superCount != 1 {
		t.Errorf("super_admins count = %d, want 1", superCount)
	}
}

// ===== AC-7: DBUserResolver consults super_admins after bootstrap =====
//
// The bootstrap branch inserts into super_admins. The slice 198 lookup
// in DBUserResolver.lookupSuperAdmin should then see the row and
// populate the JWT super_admin claim accordingly. We run a direct
// query against the same super_admins table the resolver consults —
// this asserts the schema + grant contract rather than reaching into
// the oauth package (which would create a cyclic test dependency).

func TestBootstrap_SuperAdminsLookup_FindsGranteeAfterBootstrap(t *testing.T) {
	cleanupBootstrap(t)
	t.Cleanup(func() { cleanupBootstrap(t) })

	store := users.NewStoreWithAuthPool(bootstrapAppPool, bootstrapAdminPool)
	in := users.BootstrapInput{
		Email:       "founder@example.com",
		DisplayName: "Founder",
		Issuer:      "https://idp.example.com/",
		Subject:     "oidc-subject-founder",
	}
	res, err := store.BootstrapFirstInstallOrUpsert(context.Background(), in)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	// Mimic the DBUserResolver.lookupSuperAdmin query verbatim (via
	// the BYPASSRLS authPool, since super_admins is platform-global).
	var count int
	err = bootstrapAdminPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM super_admins WHERE user_id = ANY($1)`,
		[]uuid.UUID{res.UserID},
	).Scan(&count)
	if err != nil {
		t.Fatalf("lookup super_admins: %v", err)
	}
	if count != 1 {
		t.Errorf("super_admins lookup count = %d, want 1", count)
	}
}

// ===== bonus: ErrBootstrapUnavailable when no authPool =====

func TestBootstrap_NoAuthPool_ReturnsErrSentinel(t *testing.T) {
	store := users.NewStore(bootstrapAppPool) // no authPool
	res, err := store.BootstrapFirstInstallOrUpsert(context.Background(), users.BootstrapInput{
		Email:       "x@example.com",
		DisplayName: "X",
		Issuer:      "https://idp/",
		Subject:     "x",
	})
	if err == nil {
		t.Fatal("expected ErrBootstrapUnavailable; got nil")
	}
	if !errors.Is(err, users.ErrBootstrapUnavailable) {
		t.Errorf("expected ErrBootstrapUnavailable; got %v", err)
	}
	if res.Bootstrapped {
		t.Errorf("expected Bootstrapped=false on error")
	}
}
