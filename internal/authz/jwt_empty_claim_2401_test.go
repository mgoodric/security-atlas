// #2401: the credential-derived role set for an empty role claim, per
// principal kind, before any user_roles merge. The real-Postgres matrix is
// in jwt_empty_claim_2401_integration_test.go.

package authz_test

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
)

func TestBuildInput2401_EmptyClaimDerivedRoles(t *testing.T) {
	t.Parallel()
	const user = "6f1c2a9e-1d2b-4c3d-8e4f-5a6b7c8d9e0f"
	grc := []authz.Role{authz.RoleGRCEngineer}
	for _, tc := range []struct {
		name string
		cred credstore.Credential
		want []authz.Role
	}{
		// A user's session: roles come from live user_roles only.
		{"JWT user: subject", credstore.Credential{ID: "jwt:1", TenantID: "t", UserID: "user:" + user}, nil},
		{"JWT bare uuid subject", credstore.Credential{ID: "jwt:1", TenantID: "t", UserID: user}, nil},
		{"JWT user, empty slice", credstore.Credential{ID: "jwt:1", TenantID: "t", UserID: "user:" + user, OwnerRoles: []string{}}, nil},
		// Any other JWT subject shape, known or not, derives nothing too:
		// the empty-claim default is an allow-list.
		{"JWT nil-uuid subject", credstore.Credential{ID: "jwt:1", TenantID: "t", UserID: "user:00000000-0000-0000-0000-000000000000"}, nil},
		{"JWT non-canonical uuid", credstore.Credential{ID: "jwt:1", TenantID: "t", UserID: "user:" + "6F1C2A9E1D2B4C3D8E4F5A6B7C8D9E0F"}, nil},
		{"JWT test-harness subject", credstore.Credential{ID: "jwt:1", TenantID: "t", UserID: "test-viewer:t"}, nil},
		{"JWT unknown shape", credstore.Credential{ID: "jwt:1", TenantID: "t", UserID: "someone@example.invalid"}, nil},
		{"JWT empty subject", credstore.Credential{ID: "jwt:1", TenantID: "t"}, nil},
		// Kept: a client_credentials machine token names no user, so it has
		// no user_roles to fall back on, and its claim is always empty.
		{"JWT oauth_client machine", credstore.Credential{ID: "jwt:1", TenantID: "t", UserID: "oauth_client:c1"}, grc},
		// Not JWTs: unchanged by #2401.
		{"push key", credstore.Credential{ID: "key_1", TenantID: "t", UserID: "key_1"}, grc},
		{"bootstrap credential", credstore.Credential{ID: "boot", TenantID: "t", UserID: "bootstrap-admin"}, grc},
		// Unchanged by #2401.
		{"JWT user, control_owner claim", credstore.Credential{ID: "jwt:1", TenantID: "t", UserID: "user:" + user, OwnerRoles: []string{"control_owner"}}, []authz.Role{authz.RoleControlOwner}},
		{"JWT super_admin, empty claim", credstore.Credential{ID: "jwt:1", TenantID: "t", UserID: "user:" + user, IsAdmin: true, IsApprover: true}, []authz.Role{authz.RoleAdmin}},
		{"no tenant", credstore.Credential{ID: "jwt:1", UserID: "user:" + user}, nil},
	} {
		req := httptest.NewRequest(http.MethodGet, "/v1/controls", nil)
		req = req.WithContext(authctx.WithCredential(req.Context(), tc.cred))
		if got := authz.BuildInput(req, nil).User.Roles; !slices.Equal(got, tc.want) {
			t.Errorf("%s: roles %v; want %v", tc.name, got, tc.want)
		}
	}
}
