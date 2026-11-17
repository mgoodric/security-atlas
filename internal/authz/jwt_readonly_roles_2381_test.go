// #2381: the credential-derived role set per JWT role claim, before any
// user_roles merge. The real-Postgres matrix is in
// jwt_readonly_roles_2381_integration_test.go.

package authz_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
)

// TestBuildInput2381_DerivedRoles pins the credential-derived role set per
// JWT role claim, before any user_roles merge.
func TestBuildInput2381_DerivedRoles(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		cred credstore.Credential
		want []authz.Role
	}{
		{"JWT viewer", credstore.Credential{ID: "jwt:1", TenantID: "t", UserID: "user:u", OwnerRoles: []string{"viewer"}}, nil},
		{"JWT auditor", credstore.Credential{ID: "jwt:1", TenantID: "t", UserID: "user:u", OwnerRoles: []string{"auditor"}}, nil},
		{"JWT viewer+auditor", credstore.Credential{ID: "jwt:1", TenantID: "t", UserID: "user:u", OwnerRoles: []string{"auditor", "viewer"}}, nil},
		// #2496: no user claim derives anything; live user_roles alone count.
		{"JWT control_owner", credstore.Credential{ID: "jwt:1", TenantID: "t", UserID: "user:u", OwnerRoles: []string{"control_owner"}}, nil},
		{"JWT grc_engineer", credstore.Credential{ID: "jwt:1", TenantID: "t", UserID: "user:u", OwnerRoles: []string{"grc_engineer"}}, nil},
		{"JWT admin", credstore.Credential{ID: "jwt:1", TenantID: "t", UserID: "user:u", OwnerRoles: []string{"admin"}}, nil},
		{"JWT auditor+control_owner", credstore.Credential{ID: "jwt:1", TenantID: "t", UserID: "user:u", OwnerRoles: []string{"auditor", "control_owner"}}, nil},
		// An empty claim derives nothing for a user (#2401); every principal
		// kind is in jwt_empty_claim_2401_test.go.
		{"JWT empty claim", credstore.Credential{ID: "jwt:1", TenantID: "t", UserID: "user:6f1c2a9e-1d2b-4c3d-8e4f-5a6b7c8d9e0f"}, nil},
		{"unbound key viewer", credstore.Credential{ID: "key_1", TenantID: "t", UserID: "key_1", OwnerRoles: []string{"viewer"}}, []authz.Role{authz.RoleViewer}},
		{"unbound key auditor", credstore.Credential{ID: "key_1", TenantID: "t", UserID: "key_1", OwnerRoles: []string{"auditor"}}, []authz.Role{authz.RoleAuditor}},
		{"unbound key auditor+viewer", credstore.Credential{ID: "key_1", TenantID: "t", UserID: "key_1", OwnerRoles: []string{"auditor", "viewer", "viewer"}}, []authz.Role{authz.RoleViewer, authz.RoleAuditor}},
		{"bound key viewer", credstore.Credential{ID: "key_1", TenantID: "t", UserID: "6f1c2a9e-1d2b-4c3d-8e4f-5a6b7c8d9e0f", OwnerRoles: []string{"viewer"}}, nil},
		{"bound key auditor", credstore.Credential{ID: "key_1", TenantID: "t", UserID: "6f1c2a9e-1d2b-4c3d-8e4f-5a6b7c8d9e0f", OwnerRoles: []string{"auditor"}}, nil},
		{"key control_owner", credstore.Credential{ID: "key_1", TenantID: "t", UserID: "key_1", OwnerRoles: []string{"control_owner"}}, []authz.Role{authz.RoleControlOwner}},
		{"key viewer+owner label", credstore.Credential{ID: "key_1", TenantID: "t", UserID: "key_1", OwnerRoles: []string{"viewer", "platform-eng"}}, []authz.Role{authz.RoleControlOwner}},
	} {
		req := httptest.NewRequest(http.MethodGet, "/v1/controls", nil)
		req = req.WithContext(authctx.WithCredential(req.Context(), tc.cred))
		got := authz.BuildInput(req, nil).User.Roles
		if len(got) != len(tc.want) {
			t.Errorf("%s: roles %v; want %v", tc.name, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: roles %v; want %v", tc.name, got, tc.want)
			}
		}
	}
}

// TestDecide2381_EvidenceFreshnessOnly pins both sides of the line the
// dashboard needs: viewer and auditor read the freshness aggregate (counts
// per class, no evidence rows) and stay denied the evidence list and a
// single record until #2157. The admit is GET only.
func TestDecide2381_EvidenceFreshnessOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e, err := authz.NewEngine(ctx, authz.NoopRolesResolver{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	decide := func(role authz.Role, method, path string) bool {
		req := httptest.NewRequest(method, path, nil)
		req = req.WithContext(authctx.WithCredential(req.Context(), credstore.Credential{
			ID: "jwt:f", TenantID: "t", UserID: "user:u", OwnerRoles: []string{string(role)},
		}))
		in := authz.BuildInput(req, nil)
		in.User.Roles = []authz.Role{role}
		d, err := e.Decide(ctx, in)
		if err != nil {
			t.Fatalf("Decide %s %s: %v", method, path, err)
		}
		return d.Allow
	}
	for _, role := range []authz.Role{authz.RoleViewer, authz.RoleAuditor} {
		for _, tc := range []struct {
			method, path string
			want         bool
		}{
			{http.MethodGet, "/v1/evidence/freshness", true},
			{http.MethodGet, "/v1/evidence", false},
			{http.MethodGet, "/v1/evidence/6f1c2a9e-1d2b-4c3d-8e4f-5a6b7c8d9e0f", false},
			{http.MethodPost, "/v1/evidence/freshness", false},
		} {
			if got := decide(role, tc.method, tc.path); got != tc.want {
				t.Errorf("%s %s %s: allow=%v, want %v", role, tc.method, tc.path, got, tc.want)
			}
		}
	}
}
