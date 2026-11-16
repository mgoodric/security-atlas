package risks

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
)

// TestRequireTaxonomyAdmin_Roles covers #1810's write gate, which reads
// authz.IsTenantApprover (#2447): admin or grc_engineer by flag, by the
// live tenant role authzmw stamps (TenantRoles), or by an API key's
// owner_roles label. A JWT role claim alone does not count, so a revoked
// grant is refused while the token that carried it is still live.
func TestRequireTaxonomyAdmin_Roles(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		cred credstore.Credential
		want bool
	}{
		"admin flag":                       {credstore.Credential{IsAdmin: true}, true},
		"approver flag":                    {credstore.Credential{IsApprover: true}, true},
		"live tenant admin":                {credstore.Credential{ID: "jwt:a", TenantRoles: []string{"admin"}}, true},
		"live grc_engineer":                {credstore.Credential{ID: "jwt:g", TenantRoles: []string{"viewer", "grc_engineer"}}, true},
		"live control_owner":               {credstore.Credential{ID: "jwt:c", TenantRoles: []string{"control_owner"}}, false},
		"live auditor":                     {credstore.Credential{ID: "jwt:u", TenantRoles: []string{"auditor"}}, false},
		"revoked admin, JWT claim live":    {credstore.Credential{ID: "jwt:r", OwnerRoles: []string{"admin"}}, false},
		"revoked grc_engineer, claim live": {credstore.Credential{ID: "jwt:s", OwnerRoles: []string{"grc_engineer"}}, false},
		"API key admin label":              {credstore.Credential{ID: "key_a", OwnerRoles: []string{"admin"}}, true},
		"API key viewer label":             {credstore.Credential{ID: "key_v", OwnerRoles: []string{"viewer"}}, false},
		"bare tenant credential":           {credstore.Credential{TenantID: "t"}, false},
	} {
		r := httptest.NewRequest(http.MethodPost, "/v1/risks/categories", nil)
		r = r.WithContext(authctx.WithCredential(r.Context(), tc.cred))
		w := httptest.NewRecorder()
		if got := requireTaxonomyAdmin(w, r); got != tc.want {
			t.Errorf("%s: requireTaxonomyAdmin = %v, want %v", name, got, tc.want)
		}
		if !tc.want && w.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", name, w.Code)
		}
	}
}

func TestRequireTaxonomyAdmin(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	if requireTaxonomyAdmin(w, httptest.NewRequest(http.MethodPost, "/v1/risks/categories", nil)) {
		t.Fatal("no credential was allowed")
	}
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/risks/categories", nil)
	r = r.WithContext(authctx.WithCredential(r.Context(), credstore.Credential{IsAdmin: true}))
	if !requireTaxonomyAdmin(httptest.NewRecorder(), r) {
		t.Fatal("admin was refused")
	}
}

func TestDecodeStrict_RefusesUnknownFields(t *testing.T) {
	t.Parallel()
	var req renameCategoryReq
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(`{"slug":"x"}`))
	if decodeStrict(w, r, &req) || w.Code != http.StatusBadRequest {
		t.Fatalf("slug in rename body: status %d; want refused with 400", w.Code)
	}
}
