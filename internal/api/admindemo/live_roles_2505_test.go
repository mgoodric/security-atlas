package admindemo

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
)

// TestRequireAdmin2505 pins the demo admin gate to live tenant roles: a JWT
// session whose role claim still says admin after a revoke is refused when
// its live role is narrower (#2505).
func TestRequireAdmin2505(t *testing.T) {
	t.Parallel()
	session := func(claim, live []string) credstore.Credential {
		return credstore.Credential{ID: "jwt:2505", UserID: "user:2505", TenantID: "t", OwnerRoles: claim, TenantRoles: live}
	}
	cases := []struct {
		name string
		cred credstore.Credential
		want int
	}{
		{"stale admin claim, live viewer", session([]string{"admin"}, []string{"viewer"}), http.StatusForbidden},
		{"stale admin claim, no live role", session([]string{"admin"}, nil), http.StatusForbidden},
		{"live admin, empty claim", session(nil, []string{"admin"}), http.StatusOK},
		{"admin flag", credstore.Credential{ID: "jwt:2505", IsAdmin: true}, http.StatusOK},
		{"unbound key, admin label", credstore.Credential{ID: "key_2505", UserID: "key_2505", OwnerRoles: []string{"admin"}}, http.StatusOK},
		{"bound key, stale admin label, live viewer", credstore.Credential{ID: "key_2505", UserID: "11111111-1111-1111-1111-111111111111", OwnerRoles: []string{"admin"}, TenantRoles: []string{"viewer"}}, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/v1/admin/demo/status", nil)
			req = req.WithContext(authctx.WithCredential(req.Context(), tc.cred))
			rec := httptest.NewRecorder()
			got := http.StatusOK
			if !(&Handler{}).requireAdmin(rec, req) {
				got = rec.Code
			}
			if got != tc.want {
				t.Errorf("requireAdmin status = %d; want %d", got, tc.want)
			}
		})
	}
}
