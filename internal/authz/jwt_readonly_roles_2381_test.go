// #2381: the credential-derived role set per JWT role claim, before any
// user_roles merge. The real-Postgres matrix is in
// jwt_readonly_roles_2381_integration_test.go.

package authz_test

import (
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
		{"JWT control_owner", credstore.Credential{ID: "jwt:1", TenantID: "t", UserID: "user:u", OwnerRoles: []string{"control_owner"}}, []authz.Role{authz.RoleControlOwner}},
		{"JWT grc_engineer", credstore.Credential{ID: "jwt:1", TenantID: "t", UserID: "user:u", OwnerRoles: []string{"grc_engineer"}}, []authz.Role{authz.RoleControlOwner}},
		{"JWT admin", credstore.Credential{ID: "jwt:1", TenantID: "t", UserID: "user:u", OwnerRoles: []string{"admin"}}, []authz.Role{authz.RoleControlOwner}},
		{"JWT auditor+control_owner", credstore.Credential{ID: "jwt:1", TenantID: "t", UserID: "user:u", OwnerRoles: []string{"auditor", "control_owner"}}, []authz.Role{authz.RoleControlOwner}},
		{"JWT empty claim", credstore.Credential{ID: "jwt:1", TenantID: "t", UserID: "user:u"}, []authz.Role{authz.RoleGRCEngineer}},
		{"key viewer", credstore.Credential{ID: "key_1", TenantID: "t", UserID: "key_1", OwnerRoles: []string{"viewer"}}, []authz.Role{authz.RoleControlOwner}},
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
