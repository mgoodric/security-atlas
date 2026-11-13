//go:build integration

// Integration tests for security-atlas#1759: the bootstrap tenant is
// canonical, the OIDC first-login path resolves the implied tenant instead
// of creating its own, and the upgrade migration makes the bootstrap admin
// super-admin on a single-tenant install.
//
// Shares TestMain + cleanupBootstrap with bootstrap_integration_test.go.
package users_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/auth/users"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const singleTenantUpgradeUp = "../../../migrations/sql/20260809000000_single_tenant_bootstrap_super_admin.sql"

func founderInput() users.BootstrapInput {
	return users.BootstrapInput{
		Email:       "founder@example.com",
		DisplayName: "Founder",
		Issuer:      "https://idp.example.com/",
		Subject:     "oidc-subject-founder",
	}
}

func insertTenant(t *testing.T, id uuid.UUID, name, slug string, bootstrap bool) {
	t.Helper()
	var slugArg any
	if slug != "" {
		slugArg = slug
	}
	if _, err := bootstrapAdminPool.Exec(context.Background(),
		`INSERT INTO tenants (id, name, slug, is_bootstrap_tenant) VALUES ($1, $2, $3, $4)`,
		id, name, slugArg, bootstrap,
	); err != nil {
		t.Fatalf("insert tenant %s: %v", name, err)
	}
}

func countRows(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := bootstrapAdminPool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// A fresh install whose bootstrap tenant was seeded (bootstrap.sh) and
// whose first sign-in is OIDC ends with exactly one tenant, the bootstrap
// tenant, and the user in it as admin + super-admin.
func TestBootstrap_SeededBootstrapTenant_FirstOIDCLoginAttaches(t *testing.T) {
	cleanupBootstrap(t)
	t.Cleanup(func() { cleanupBootstrap(t) })

	seeded := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	insertTenant(t, seeded, "Seeded Org", "", true)

	store := users.NewStoreWithAuthPool(bootstrapAppPool, bootstrapAdminPool)
	res, err := store.BootstrapFirstInstallOrUpsert(context.Background(), founderInput())
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if !res.Bootstrapped {
		t.Fatal("expected Bootstrapped=true for the first user")
	}
	if res.TenantID != seeded {
		t.Fatalf("TenantID = %s, want seeded bootstrap tenant %s", res.TenantID, seeded)
	}

	if n := countRows(t, `SELECT COUNT(*) FROM tenants`); n != 1 {
		t.Errorf("tenants = %d, want exactly 1", n)
	}
	if n := countRows(t, `SELECT COUNT(*) FROM tenants WHERE id = $1 AND is_bootstrap_tenant`, seeded); n != 1 {
		t.Errorf("seeded tenant not the bootstrap tenant")
	}
	if n := countRows(t, `SELECT COUNT(*) FROM users WHERE id = $1 AND tenant_id = $2`, res.UserID, seeded); n != 1 {
		t.Errorf("user not in the bootstrap tenant")
	}
	if n := countRows(t, `SELECT COUNT(*) FROM user_roles WHERE tenant_id = $1 AND user_id = $2 AND role = 'admin'`, seeded, res.UserID.String()); n != 1 {
		t.Errorf("user lacks admin in the bootstrap tenant")
	}
	if n := countRows(t, `SELECT COUNT(*) FROM super_admins WHERE user_id = $1`, res.UserID); n != 1 {
		t.Errorf("bootstrap admin is not super-admin")
	}
}

// A single tenant that nobody flagged becomes the bootstrap tenant when the
// first user attaches to it.
func TestBootstrap_SingleUnflaggedTenant_IsFlagged(t *testing.T) {
	cleanupBootstrap(t)
	t.Cleanup(func() { cleanupBootstrap(t) })

	only := uuid.New()
	insertTenant(t, only, "Only Org", "only-org", false)

	store := users.NewStoreWithAuthPool(bootstrapAppPool, bootstrapAdminPool)
	res, err := store.BootstrapFirstInstallOrUpsert(context.Background(), founderInput())
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if !res.Bootstrapped || res.TenantID != only {
		t.Fatalf("res = %+v, want Bootstrapped into %s", res, only)
	}
	if n := countRows(t, `SELECT COUNT(*) FROM tenants WHERE id = $1 AND is_bootstrap_tenant`, only); n != 1 {
		t.Errorf("single tenant was not flagged as bootstrap")
	}
	if n := countRows(t, `SELECT COUNT(*) FROM tenants`); n != 1 {
		t.Errorf("tenants = %d, want 1", n)
	}
}

// With two tenants and no user no tenant is implied: the first-login path
// fails with the resolver's error listing both tenants, and writes nothing.
func TestBootstrap_TwoTenants_ResolverErrorListsTenants(t *testing.T) {
	cleanupBootstrap(t)
	t.Cleanup(func() { cleanupBootstrap(t) })

	a, b := uuid.New(), uuid.New()
	insertTenant(t, a, "Alpha Org", "alpha", true)
	insertTenant(t, b, "Beta Org", "beta", false)

	store := users.NewStoreWithAuthPool(bootstrapAppPool, bootstrapAdminPool)
	res, err := store.BootstrapFirstInstallOrUpsert(context.Background(), founderInput())
	var amb *tenancy.AmbiguousTenantError
	if !errors.As(err, &amb) {
		t.Fatalf("err = %v, want *tenancy.AmbiguousTenantError", err)
	}
	if res.Bootstrapped {
		t.Error("Bootstrapped = true on ambiguous tenants")
	}
	for _, want := range []string{a.String(), "alpha", "Alpha Org", b.String(), "beta", "Beta Org"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not list %q", err.Error(), want)
		}
	}
	if n := countRows(t, `SELECT COUNT(*) FROM users`); n != 0 {
		t.Errorf("users = %d, want 0", n)
	}
	if n := countRows(t, `SELECT COUNT(*) FROM super_admins`); n != 0 {
		t.Errorf("super_admins = %d, want 0", n)
	}
}

// ===== upgrade migration: bootstrap admin becomes super-admin =====

func applySingleTenantUpgrade(t *testing.T) {
	t.Helper()
	up, err := os.ReadFile(singleTenantUpgradeUp)
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if _, err := bootstrapAdminPool.Exec(context.Background(), string(up)); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
}

// insertAdmin creates an active admin user in tenant with no super_admin row.
func insertAdmin(t *testing.T, tenant uuid.UUID, email string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.New()
	if _, err := bootstrapAdminPool.Exec(ctx,
		`INSERT INTO users (id, tenant_id, email, display_name, status) VALUES ($1, $2, $3, $3, 'active')`,
		id, tenant, email,
	); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := bootstrapAdminPool.Exec(ctx,
		`INSERT INTO user_roles (tenant_id, user_id, role, granted_by) VALUES ($1, $2, 'admin', 'test')`,
		tenant, id.String(),
	); err != nil {
		t.Fatalf("insert user_roles: %v", err)
	}
	return id
}

func TestSingleTenantUpgrade_GrantsBootstrapAdminSuperAdmin(t *testing.T) {
	cleanupBootstrap(t)
	t.Cleanup(func() { cleanupBootstrap(t) })

	tenant := uuid.New()
	insertTenant(t, tenant, "Upgraded Org", "", true)
	bootstrapAdmin := insertAdmin(t, tenant, "first@example.com")
	laterAdmin := insertAdmin(t, tenant, "second@example.com")

	applySingleTenantUpgrade(t)

	if n := countRows(t,
		`SELECT COUNT(*) FROM super_admins WHERE user_id = $1 AND granted_via = 'single_tenant_upgrade'`,
		bootstrapAdmin); n != 1 {
		t.Errorf("bootstrap admin not granted super-admin by the upgrade")
	}
	if n := countRows(t, `SELECT COUNT(*) FROM super_admins WHERE user_id = $1`, laterAdmin); n != 0 {
		t.Errorf("a later admin was granted super-admin; only the bootstrap admin should be")
	}

	// Re-applying is a no-op.
	applySingleTenantUpgrade(t)
	if n := countRows(t, `SELECT COUNT(*) FROM super_admins`); n != 1 {
		t.Errorf("super_admins = %d after re-apply, want 1", n)
	}
}

func TestSingleTenantUpgrade_KeepsExistingGrant(t *testing.T) {
	cleanupBootstrap(t)
	t.Cleanup(func() { cleanupBootstrap(t) })

	tenant := uuid.New()
	insertTenant(t, tenant, "Granted Org", "", true)
	admin := insertAdmin(t, tenant, "first@example.com")
	if _, err := bootstrapAdminPool.Exec(context.Background(),
		`INSERT INTO super_admins (user_id, granted_via) VALUES ($1, 'bootstrap_first_install')`, admin,
	); err != nil {
		t.Fatalf("seed grant: %v", err)
	}

	applySingleTenantUpgrade(t)

	if n := countRows(t,
		`SELECT COUNT(*) FROM super_admins WHERE user_id = $1 AND granted_via = 'bootstrap_first_install'`,
		admin); n != 1 {
		t.Errorf("existing grant was changed")
	}
}

func TestSingleTenantUpgrade_MultiTenantDoesNothing(t *testing.T) {
	cleanupBootstrap(t)
	t.Cleanup(func() { cleanupBootstrap(t) })

	a, b := uuid.New(), uuid.New()
	insertTenant(t, a, "Alpha Org", "alpha", true)
	insertTenant(t, b, "Beta Org", "beta", false)
	insertAdmin(t, a, "alpha-admin@example.com")
	insertAdmin(t, b, "beta-admin@example.com")

	applySingleTenantUpgrade(t)

	if n := countRows(t, `SELECT COUNT(*) FROM super_admins`); n != 0 {
		t.Errorf("super_admins = %d on a multi-tenant install, want 0", n)
	}
}
