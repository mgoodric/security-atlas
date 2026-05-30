//go:build integration

// user_resolver_integration_test.go — slice 314 integration coverage
// for the slice-192 DBUserResolver (the OAuth authorize-flow identity
// snapshot source).
//
// The authorize integration suite uses a STUB UserResolver, so the
// real DB-backed resolver (readSessionIdentity + queryUserRoles +
// ResolveForOAuth) had no coverage at all. This suite seeds `users` +
// `user_roles` under tenant RLS via the atlas_app pool and drives the
// resolver directly.
//
// Scope note: enumerateMemberships + lookupSuperAdmin require the
// BYPASSRLS authPool (cross-tenant `users` enumeration + the
// no-RLS `super_admins` table). This suite wires authPool=nil — the
// single-tenant resolution path — which exercises ResolveForOAuth's
// RLS-bound steps (1 + 3) without the cross-tenant plumbing. The
// authPool branches stay for a follow-on if the cross-tenant
// membership shape needs dedicated coverage.

package oauth_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/oauth"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// seedUserWithRoles inserts a `users` row + its `user_roles` under the
// tenant's RLS context via the atlas_app pool. Returns nothing; the
// caller already holds userID + tenantID.
func seedUserWithRoles(t *testing.T, userID, tenantID uuid.UUID, idpIssuer, idpSubject string, roles []string) {
	t.Helper()
	ctx := context.Background()
	pool := openTokenIntegrationPool(t)

	tenantCtx, err := tenancy.WithTenant(ctx, tenantID.String())
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	tx, err := pool.Begin(tenantCtx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(tenantCtx) }()
	if err := tenancy.ApplyTenant(tenantCtx, tx); err != nil {
		t.Fatalf("ApplyTenant: %v", err)
	}

	if _, err := tx.Exec(tenantCtx,
		`INSERT INTO users (id, tenant_id, email, status, idp_issuer, idp_subject)
		 VALUES ($1, $2, $3, 'active', $4, $5)`,
		userID, tenantID, userID.String()[:8]+"@example.test", idpIssuer, idpSubject,
	); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	for _, role := range roles {
		if _, err := tx.Exec(tenantCtx,
			`INSERT INTO user_roles (tenant_id, user_id, role) VALUES ($1, $2, $3)`,
			tenantID, userID.String(), role,
		); err != nil {
			t.Fatalf("insert role %q: %v", role, err)
		}
	}
	if err := tx.Commit(tenantCtx); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

// TestIntegrationDBUserResolver_SingleTenantWithRoles covers the
// slice-192 ResolveForOAuth happy path (authPool=nil → single-tenant):
// the resolver reads the session-tenant identity, enumerates the
// single membership, and resolves the user's roles under RLS. The
// returned snapshot is what the authorize handler captures into
// oauth_auth_codes.
func TestIntegrationDBUserResolver_SingleTenantWithRoles(t *testing.T) {
	pool := openTokenIntegrationPool(t)

	userID := uuid.New()
	tenantID := uuid.New()
	seedUserWithRoles(t, userID, tenantID, "https://idp.example.test", "subject-"+userID.String()[:8],
		[]string{"grc_engineer", "control_owner"})

	r := oauth.NewDBUserResolver(pool)
	id, err := r.ResolveForOAuth(context.Background(), userID, tenantID)
	if err != nil {
		t.Fatalf("ResolveForOAuth: %v", err)
	}
	if id.UserID != userID {
		t.Errorf("user_id = %v, want %v", id.UserID, userID)
	}
	if id.CurrentTenantID != tenantID {
		t.Errorf("current_tenant = %v, want %v", id.CurrentTenantID, tenantID)
	}
	if len(id.AvailableTenants) != 1 || id.AvailableTenants[0] != tenantID {
		t.Errorf("available_tenants = %v, want [%v]", id.AvailableTenants, tenantID)
	}
	got := id.Roles[tenantID]
	if len(got) != 2 {
		t.Fatalf("roles[%v] = %v, want 2 roles", tenantID, got)
	}
	roleSet := map[string]bool{}
	for _, role := range got {
		roleSet[role] = true
	}
	if !roleSet["grc_engineer"] || !roleSet["control_owner"] {
		t.Errorf("roles = %v, want grc_engineer + control_owner", got)
	}
	// authPool=nil → super_admin stays false (the no-RLS table is not
	// consulted in the single-tenant path).
	if id.SuperAdmin {
		t.Error("super_admin true with authPool=nil; want false")
	}
}

// TestIntegrationDBUserResolver_NoRolesYieldsEmptyMap covers the
// branch where the user has no user_roles rows: the resolver returns a
// snapshot with an empty role map for the tenant (NOT a nil map, NOT
// an error). A user with no roles is a valid identity (e.g. just
// invited, not yet granted).
func TestIntegrationDBUserResolver_NoRolesYieldsEmptyMap(t *testing.T) {
	pool := openTokenIntegrationPool(t)

	userID := uuid.New()
	tenantID := uuid.New()
	seedUserWithRoles(t, userID, tenantID, "https://idp.example.test", "noroles-"+userID.String()[:8], nil)

	r := oauth.NewDBUserResolver(pool)
	id, err := r.ResolveForOAuth(context.Background(), userID, tenantID)
	if err != nil {
		t.Fatalf("ResolveForOAuth: %v", err)
	}
	if got := id.Roles[tenantID]; len(got) != 0 {
		t.Errorf("roles[%v] = %v, want empty", tenantID, got)
	}
	if id.Roles == nil {
		t.Error("Roles map is nil; want non-nil empty map")
	}
}

// TestIntegrationDBUserResolver_UnknownUserNoIdentity covers the
// readSessionIdentity ErrNoRows branch: a userID with no matching
// `users` row resolves to the default single-tenant identity with
// empty idp metadata (the resolver treats a missing user as
// "single-tenant by definition" rather than erroring — the session
// already authenticated the principal upstream).
func TestIntegrationDBUserResolver_UnknownUserNoIdentity(t *testing.T) {
	pool := openTokenIntegrationPool(t)

	r := oauth.NewDBUserResolver(pool)
	userID := uuid.New() // never seeded
	tenantID := uuid.New()
	id, err := r.ResolveForOAuth(context.Background(), userID, tenantID)
	if err != nil {
		t.Fatalf("ResolveForOAuth: %v", err)
	}
	if len(id.AvailableTenants) != 1 || id.AvailableTenants[0] != tenantID {
		t.Errorf("available_tenants = %v, want [%v] (single-tenant fallback)", id.AvailableTenants, tenantID)
	}
	if len(id.Roles[tenantID]) != 0 {
		t.Errorf("roles = %v, want empty for unknown user", id.Roles[tenantID])
	}
}
