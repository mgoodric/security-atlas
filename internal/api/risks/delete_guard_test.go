package risks

// #2107 — hard delete is admin-only. The guard runs before the tenant or
// the store is read, so every non-admin credential gets a 403 here with
// no database. The admin path is covered by the lifecycle integration
// test.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
)

func TestDeleteRisk_RefusesNonAdmins(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	for name, cred := range map[string]credstore.Credential{
		"approver flag":   {ID: "key_appr", TenantID: "t", IsApprover: true},
		"grc_engineer":    {ID: "jwt:grc", TenantID: "t", OwnerRoles: []string{"grc_engineer"}},
		"control_owner":   {ID: "jwt:co", TenantID: "t", OwnerRoles: []string{"control_owner"}},
		"bare credential": {ID: "key_bare", TenantID: "t"},
	} {
		r := httptest.NewRequest(http.MethodDelete, "/v1/risks/6f1c2a52-3b0e-4b8e-9a51-1d6a3f0c2107", nil)
		r = r.WithContext(authctx.WithCredential(r.Context(), cred))
		w := httptest.NewRecorder()
		router := chi.NewRouter()
		router.Delete("/v1/risks/{id}", h.DeleteRisk)
		router.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403", name, w.Code)
		}
	}
}

func TestIsTenantAdmin(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		cred credstore.Credential
		want bool
	}{
		{"admin flag", credstore.Credential{IsAdmin: true}, true},
		{"tenant admin role", credstore.Credential{OwnerRoles: []string{"grc_engineer", "admin"}}, true},
		{"approver flag", credstore.Credential{IsApprover: true}, false},
		{"grc_engineer", credstore.Credential{OwnerRoles: []string{"grc_engineer"}}, false},
		{"none", credstore.Credential{}, false},
	} {
		if got := isTenantAdmin(c.cred); got != c.want {
			t.Errorf("%s: isTenantAdmin = %v, want %v", c.name, got, c.want)
		}
	}
}
