//go:build integration

// Integration tests for the slice 478 user↔tenant↔role assignment surface.
// Requires Postgres reachable via DATABASE_URL_APP (RLS-bound atlas_app) +
// DATABASE_URL (BYPASSRLS admin/migrate). The cross-tenant super-admin paths
// use the admin pool as the handler's authPool; within-tenant paths use the
// app pool under RLS.
//
// Run with:
//
//	go test -tags=integration -p 1 ./internal/api/adminusers/...
//
// Coverage maps to slice 478 acceptance criteria:
//
//	AC-1  GET /v1/admin/users cross-tenant (super-admin) vs within-tenant
//	AC-2  POST .../assign creates membership + roles atomically, idempotent
//	AC-3  POST .../revoke removes roles
//	AC-4  self-assign → tenant in resolver's available_tenants (end-to-end)
//	AC-5  local-auth assignment WITHOUT empty-tuple over-match (P0-478-2)
//	AC-6  authz: cross-tenant requires super_admin (DENIED case)
//	AC-7  audit row per assign/revoke
//	AC-8  RLS: tenant-admin cannot assign/list outside their tenant
//
// All fixtures use neutral test-* strings (P0-478-6).
package adminusers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/adminusers"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/oauth"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
)

// appPool + adminPool + TestMain are defined in handler_integration_test.go
// (same _test package). adminPool is the BYPASSRLS pool; the slice-478
// cross-tenant tests skip when it is nil.

// skipIfNoAdminPool skips a slice-478 cross-tenant test when DATABASE_URL
// (the BYPASSRLS pool) is not configured.
func skipIfNoAdminPool(t *testing.T) {
	t.Helper()
	if adminPool == nil {
		t.Skip("DATABASE_URL (BYPASSRLS admin pool) not set; skipping slice-478 cross-tenant test")
	}
}

// ----- harness -----

func seedTenant478(t *testing.T, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := adminPool.Exec(context.Background(),
		`INSERT INTO tenants (id, name) VALUES ($1, $2)`, id, name); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = adminPool.Exec(ctx, `DELETE FROM user_roles WHERE tenant_id = $1`, id)
		_, _ = adminPool.Exec(ctx, `DELETE FROM me_audit_log WHERE tenant_id = $1`, id)
		_, _ = adminPool.Exec(ctx, `DELETE FROM users WHERE tenant_id = $1`, id)
		_, _ = adminPool.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, id)
	})
	return id
}

// seedUser478 inserts a users row directly via the admin pool (BYPASSRLS).
// idp tuple empty => local user; non-empty => IdP-backed. Returns the user id.
func seedUser478(t *testing.T, tenantID uuid.UUID, email, displayName, idpIssuer, idpSubject string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := adminPool.Exec(context.Background(),
		`INSERT INTO users (id, tenant_id, email, display_name, status, idp_issuer, idp_subject)
		 VALUES ($1, $2, $3, $4, 'active', $5, $6)`,
		id, tenantID, email, displayName, idpIssuer, idpSubject); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

func seedSuperAdmin(t *testing.T, userID uuid.UUID) {
	t.Helper()
	if _, err := adminPool.Exec(context.Background(),
		`INSERT INTO super_admins (user_id, granted_via) VALUES ($1, 'bootstrap_first_install')
		 ON CONFLICT (user_id) DO NOTHING`, userID); err != nil {
		t.Fatalf("seed super_admin: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = adminPool.Exec(ctx, `DELETE FROM super_admin_audit_log WHERE target_user_id = $1 OR actor_user_id = $1`, userID)
		_, _ = adminPool.Exec(ctx, `DELETE FROM super_admins WHERE user_id = $1`, userID)
	})
}

// newRouter wires the slice-478 handler with the BYPASSRLS authPool + a JWT
// claims injector. superAdmin controls atlas:super_admin. sessionTenant is
// the GUC the within-tenant paths run under.
func newRouter478(t *testing.T, sessionTenant, actorID uuid.UUID, superAdmin bool) http.Handler {
	t.Helper()
	h := adminusers.New(appPool).SetAuthPool(adminPool)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			claims := &jwt.AtlasClaims{
				RegisteredClaims: jwt.RegisteredClaims{Subject: actorID.String()},
				CurrentTenantID:  sessionTenant,
				AvailableTenants: []uuid.UUID{sessionTenant},
				SuperAdmin:       superAdmin,
			}
			ctx := jwtmw.WithClaimsForTest(req.Context(), claims)
			cred := credstore.Credential{
				ID:       "jwt:test",
				TenantID: sessionTenant.String(),
				UserID:   actorID.String(),
				IsAdmin:  superAdmin, // jwtmw bridges SuperAdmin->IsAdmin
			}
			ctx = authctx.WithCredential(ctx, cred)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Get("/v1/admin/users", h.ListDispatch)
	r.Post("/v1/admin/users/assign", h.Assign)
	r.Post("/v1/admin/users/revoke", h.Revoke)
	return r
}

// newTenantAdminRouter wires a NON-super-admin tenant-admin (IsAdmin via the
// credential, SuperAdmin=false). Used for the AC-6 / AC-8 deny cases.
func newTenantAdminRouter478(t *testing.T, sessionTenant, actorID uuid.UUID) http.Handler {
	t.Helper()
	h := adminusers.New(appPool).SetAuthPool(adminPool)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			// No JWT super_admin claim; only a tenant-admin credential.
			claims := &jwt.AtlasClaims{
				RegisteredClaims: jwt.RegisteredClaims{Subject: actorID.String()},
				CurrentTenantID:  sessionTenant,
				AvailableTenants: []uuid.UUID{sessionTenant},
				SuperAdmin:       false,
			}
			ctx := jwtmw.WithClaimsForTest(req.Context(), claims)
			cred := credstore.Credential{
				ID:       "jwt:test",
				TenantID: sessionTenant.String(),
				UserID:   actorID.String(),
				IsAdmin:  true, // tenant-admin within-tenant authority
			}
			ctx = authctx.WithCredential(ctx, cred)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Get("/v1/admin/users", h.ListDispatch)
	r.Post("/v1/admin/users/assign", h.Assign)
	r.Post("/v1/admin/users/revoke", h.Revoke)
	return r
}

func doJSON(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		buf, _ := json.Marshal(body)
		reader = bytes.NewReader(buf)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func countUserRoles(t *testing.T, tenantID, userID uuid.UUID) int {
	t.Helper()
	var n int
	if err := adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM user_roles WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, userID.String()).Scan(&n); err != nil {
		t.Fatalf("count user_roles: %v", err)
	}
	return n
}

// ----- tests -----

// AC-2 + AC-7: super-admin cross-tenant assign creates the membership + roles
// atomically and is idempotent on re-assign; audit rows are written.
func TestAssign_CrossTenant_CreatesMembershipAndRoles(t *testing.T) {
	skipIfNoAdminPool(t)
	sessionTenant := seedTenant478(t, "test-session")
	destTenant := seedTenant478(t, "test-dest")
	actorID := seedUser478(t, sessionTenant, "test-actor@example.com", "Test Actor",
		"https://idp.test/", "test-actor-sub")
	seedSuperAdmin(t, actorID)
	// Target is an IdP-backed user in the session tenant.
	targetID := seedUser478(t, sessionTenant, "test-target@example.com", "Test Target",
		"https://idp.test/", "test-target-sub")

	h := newRouter478(t, sessionTenant, actorID, true)

	rr := doJSON(t, h, http.MethodPost, "/v1/admin/users/assign", map[string]any{
		"user_id":   targetID.String(),
		"tenant_id": destTenant.String(),
		"roles":     []string{"viewer", "auditor"},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("assign status = %d; body = %s", rr.Code, rr.Body.String())
	}
	var resp adminusers.AssignResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.MembershipCreated {
		t.Errorf("expected membership_created=true")
	}
	destUserID := uuid.MustParse(resp.UserID)
	if got := countUserRoles(t, destTenant, destUserID); got != 2 {
		t.Errorf("dest tenant role count = %d; want 2", got)
	}

	// AC-7: a me_audit_log row anchored to the actor's session tenant.
	var meCount int
	if err := adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM me_audit_log WHERE tenant_id = $1 AND action = 'user_tenant_assign'`,
		sessionTenant).Scan(&meCount); err != nil {
		t.Fatalf("count me_audit_log: %v", err)
	}
	if meCount != 1 {
		t.Errorf("me_audit_log user_tenant_assign rows = %d; want 1", meCount)
	}
	// AC-7: a platform-global super_admin_audit_log row.
	var saCount int
	if err := adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM super_admin_audit_log WHERE actor_user_id = $1 AND action = 'user_tenant_assign'`,
		actorID).Scan(&saCount); err != nil {
		t.Fatalf("count super_admin_audit_log: %v", err)
	}
	if saCount != 1 {
		t.Errorf("super_admin_audit_log user_tenant_assign rows = %d; want 1", saCount)
	}

	// AC-2 idempotency: re-assign the same roles -> still 2, no error.
	rr2 := doJSON(t, h, http.MethodPost, "/v1/admin/users/assign", map[string]any{
		"user_id":   targetID.String(),
		"tenant_id": destTenant.String(),
		"roles":     []string{"viewer", "auditor"},
	})
	if rr2.Code != http.StatusOK {
		t.Fatalf("re-assign status = %d; body = %s", rr2.Code, rr2.Body.String())
	}
	if got := countUserRoles(t, destTenant, destUserID); got != 2 {
		t.Errorf("after re-assign role count = %d; want 2 (idempotent)", got)
	}
}

// AC-4: super-admin self-assign -> the tenant appears in the resolver's
// available_tenants (end-to-end via the slice-192 DBUserResolver).
func TestAssign_SelfAssign_ReachableInResolver(t *testing.T) {
	skipIfNoAdminPool(t)
	sessionTenant := seedTenant478(t, "test-self-session")
	demoTenant := seedTenant478(t, "test-self-demo")
	// The actor is an IdP-backed user with a home row in the session tenant.
	actorID := seedUser478(t, sessionTenant, "test-self@example.com", "Test Self",
		"https://idp.test/", "test-self-sub")
	seedSuperAdmin(t, actorID)

	h := newRouter478(t, sessionTenant, actorID, true)
	rr := doJSON(t, h, http.MethodPost, "/v1/admin/users/assign", map[string]any{
		"self_assign": true,
		"tenant_id":   demoTenant.String(),
		"roles":       []string{"admin"},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("self-assign status = %d; body = %s", rr.Code, rr.Body.String())
	}

	// Resolve the actor's available_tenants from their SESSION tenant home
	// row, using the real slice-192 resolver wired with the BYPASSRLS pool.
	resolver := oauth.NewDBUserResolverWithAuthPool(appPool, adminPool)
	id, err := resolver.ResolveForOAuth(context.Background(), actorID, sessionTenant)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !containsTenant(id.AvailableTenants, demoTenant) {
		t.Errorf("demo tenant %s not in available_tenants %v after self-assign", demoTenant, id.AvailableTenants)
	}
	if !containsTenant(id.AvailableTenants, sessionTenant) {
		t.Errorf("session tenant missing from available_tenants %v", id.AvailableTenants)
	}
}

// AC-5 + P0-478-2: two DISTINCT local users assigned to a shared second
// tenant must NOT over-match each other. Each local user gets a UNIQUE
// synthetic key; the resolver returns ONLY that user's memberships.
func TestAssign_LocalAuth_NoOverMatch(t *testing.T) {
	skipIfNoAdminPool(t)
	homeA := seedTenant478(t, "test-localA-home")
	homeB := seedTenant478(t, "test-localB-home")
	shared := seedTenant478(t, "test-local-shared")
	adminTenant := seedTenant478(t, "test-local-admin")
	actorID := seedUser478(t, adminTenant, "test-localadmin@example.com", "Local Admin",
		"https://idp.test/", "test-localadmin-sub")
	seedSuperAdmin(t, actorID)

	// Two LOCAL users (empty IdP tuple), each in their own home tenant.
	localA := seedUser478(t, homeA, "test-localA@example.com", "Local A", "", "")
	localB := seedUser478(t, homeB, "test-localB@example.com", "Local B", "", "")

	h := newRouter478(t, adminTenant, actorID, true)

	// Assign BOTH local users to the SAME shared tenant.
	for _, lu := range []uuid.UUID{localA, localB} {
		rr := doJSON(t, h, http.MethodPost, "/v1/admin/users/assign", map[string]any{
			"user_id":   lu.String(),
			"tenant_id": shared.String(),
			"roles":     []string{"viewer"},
		})
		if rr.Code != http.StatusOK {
			t.Fatalf("assign local user %s status = %d; body = %s", lu, rr.Code, rr.Body.String())
		}
	}

	// The origin rows must have been BACKFILLED with the synthetic key.
	assertSyntheticBackfill(t, localA)
	assertSyntheticBackfill(t, localB)

	resolver := oauth.NewDBUserResolverWithAuthPool(appPool, adminPool)

	// Local A resolves to EXACTLY {homeA, shared} — never homeB or localB.
	idA, err := resolver.ResolveForOAuth(context.Background(), localA, homeA)
	if err != nil {
		t.Fatalf("resolve A: %v", err)
	}
	if !containsTenant(idA.AvailableTenants, homeA) || !containsTenant(idA.AvailableTenants, shared) {
		t.Errorf("local A available_tenants = %v; want {homeA, shared}", idA.AvailableTenants)
	}
	if containsTenant(idA.AvailableTenants, homeB) {
		t.Errorf("OVER-MATCH: local A saw local B's home tenant %s (available=%v)", homeB, idA.AvailableTenants)
	}
	if len(idA.AvailableTenants) != 2 {
		t.Errorf("local A available_tenants len = %d; want 2 (no over-match) %v", len(idA.AvailableTenants), idA.AvailableTenants)
	}

	// Local B resolves to EXACTLY {homeB, shared}.
	idB, err := resolver.ResolveForOAuth(context.Background(), localB, homeB)
	if err != nil {
		t.Fatalf("resolve B: %v", err)
	}
	if containsTenant(idB.AvailableTenants, homeA) {
		t.Errorf("OVER-MATCH: local B saw local A's home tenant %s (available=%v)", homeA, idB.AvailableTenants)
	}
	if len(idB.AvailableTenants) != 2 {
		t.Errorf("local B available_tenants len = %d; want 2 %v", len(idB.AvailableTenants), idB.AvailableTenants)
	}
}

// AC-6 + P0-478-1: a NON-super-admin (tenant-admin) attempting a cross-tenant
// assign is DENIED (403). They cannot grant beyond their own authority.
func TestAssign_CrossTenant_DeniedForTenantAdmin(t *testing.T) {
	skipIfNoAdminPool(t)
	sessionTenant := seedTenant478(t, "test-ta-session")
	foreignTenant := seedTenant478(t, "test-ta-foreign")
	actorID := seedUser478(t, sessionTenant, "test-ta@example.com", "Tenant Admin",
		"https://idp.test/", "test-ta-sub")
	targetID := seedUser478(t, sessionTenant, "test-tatarget@example.com", "TA Target",
		"https://idp.test/", "test-tatarget-sub")

	h := newTenantAdminRouter478(t, sessionTenant, actorID)
	rr := doJSON(t, h, http.MethodPost, "/v1/admin/users/assign", map[string]any{
		"user_id":   targetID.String(),
		"tenant_id": foreignTenant.String(), // NOT the session tenant
		"roles":     []string{"admin"},
	})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("cross-tenant assign by tenant-admin status = %d; want 403; body = %s", rr.Code, rr.Body.String())
	}
	// And no roles were written in the foreign tenant.
	if got := countUserRoles(t, foreignTenant, targetID); got != 0 {
		t.Errorf("foreign tenant role count = %d; want 0 (denied write must not land)", got)
	}
}

// AC-8: a tenant-admin CAN assign WITHIN their own tenant (RLS-correct write
// lands under the right tenant_id).
func TestAssign_WithinTenant_TenantAdminAllowed(t *testing.T) {
	skipIfNoAdminPool(t)
	sessionTenant := seedTenant478(t, "test-within-session")
	actorID := seedUser478(t, sessionTenant, "test-within-admin@example.com", "Within Admin",
		"https://idp.test/", "test-within-admin-sub")
	targetID := seedUser478(t, sessionTenant, "test-within-target@example.com", "Within Target",
		"https://idp.test/", "test-within-target-sub")

	h := newTenantAdminRouter478(t, sessionTenant, actorID)
	rr := doJSON(t, h, http.MethodPost, "/v1/admin/users/assign", map[string]any{
		"user_id":   targetID.String(),
		"tenant_id": sessionTenant.String(), // == session tenant
		"roles":     []string{"control_owner"},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("within-tenant assign status = %d; want 200; body = %s", rr.Code, rr.Body.String())
	}
	if got := countUserRoles(t, sessionTenant, targetID); got != 1 {
		t.Errorf("within-tenant role count = %d; want 1", got)
	}
}

// AC-3: revoke removes the roles for the (user, tenant).
func TestRevoke_RemovesRoles(t *testing.T) {
	skipIfNoAdminPool(t)
	sessionTenant := seedTenant478(t, "test-revoke-session")
	destTenant := seedTenant478(t, "test-revoke-dest")
	actorID := seedUser478(t, sessionTenant, "test-revoke-actor@example.com", "Revoke Actor",
		"https://idp.test/", "test-revoke-actor-sub")
	seedSuperAdmin(t, actorID)
	targetID := seedUser478(t, sessionTenant, "test-revoke-target@example.com", "Revoke Target",
		"https://idp.test/", "test-revoke-target-sub")

	h := newRouter478(t, sessionTenant, actorID, true)
	// Assign first.
	rrA := doJSON(t, h, http.MethodPost, "/v1/admin/users/assign", map[string]any{
		"user_id":   targetID.String(),
		"tenant_id": destTenant.String(),
		"roles":     []string{"viewer", "auditor"},
	})
	if rrA.Code != http.StatusOK {
		t.Fatalf("assign status = %d; body = %s", rrA.Code, rrA.Body.String())
	}
	var resp adminusers.AssignResponse
	_ = json.Unmarshal(rrA.Body.Bytes(), &resp)
	destUserID := uuid.MustParse(resp.UserID)
	if countUserRoles(t, destTenant, destUserID) != 2 {
		t.Fatalf("precondition: expected 2 roles before revoke")
	}

	// Revoke.
	rrR := doJSON(t, h, http.MethodPost, "/v1/admin/users/revoke", map[string]any{
		"user_id":   destUserID.String(),
		"tenant_id": destTenant.String(),
	})
	if rrR.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d; want 204; body = %s", rrR.Code, rrR.Body.String())
	}
	if got := countUserRoles(t, destTenant, destUserID); got != 0 {
		t.Errorf("after revoke role count = %d; want 0", got)
	}
}

// AC-6: a NON-super-admin cross-tenant revoke is denied (403).
func TestRevoke_CrossTenant_DeniedForTenantAdmin(t *testing.T) {
	skipIfNoAdminPool(t)
	sessionTenant := seedTenant478(t, "test-revdeny-session")
	foreignTenant := seedTenant478(t, "test-revdeny-foreign")
	actorID := seedUser478(t, sessionTenant, "test-revdeny@example.com", "Rev Deny",
		"https://idp.test/", "test-revdeny-sub")
	targetID := seedUser478(t, foreignTenant, "test-revdeny-target@example.com", "Rev Deny Target",
		"https://idp.test/", "test-revdeny-target-sub")

	h := newTenantAdminRouter478(t, sessionTenant, actorID)
	rr := doJSON(t, h, http.MethodPost, "/v1/admin/users/revoke", map[string]any{
		"user_id":   targetID.String(),
		"tenant_id": foreignTenant.String(),
	})
	if rr.Code != http.StatusForbidden {
		t.Errorf("cross-tenant revoke by tenant-admin status = %d; want 403", rr.Code)
	}
}

// AC-1 + AC-8: the cross-tenant list is super-admin-only; a tenant-admin sees
// only their own tenant's users (P0-478-3 — no widening).
func TestList_SuperAdminCrossTenant_vs_TenantAdminScoped(t *testing.T) {
	skipIfNoAdminPool(t)
	tenantA := seedTenant478(t, "test-list-A")
	tenantB := seedTenant478(t, "test-list-B")
	superID := seedUser478(t, tenantA, "test-list-super@example.com", "List Super",
		"https://idp.test/", "test-list-super-sub")
	seedSuperAdmin(t, superID)
	_ = seedUser478(t, tenantA, "test-list-a-user@example.com", "A User", "https://idp.test/", "test-list-a-user-sub")
	_ = seedUser478(t, tenantB, "test-list-b-user@example.com", "B User", "https://idp.test/", "test-list-b-user-sub")

	// Super-admin: cross-tenant list includes BOTH tenants' users.
	hSuper := newRouter478(t, tenantA, superID, true)
	rrS := doJSON(t, hSuper, http.MethodGet, "/v1/admin/users?limit=200", nil)
	if rrS.Code != http.StatusOK {
		t.Fatalf("super list status = %d; body = %s", rrS.Code, rrS.Body.String())
	}
	var sresp adminusers.CrossTenantListResponse
	if err := json.Unmarshal(rrS.Body.Bytes(), &sresp); err != nil {
		t.Fatalf("decode super list: %v", err)
	}
	sawA, sawB := false, false
	for _, it := range sresp.Items {
		if it.Email == "test-list-a-user@example.com" {
			sawA = true
		}
		if it.Email == "test-list-b-user@example.com" {
			sawB = true
		}
	}
	if !sawA || !sawB {
		t.Errorf("super-admin cross-tenant list missing users (sawA=%v sawB=%v)", sawA, sawB)
	}

	// Tenant-admin of B: within-tenant list must NOT include tenant A's user.
	hTA := newTenantAdminRouter478(t, tenantB, uuid.New())
	rrT := doJSON(t, hTA, http.MethodGet, "/v1/admin/users", nil)
	if rrT.Code != http.StatusOK {
		t.Fatalf("tenant-admin list status = %d; body = %s", rrT.Code, rrT.Body.String())
	}
	var tresp adminusers.ListResponse
	if err := json.Unmarshal(rrT.Body.Bytes(), &tresp); err != nil {
		t.Fatalf("decode tenant-admin list: %v", err)
	}
	for _, it := range tresp.Items {
		if it.Email == "test-list-a-user@example.com" {
			t.Errorf("tenant-admin of B saw tenant A's user (RLS/scoping leak)")
		}
	}
}

// ----- helpers -----

func containsTenant(list []uuid.UUID, want uuid.UUID) bool {
	for _, t := range list {
		if t == want {
			return true
		}
	}
	return false
}

func assertSyntheticBackfill(t *testing.T, originUserID uuid.UUID) {
	t.Helper()
	var issuer, subject string
	if err := adminPool.QueryRow(context.Background(),
		`SELECT idp_issuer, idp_subject FROM users WHERE id = $1`, originUserID).Scan(&issuer, &subject); err != nil {
		t.Fatalf("read backfilled origin: %v", err)
	}
	if issuer != adminusers.LocalSyntheticIssuer {
		t.Errorf("origin %s idp_issuer = %q; want %q (backfill)", originUserID, issuer, adminusers.LocalSyntheticIssuer)
	}
	if subject != originUserID.String() {
		t.Errorf("origin %s idp_subject = %q; want %q (origin user id)", originUserID, subject, originUserID.String())
	}
}
