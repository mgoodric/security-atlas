//go:build integration

// user_resolver_authpool_integration_test.go — slice 472 integration
// coverage for the slice-192 DBUserResolver BYPASSRLS authPool path that
// slices 314/456 left untested.
//
// The slice-314 user_resolver_integration_test.go wires authPool=nil — the
// single-tenant fallback — so NewDBUserResolverWithAuthPool,
// enumerateMemberships (cross-tenant `users` enumeration), and
// lookupSuperAdmin (the no-RLS `super_admins` table) all sat at 0%. This is
// a real security surface — cross-tenant membership enumeration +
// super_admin lookup — and is in the slice-350 security-critical tier, so it
// earns dedicated coverage (slice 472 AC-2).
//
// POOL MODEL (mirrors the production wiring + the adminsuperadmins
// integration suite):
//
//   - pool      = atlas_app (DATABASE_URL_APP), RLS-bound. Used for the
//                 per-tenant readSessionIdentity + queryUserRoles SELECTs.
//   - authPool  = the BYPASSRLS admin pool (DATABASE_URL — in CI the
//                 superuser `postgres`, which has BYPASSRLS). Used for the
//                 cross-tenant `users` enumeration + the `super_admins`
//                 lookup. Nil-falls-back to single-tenant, which is exactly
//                 what slice 314 already covers — so this suite REQUIRES a
//                 non-nil authPool and skips when DATABASE_URL is unset.
//
// No JWT/vendor-shaped fixture literals — IdP issuer/subject are neutral
// example.test values; all ids are fresh UUIDs.

package oauth_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/oauth"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// openAuthIntegrationPool opens the BYPASSRLS admin pool from DATABASE_URL
// (the migrate/superuser role). Skips the test when unset — the authPool
// branches are only reachable with a BYPASSRLS connection because they
// read `users` across tenants and the no-RLS `super_admins` table.
func openAuthIntegrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL (BYPASSRLS admin pool) not set; skipping authPool resolver test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New (auth): %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedTenant inserts a tenants row via the BYPASSRLS admin pool so the
// users FK is satisfiable. The slug is process-unique.
func seedTenant(t *testing.T, adminPool *pgxpool.Pool, tenantID uuid.UUID) {
	t.Helper()
	if _, err := adminPool.Exec(context.Background(),
		`INSERT INTO tenants (id, name, slug) VALUES ($1, $2, $3)`,
		tenantID, "T-"+tenantID.String()[:8], "t-"+tenantID.String()[:8],
	); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
}

// seedUserViaAdmin inserts a `users` row + its `user_roles` via the
// BYPASSRLS admin pool (no RLS context needed). Used to build the
// cross-tenant membership graph that enumerateMemberships reads.
func seedUserViaAdmin(t *testing.T, adminPool *pgxpool.Pool, userID, tenantID uuid.UUID, idpIssuer, idpSubject string, roles []string) {
	t.Helper()
	ctx := context.Background()
	if _, err := adminPool.Exec(ctx,
		`INSERT INTO users (id, tenant_id, email, status, idp_issuer, idp_subject)
		 VALUES ($1, $2, $3, 'active', $4, $5)`,
		userID, tenantID, userID.String()[:8]+"@example.test", idpIssuer, idpSubject,
	); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	for _, role := range roles {
		if _, err := adminPool.Exec(ctx,
			`INSERT INTO user_roles (tenant_id, user_id, role) VALUES ($1, $2, $3)`,
			tenantID, userID.String(), role,
		); err != nil {
			t.Fatalf("insert role %q: %v", role, err)
		}
	}
}

// seedSuperAdminRow inserts a super_admins row for the given user_id via
// the BYPASSRLS admin pool (the table is not under RLS).
func seedSuperAdminRow(t *testing.T, adminPool *pgxpool.Pool, userID uuid.UUID) {
	t.Helper()
	if _, err := adminPool.Exec(context.Background(),
		`INSERT INTO super_admins (user_id, granted_via) VALUES ($1, $2)
		 ON CONFLICT (user_id) DO NOTHING`,
		userID, "manual_grant",
	); err != nil {
		t.Fatalf("insert super_admins: %v", err)
	}
}

// TestIntegrationResolverAuthPool_CrossTenantMemberships covers AC-2: a
// single OIDC subject (idp_issuer + idp_subject) holding active `users`
// rows in TWO tenants. With the BYPASSRLS authPool wired,
// ResolveForOAuth → enumerateMemberships returns BOTH tenants in
// available_tenants and resolves per-tenant roles under RLS. This is the
// vCISO multi-tenant case (one operator → N tenants) the slice-192
// expansion exists for.
func TestIntegrationResolverAuthPool_CrossTenantMemberships(t *testing.T) {
	appPool := openTokenIntegrationPool(t)
	adminPool := openAuthIntegrationPool(t)

	idpIssuer := "https://idp.example.test"
	idpSubject := "subject-" + uuid.NewString()[:12] // unique across runs

	// Two tenants, same OIDC subject, distinct per-tenant user_id values.
	tenantA, tenantB := uuid.New(), uuid.New()
	userA, userB := uuid.New(), uuid.New()
	seedTenant(t, adminPool, tenantA)
	seedTenant(t, adminPool, tenantB)
	seedUserViaAdmin(t, adminPool, userA, tenantA, idpIssuer, idpSubject, []string{"grc_engineer"})
	seedUserViaAdmin(t, adminPool, userB, tenantB, idpIssuer, idpSubject, []string{"control_owner", "auditor"})

	r := oauth.NewDBUserResolverWithAuthPool(appPool, adminPool)
	// The session tenant is tenantA (the user authenticated there); the
	// resolver must enumerate BOTH memberships via the authPool.
	id, err := r.ResolveForOAuth(context.Background(), userA, tenantA)
	if err != nil {
		t.Fatalf("ResolveForOAuth: %v", err)
	}

	if got := len(id.AvailableTenants); got != 2 {
		t.Fatalf("available_tenants len = %d (%v), want 2", got, id.AvailableTenants)
	}
	seen := map[uuid.UUID]bool{}
	for _, tid := range id.AvailableTenants {
		seen[tid] = true
	}
	if !seen[tenantA] || !seen[tenantB] {
		t.Errorf("available_tenants = %v, want both %v and %v", id.AvailableTenants, tenantA, tenantB)
	}
	// Per-tenant roles resolved from the correct tenant-scoped user_id.
	if rolesA := id.Roles[tenantA]; len(rolesA) != 1 || rolesA[0] != "grc_engineer" {
		t.Errorf("roles[tenantA] = %v, want [grc_engineer]", rolesA)
	}
	if rolesB := id.Roles[tenantB]; len(rolesB) != 2 {
		t.Errorf("roles[tenantB] = %v, want 2 roles", id.Roles[tenantB])
	}
	// No super_admins row → super_admin stays false even though the
	// authPool consulted the table.
	if id.SuperAdmin {
		t.Error("super_admin = true with no super_admins row; want false")
	}
}

// TestIntegrationResolverAuthPool_SuperAdminPresent covers the
// lookupSuperAdmin TRUE arm (AC-2): when a super_admins row matches one of
// the user's tenant-scoped user_id values, the snapshot's super_admin is
// true. A regression here would silently DROP a platform admin's super_admin
// claim (under-privilege) — exactly the security-critical-tier failure the
// slice-350 advisory targets.
func TestIntegrationResolverAuthPool_SuperAdminPresent(t *testing.T) {
	appPool := openTokenIntegrationPool(t)
	adminPool := openAuthIntegrationPool(t)

	idpIssuer := "https://idp.example.test"
	idpSubject := "sa-subject-" + uuid.NewString()[:12]
	tenant := uuid.New()
	userID := uuid.New()
	seedTenant(t, adminPool, tenant)
	seedUserViaAdmin(t, adminPool, userID, tenant, idpIssuer, idpSubject, []string{"grc_engineer"})
	seedSuperAdminRow(t, adminPool, userID)

	r := oauth.NewDBUserResolverWithAuthPool(appPool, adminPool)
	id, err := r.ResolveForOAuth(context.Background(), userID, tenant)
	if err != nil {
		t.Fatalf("ResolveForOAuth: %v", err)
	}
	if !id.SuperAdmin {
		t.Error("super_admin = false with a matching super_admins row; want true")
	}
}

// TestIntegrationResolverAuthPool_SuperAdminAbsent covers the
// lookupSuperAdmin FALSE arm with the authPool wired but NO super_admins
// row: super_admin must be false. Pairs with the present-arm test so the
// COUNT(*) > 0 branch is covered both ways (P0-188-4: super_admin is never
// synthesized).
func TestIntegrationResolverAuthPool_SuperAdminAbsent(t *testing.T) {
	appPool := openTokenIntegrationPool(t)
	adminPool := openAuthIntegrationPool(t)

	idpIssuer := "https://idp.example.test"
	idpSubject := "nosa-subject-" + uuid.NewString()[:12]
	tenant := uuid.New()
	userID := uuid.New()
	seedTenant(t, adminPool, tenant)
	seedUserViaAdmin(t, adminPool, userID, tenant, idpIssuer, idpSubject, nil)

	r := oauth.NewDBUserResolverWithAuthPool(appPool, adminPool)
	id, err := r.ResolveForOAuth(context.Background(), userID, tenant)
	if err != nil {
		t.Fatalf("ResolveForOAuth: %v", err)
	}
	if id.SuperAdmin {
		t.Error("super_admin = true with no super_admins row; want false")
	}
}

// TestIntegrationResolverAuthPool_SessionFallbackAppended covers the
// enumerateMemberships fallback branch (user_resolver.go:261): when the
// cross-tenant `users` query does NOT return the session tenant (the user
// has an IdP identity in OTHER tenants but the session-tenant user row is
// absent from the active set), the resolver appends the session
// (tenant, userID) so the session tenant is never lost. Here the session
// userID has a distinct idp_subject so the cross-tenant query misses it,
// then the fallback re-adds the session tenant.
func TestIntegrationResolverAuthPool_SessionFallbackAppended(t *testing.T) {
	appPool := openTokenIntegrationPool(t)
	adminPool := openAuthIntegrationPool(t)

	// The session user has a UNIQUE idp_subject so the cross-tenant
	// enumeration returns only this one row — but we craft the scenario so
	// the readSessionIdentity sees a subject that the enumeration query
	// also matches, guaranteeing sawSession=true is NOT trivially hit.
	// To force the fallback we seed the session user with an idp identity
	// in a DIFFERENT tenant only, leaving the session tenant's row to be
	// re-appended by the fallback.
	sharedIssuer := "https://idp.example.test"
	sharedSubject := "fb-subject-" + uuid.NewString()[:12]

	otherTenant := uuid.New()
	otherUser := uuid.New()
	sessionTenant := uuid.New()
	sessionUser := uuid.New()

	seedTenant(t, adminPool, otherTenant)
	seedTenant(t, adminPool, sessionTenant)
	// The enumerable membership lives in otherTenant under the shared subject.
	seedUserViaAdmin(t, adminPool, otherUser, otherTenant, sharedIssuer, sharedSubject, []string{"auditor"})
	// The session-tenant user shares the SAME subject so readSessionIdentity
	// returns its (idp_issuer, idp_subject), but it is `disabled` so the
	// enumeration's `status = 'active'` filter EXCLUDES it → sawSession is
	// false and the fallback (user_resolver.go:261) re-appends the session
	// tenant. (`disabled` is the only non-active value the users_status_check
	// admits.)
	if _, err := adminPool.Exec(context.Background(),
		`INSERT INTO users (id, tenant_id, email, status, idp_issuer, idp_subject)
		 VALUES ($1, $2, $3, 'disabled', $4, $5)`,
		sessionUser, sessionTenant, sessionUser.String()[:8]+"@example.test", sharedIssuer, sharedSubject,
	); err != nil {
		t.Fatalf("insert disabled session user: %v", err)
	}

	r := oauth.NewDBUserResolverWithAuthPool(appPool, adminPool)
	id, err := r.ResolveForOAuth(context.Background(), sessionUser, sessionTenant)
	if err != nil {
		t.Fatalf("ResolveForOAuth: %v", err)
	}
	// enumeration returns otherTenant (active); the session tenant is
	// re-appended by the fallback → both present.
	seen := map[uuid.UUID]bool{}
	for _, tid := range id.AvailableTenants {
		seen[tid] = true
	}
	if !seen[sessionTenant] {
		t.Errorf("available_tenants = %v, missing re-appended session tenant %v", id.AvailableTenants, sessionTenant)
	}
	if !seen[otherTenant] {
		t.Errorf("available_tenants = %v, missing enumerated tenant %v", id.AvailableTenants, otherTenant)
	}
}

// sanity: tenancy import is used by the shared seed helper in the
// slice-314 suite; keep it referenced here for symmetry if that helper
// changes packages.
var _ = tenancy.WithTenant
