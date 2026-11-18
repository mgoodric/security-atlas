//go:build integration

// #2503: a tenant admin by its live roles manages its own tenant's users at
// /v1/admin/users, and nothing on the platform /v1/admin/* segments.
//
// Each principal is a users row with one user_roles grant, signed in with an
// ES256 atlas JWT that carries no super_admin claim. Requests run through
// jwtmw, tenancymw, authzmw (the embedded rego bundle with the DB-backed
// roles resolver on the RLS-enforced pool) and the real adminusers handlers,
// so the test shows what a person who signed in normally gets.
//
// Run with:  go test -tags=integration -race -run 2503 ./internal/api/adminusers/...

package adminusers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/adminauditlog"
	"github.com/mgoodric/security-atlas/internal/api/admingroupmappings"
	"github.com/mgoodric/security-atlas/internal/api/adminscim"
	"github.com/mgoodric/security-atlas/internal/api/adminsso"
	"github.com/mgoodric/security-atlas/internal/api/adminusers"
	"github.com/mgoodric/security-atlas/internal/api/authzmw"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/auth/bearer"
	"github.com/mgoodric/security-atlas/internal/auth/grouprole"
	"github.com/mgoodric/security-atlas/internal/auth/idpconfig"
	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
	"github.com/mgoodric/security-atlas/internal/auth/keystore/fsstore"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/scim"
)

const (
	issuer2503   = "https://atlas.example.test"
	audience2503 = "https://atlas.example.test"
)

// platformRoutes2503 are platform /v1/admin/* routes. authzmw must refuse a
// tenant admin by role on each before any handler runs.
var platformRoutes2503 = []struct{ method, path string }{
	{http.MethodGet, "/v1/admin/credentials"},
	{http.MethodPost, "/v1/admin/credentials"},
	{http.MethodGet, "/v1/admin/features"},
	{http.MethodGet, "/v1/admin/tenants"},
	{http.MethodPost, "/v1/admin/tenants"},
	{http.MethodGet, "/v1/admin/super-admins"},
	{http.MethodPost, "/v1/admin/super-admins"},
	{http.MethodPost, "/v1/admin/authz-bundle/reload"},
	{http.MethodGet, "/v1/admin/demo/status"},
	{http.MethodPost, "/v1/admin/demo/seed"},
	{http.MethodPost, "/v1/admin/install/reset-bootstrap"},
	{http.MethodPost, "/v1/admin/framework-versions/" + "00000000-0000-4000-8000-000000002503" + "/promote"},
}

type stack2503 struct {
	signer  *tokensign.Signer
	handler http.Handler
	reached *bool
}

func newStack2503(t *testing.T) stack2503 {
	t.Helper()
	store, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	signer := tokensign.New(store)
	engine, err := authz.NewEngine(context.Background(), authz.NewDBRolesResolver(appPool))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	h := adminusers.New(appPool).SetAuthPool(adminPool)
	reached := new(bool)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			*reached = true
			next.ServeHTTP(w, req)
		})
	})
	r.Get("/v1/admin/users", h.ListDispatch)
	r.Get("/v1/admin/users/{id}", h.Get)
	r.Patch("/v1/admin/users/{id}/roles", h.PatchRoles)
	r.Post("/v1/admin/users/assign", h.Assign)
	r.Post("/v1/admin/users/revoke", h.Revoke)
	// #2503: the tenant's SSO config, and (D5) its admin audit log.
	crypter, err := idpconfig.NewSecretCrypter(bytes.Repeat([]byte{0x25}, 32))
	if err != nil {
		t.Fatalf("NewSecretCrypter: %v", err)
	}
	sso := adminsso.New(appPool).WithSecretCrypter(crypter)
	r.Get("/v1/admin/sso", sso.Get)
	r.Patch("/v1/admin/sso", sso.Patch)
	// #2503: the tenant's SCIM bearer.
	hasher, err := bearer.NewHasher([]byte("test-2503-bearer-hash-key-at-least-32-bytes"))
	if err != nil {
		t.Fatalf("hasher: %v", err)
	}
	scimStore := scim.NewCredentialStore(appPool, adminPool, hasher)
	scimStore.SetPrefix(bearer.PrefixTest)
	scimH := adminscim.New(scimStore)
	r.Post("/v1/admin/scim-credentials", scimH.Issue)
	r.Get("/v1/admin/scim-credentials", scimH.List)
	r.Delete("/v1/admin/scim-credentials/{id}", scimH.Revoke)
	// #2503: the tenant's IdP group-to-role map.
	groupMap := admingroupmappings.New(grouprole.NewStore(appPool))
	r.Post("/v1/admin/group-role-mappings", groupMap.Create)
	r.Get("/v1/admin/group-role-mappings", groupMap.List)
	r.Delete("/v1/admin/group-role-mappings/{id}", groupMap.Delete)
	auditLog := adminauditlog.New(appPool)
	r.Get("/v1/admin/audit-log", auditLog.List)
	r.Get("/v1/admin/audit-log/unified", auditLog.UnifiedList)
	r.Get("/v1/admin/audit-log/export", auditLog.ExportUnified)
	// Any platform route that gets past authzmw answers 204, which the
	// test reads as a failure.
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	chain := jwtmw.Middleware(signer, nil, jwtmw.Options{
		ExpectedIssuer:   issuer2503,
		ExpectedAudience: audience2503,
	})(tenancymw.Middleware(authzmw.Middleware(engine, nil)(r)))
	return stack2503{signer: signer, handler: chain, reached: reached}
}

func (s stack2503) mint(t *testing.T, tenant, user uuid.UUID, role authz.Role) string {
	t.Helper()
	now := time.Now()
	tok, err := s.signer.Sign(context.Background(), jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer2503,
			Subject:   "user:" + user.String(),
			Audience:  []string{audience2503},
			ExpiresAt: now.Add(time.Hour).Unix(),
			IssuedAt:  now.Unix(),
			NotBefore: now.Unix(),
			ID:        uuid.NewString(),
		},
		IDPIssuer:        "local",
		CurrentTenantID:  tenant,
		AvailableTenants: []uuid.UUID{tenant},
		Roles:            map[uuid.UUID][]string{tenant: {string(role)}},
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return tok
}

func (s stack2503) do(t *testing.T, bearer, method, path string, body any) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	*s.reached = false
	var buf []byte
	if body != nil {
		buf, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(buf))
	req.Header.Set("Authorization", "Bearer "+bearer)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	s.handler.ServeHTTP(rr, req)
	return rr, *s.reached
}

func liveRoles2503(t *testing.T, tenant, user uuid.UUID) []string {
	t.Helper()
	rows, err := adminPool.Query(context.Background(),
		`SELECT role FROM user_roles WHERE tenant_id = $1 AND user_id = $2 ORDER BY role`, tenant, user.String())
	if err != nil {
		t.Fatalf("roles: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, r)
	}
	return out
}

// A tenant admin by role lists, reads and re-roles its own tenant's members.
// Ids from another tenant are 404 and leave nothing behind; cross-tenant
// assign and revoke are 403; the last-admin guard still holds.
func TestTenantAdmin2503_ManagesOwnTenantUsers(t *testing.T) {
	skipIfNoAdminPool(t)
	s := newStack2503(t)
	own := seedTenant478(t, "test-2503-own-"+uuid.NewString()[:8])
	other := seedTenant478(t, "test-2503-other-"+uuid.NewString()[:8])
	admin := seedMember2261(t, own, "active", "admin")
	member := seedMember2261(t, own, "active", "viewer")
	foreign := seedMember2261(t, other, "active", "admin")
	bearer := s.mint(t, own, admin, authz.RoleAdmin)

	t.Run("list shows own tenant only", func(t *testing.T) {
		rr, _ := s.do(t, bearer, http.MethodGet, "/v1/admin/users", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("GET /v1/admin/users = %d %s, want 200", rr.Code, rr.Body.String())
		}
		var resp adminusers.ListResponse
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		seen := map[string]bool{}
		for _, it := range resp.Items {
			seen[it.ID] = true
		}
		if !seen[admin.String()] || !seen[member.String()] {
			t.Errorf("list missing own members: %v", seen)
		}
		if seen[foreign.String()] {
			t.Errorf("list shows another tenant's user")
		}
	})

	t.Run("read own member; foreign id is 404", func(t *testing.T) {
		if rr, _ := s.do(t, bearer, http.MethodGet, "/v1/admin/users/"+member.String(), nil); rr.Code != http.StatusOK {
			t.Errorf("GET own member = %d, want 200", rr.Code)
		}
		if rr, _ := s.do(t, bearer, http.MethodGet, "/v1/admin/users/"+foreign.String(), nil); rr.Code != http.StatusNotFound {
			t.Errorf("GET foreign user = %d, want 404", rr.Code)
		}
	})

	t.Run("set own member's roles; foreign id is 404 with no row written", func(t *testing.T) {
		rr, _ := s.do(t, bearer, http.MethodPatch, "/v1/admin/users/"+member.String()+"/roles",
			map[string]any{"roles": []string{"grc_engineer"}})
		if rr.Code != http.StatusOK {
			t.Fatalf("PATCH own member roles = %d %s, want 200", rr.Code, rr.Body.String())
		}
		if got := liveRoles2503(t, own, member); len(got) != 1 || got[0] != "grc_engineer" {
			t.Errorf("member roles = %v, want [grc_engineer]", got)
		}
		rr, _ = s.do(t, bearer, http.MethodPatch, "/v1/admin/users/"+foreign.String()+"/roles",
			map[string]any{"roles": []string{"admin"}})
		if rr.Code != http.StatusNotFound {
			t.Errorf("PATCH foreign user roles = %d, want 404", rr.Code)
		}
		if got := liveRoles2503(t, own, foreign); len(got) != 0 {
			t.Errorf("foreign user gained roles in own tenant: %v", got)
		}
		if got := liveRoles2503(t, other, foreign); len(got) != 1 || got[0] != "admin" {
			t.Errorf("foreign user's own-tenant roles changed: %v", got)
		}
	})

	t.Run("super_admin is not a grantable role", func(t *testing.T) {
		rr, _ := s.do(t, bearer, http.MethodPatch, "/v1/admin/users/"+member.String()+"/roles",
			map[string]any{"roles": []string{"super_admin"}})
		if rr.Code != http.StatusBadRequest {
			t.Errorf("PATCH roles=[super_admin] = %d, want 400", rr.Code)
		}
	})

	t.Run("assign within own tenant; cross-tenant assign is 403", func(t *testing.T) {
		rr, _ := s.do(t, bearer, http.MethodPost, "/v1/admin/users/assign", map[string]any{
			"user_id": member.String(), "tenant_id": own.String(), "roles": []string{"auditor"},
		})
		if rr.Code != http.StatusOK {
			t.Errorf("assign within own tenant = %d %s, want 200", rr.Code, rr.Body.String())
		}
		rr, _ = s.do(t, bearer, http.MethodPost, "/v1/admin/users/assign", map[string]any{
			"user_id": admin.String(), "tenant_id": other.String(), "roles": []string{"admin"},
		})
		if rr.Code != http.StatusForbidden {
			t.Errorf("assign into another tenant = %d, want 403", rr.Code)
		}
	})

	t.Run("revoke: foreign id is 404, cross-tenant is 403", func(t *testing.T) {
		rr, _ := s.do(t, bearer, http.MethodPost, "/v1/admin/users/revoke", revokeBody(foreign, own, false))
		if rr.Code != http.StatusNotFound {
			t.Errorf("revoke foreign id in own tenant = %d, want 404", rr.Code)
		}
		rr, _ = s.do(t, bearer, http.MethodPost, "/v1/admin/users/revoke", revokeBody(foreign, other, false))
		if rr.Code != http.StatusForbidden {
			t.Errorf("revoke in another tenant = %d, want 403", rr.Code)
		}
		if got := liveRoles2503(t, other, foreign); len(got) != 1 {
			t.Errorf("foreign user's roles changed: %v", got)
		}
	})

	t.Run("last-admin guard holds", func(t *testing.T) {
		rr, _ := s.do(t, bearer, http.MethodPatch, "/v1/admin/users/"+admin.String()+"/roles",
			map[string]any{"roles": []string{"viewer"}, "confirm_self_demotion": true})
		if rr.Code != http.StatusConflict {
			t.Errorf("self-demote the only admin = %d, want 409", rr.Code)
		}
		rr, _ = s.do(t, bearer, http.MethodPost, "/v1/admin/users/revoke", revokeBody(admin, own, false))
		if rr.Code != http.StatusConflict {
			t.Errorf("revoke the only admin = %d, want 409", rr.Code)
		}
		if got := liveRoles2503(t, own, admin); len(got) != 1 || got[0] != "admin" {
			t.Errorf("admin roles = %v, want [admin]", got)
		}
	})
}

// #2503: a tenant admin by role saves and reads its own tenant's SSO config,
// and (D5, Matt 2026-11-18) reads and exports its own tenant's admin audit
// log. Another tenant's admin sees none of it.
func TestTenantAdmin2503_SSOAndAuditLog(t *testing.T) {
	skipIfNoAdminPool(t)
	s := newStack2503(t)
	own := seedTenant478(t, "test-2503-sso-"+uuid.NewString()[:8])
	other := seedTenant478(t, "test-2503-sso-other-"+uuid.NewString()[:8])
	t.Cleanup(func() {
		_, _ = adminPool.Exec(context.Background(),
			`DELETE FROM oidc_idp_configs WHERE tenant_id = ANY($1)`, []uuid.UUID{own, other})
	})
	bearer := s.mint(t, own, seedMember2261(t, own, "active", "admin"), authz.RoleAdmin)
	otherBearer := s.mint(t, other, seedMember2261(t, other, "active", "admin"), authz.RoleAdmin)

	if rr, _ := s.do(t, bearer, http.MethodGet, "/v1/admin/sso", nil); rr.Code != http.StatusNotFound {
		t.Fatalf("GET sso before any config = %d %s, want 404", rr.Code, rr.Body.String())
	}
	rr, _ := s.do(t, bearer, http.MethodPatch, "/v1/admin/sso", map[string]any{
		"issuer_url":            "https://accounts.example.com",
		"client_id":             "client-2503",
		"client_secret":         "neutral-test-secret",
		"redirect_url":          "https://atlas.example.com/auth/oidc/callback",
		"allowed_email_domains": []string{"example.com"},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("PATCH sso = %d %s, want 200", rr.Code, rr.Body.String())
	}
	rr, _ = s.do(t, bearer, http.MethodGet, "/v1/admin/sso", nil)
	if rr.Code != http.StatusOK || !bytes.Contains(rr.Body.Bytes(), []byte("client-2503")) {
		t.Errorf("GET own sso = %d %s, want 200 with client-2503", rr.Code, rr.Body.String())
	}
	if rr, _ := s.do(t, otherBearer, http.MethodGet, "/v1/admin/sso", nil); rr.Code != http.StatusNotFound {
		t.Errorf("other tenant's admin GET sso = %d, want 404 (no config of its own)", rr.Code)
	}

	from := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	for _, path := range []string{
		"/v1/admin/audit-log",
		"/v1/admin/audit-log/unified?from=" + from + "&to=" + to,
		"/v1/admin/audit-log/export?format=csv&from=" + from + "&to=" + to,
	} {
		if rr, _ := s.do(t, bearer, http.MethodGet, path, nil); rr.Code != http.StatusOK {
			t.Errorf("tenant admin GET %s = %d %s, want 200", path, rr.Code, rr.Body.String())
		}
	}
}

// #2503: a tenant admin by role issues, lists and revokes its own tenant's
// SCIM bearer. Another tenant's admin neither lists nor revokes it (404).
func TestTenantAdmin2503_SCIMCredentials(t *testing.T) {
	skipIfNoAdminPool(t)
	s := newStack2503(t)
	own := seedTenant478(t, "test-2503-scim-"+uuid.NewString()[:8])
	other := seedTenant478(t, "test-2503-scim-other-"+uuid.NewString()[:8])
	t.Cleanup(func() {
		_, _ = adminPool.Exec(context.Background(),
			`DELETE FROM scim_credentials WHERE tenant_id = ANY($1)`, []uuid.UUID{own, other})
	})
	bearerTok := s.mint(t, own, seedMember2261(t, own, "active", "admin"), authz.RoleAdmin)
	otherTok := s.mint(t, other, seedMember2261(t, other, "active", "admin"), authz.RoleAdmin)

	rr, _ := s.do(t, bearerTok, http.MethodPost, "/v1/admin/scim-credentials",
		map[string]any{"description": "2503 e2e"})
	if rr.Code != http.StatusCreated && rr.Code != http.StatusOK {
		t.Fatalf("issue scim credential = %d %s, want 200/201", rr.Code, rr.Body.String())
	}
	var issued struct {
		ID       string `json:"id"`
		TenantID string `json:"tenant_id"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&issued); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	if issued.TenantID != own.String() {
		t.Errorf("issued tenant_id = %s, want the caller's tenant %s", issued.TenantID, own)
	}

	if rr, _ := s.do(t, bearerTok, http.MethodGet, "/v1/admin/scim-credentials", nil); rr.Code != http.StatusOK ||
		!bytes.Contains(rr.Body.Bytes(), []byte(issued.ID)) {
		t.Errorf("own list = %d %s, want 200 listing %s", rr.Code, rr.Body.String(), issued.ID)
	}
	if rr, _ := s.do(t, otherTok, http.MethodGet, "/v1/admin/scim-credentials", nil); rr.Code != http.StatusOK ||
		bytes.Contains(rr.Body.Bytes(), []byte(issued.ID)) {
		t.Errorf("other tenant's list = %d %s, want 200 without %s", rr.Code, rr.Body.String(), issued.ID)
	}
	if rr, _ := s.do(t, otherTok, http.MethodDelete, "/v1/admin/scim-credentials/"+issued.ID, nil); rr.Code != http.StatusNotFound {
		t.Errorf("other tenant's revoke = %d, want 404", rr.Code)
	}
	if rr, _ := s.do(t, bearerTok, http.MethodDelete, "/v1/admin/scim-credentials/"+issued.ID, nil); rr.Code != http.StatusNoContent {
		t.Errorf("own revoke = %d %s, want 204", rr.Code, rr.Body.String())
	}
}

// #2503: a tenant admin by role maps its own tenant's IdP groups to
// canonical roles, never to super_admin. Another tenant's admin neither
// lists nor deletes the mapping (404).
func TestTenantAdmin2503_GroupRoleMappings(t *testing.T) {
	skipIfNoAdminPool(t)
	s := newStack2503(t)
	own := seedTenant478(t, "test-2503-grp-"+uuid.NewString()[:8])
	other := seedTenant478(t, "test-2503-grp-other-"+uuid.NewString()[:8])
	t.Cleanup(func() {
		_, _ = adminPool.Exec(context.Background(),
			`DELETE FROM oidc_idp_group_mappings WHERE tenant_id = ANY($1)`, []uuid.UUID{own, other})
	})
	tok := s.mint(t, own, seedMember2261(t, own, "active", "admin"), authz.RoleAdmin)
	otherTok := s.mint(t, other, seedMember2261(t, other, "active", "admin"), authz.RoleAdmin)

	if rr, _ := s.do(t, tok, http.MethodPost, "/v1/admin/group-role-mappings",
		map[string]any{"group_ref": "platform-admins-2503", "role": "super_admin"}); rr.Code != http.StatusBadRequest {
		t.Errorf("map a group to super_admin = %d, want 400", rr.Code)
	}
	rr, _ := s.do(t, tok, http.MethodPost, "/v1/admin/group-role-mappings",
		map[string]any{"group_ref": "auditors-2503", "role": "auditor"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create mapping = %d %s, want 201", rr.Code, rr.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if rr, _ := s.do(t, tok, http.MethodGet, "/v1/admin/group-role-mappings", nil); rr.Code != http.StatusOK ||
		!bytes.Contains(rr.Body.Bytes(), []byte(created.ID)) {
		t.Errorf("own list = %d %s, want 200 listing %s", rr.Code, rr.Body.String(), created.ID)
	}
	if rr, _ := s.do(t, otherTok, http.MethodGet, "/v1/admin/group-role-mappings", nil); rr.Code != http.StatusOK ||
		bytes.Contains(rr.Body.Bytes(), []byte(created.ID)) {
		t.Errorf("other tenant's list = %d %s, want 200 without %s", rr.Code, rr.Body.String(), created.ID)
	}
	if rr, _ := s.do(t, otherTok, http.MethodDelete, "/v1/admin/group-role-mappings/"+created.ID, nil); rr.Code != http.StatusNotFound {
		t.Errorf("other tenant's delete = %d, want 404", rr.Code)
	}
	if rr, _ := s.do(t, tok, http.MethodDelete, "/v1/admin/group-role-mappings/"+created.ID, nil); rr.Code != http.StatusNoContent {
		t.Errorf("own delete = %d %s, want 204", rr.Code, rr.Body.String())
	}
}

// A tenant admin by role is refused by OPA on every platform segment.
func TestTenantAdmin2503_PlatformRoutesRefused(t *testing.T) {
	skipIfNoAdminPool(t)
	s := newStack2503(t)
	own := seedTenant478(t, "test-2503-plat-"+uuid.NewString()[:8])
	admin := seedMember2261(t, own, "active", "admin")
	bearer := s.mint(t, own, admin, authz.RoleAdmin)
	for _, rt := range platformRoutes2503 {
		rr, reached := s.do(t, bearer, rt.method, rt.path, nil)
		if rr.Code != http.StatusForbidden || reached {
			t.Errorf("tenant admin: %s %s = %d (reached router=%v), want OPA's 403", rt.method, rt.path, rr.Code, reached)
		}
	}
}

// Every other role is refused on the users routes, as before #2503.
func TestTenantAdmin2503_OtherRolesRefused(t *testing.T) {
	skipIfNoAdminPool(t)
	s := newStack2503(t)
	own := seedTenant478(t, "test-2503-roles-"+uuid.NewString()[:8])
	target := seedMember2261(t, own, "active", "viewer")
	for _, role := range []authz.Role{authz.RoleGRCEngineer, authz.RoleControlOwner, authz.RoleAuditor, authz.RoleViewer} {
		bearer := s.mint(t, own, seedMember2261(t, own, "active", string(role)), role)
		for _, rt := range []struct {
			method, path string
			body         any
		}{
			{http.MethodGet, "/v1/admin/users", nil},
			{http.MethodGet, "/v1/admin/users/" + target.String(), nil},
			{http.MethodPatch, "/v1/admin/users/" + target.String() + "/roles", map[string]any{"roles": []string{"admin"}}},
			{http.MethodPost, "/v1/admin/users/assign", map[string]any{"user_id": target.String(), "tenant_id": own.String(), "roles": []string{"admin"}}},
			{http.MethodPost, "/v1/admin/users/revoke", revokeBody(target, own, false)},
		} {
			if rr, _ := s.do(t, bearer, rt.method, rt.path, rt.body); rr.Code != http.StatusForbidden {
				t.Errorf("%s: %s %s = %d, want 403", role, rt.method, rt.path, rr.Code)
			}
		}
	}
	if got := liveRoles2503(t, own, target); len(got) != 1 || got[0] != "viewer" {
		t.Errorf("target roles changed: %v", got)
	}

	// #2503 D5a (Matt, 2026-11-18): Users is org-admin only, so OPA itself
	// refuses grc_engineer's read; it no longer reaches the handler.
	grc := s.mint(t, own, seedMember2261(t, own, "active", "grc_engineer"), authz.RoleGRCEngineer)
	for _, path := range []string{"/v1/admin/users", "/v1/admin/users/" + target.String()} {
		if rr, reached := s.do(t, grc, http.MethodGet, path, nil); rr.Code != http.StatusForbidden || reached {
			t.Errorf("grc_engineer GET %s = %d (reached router=%v), want OPA's 403", path, rr.Code, reached)
		}
	}
}
