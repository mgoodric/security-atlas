package mappingoverlays

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
)

// TestRequireOverlayWrite_Roles: the overlay write gate is
// authz.IsTenantApprover (#2447): admin or grc_engineer by flag, by the
// live tenant role authzmw stamps (TenantRoles), or by an API key's
// owner_roles label. A JWT role claim alone does not count, so a revoked
// grant is refused while the token that carried it is still live.
func TestRequireOverlayWrite_Roles(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		cred credstore.Credential
		want bool
	}{
		"admin flag":                           {credstore.Credential{IsAdmin: true}, true},
		"approver flag":                        {credstore.Credential{IsApprover: true}, true},
		"live tenant admin":                    {credstore.Credential{ID: "jwt:a", TenantRoles: []string{"admin"}}, true},
		"live grc_engineer":                    {credstore.Credential{ID: "jwt:g", TenantRoles: []string{"grc_engineer"}}, true},
		"live control_owner":                   {credstore.Credential{ID: "jwt:c", TenantRoles: []string{"control_owner"}}, false},
		"live auditor":                         {credstore.Credential{ID: "jwt:u", TenantRoles: []string{"auditor"}}, false},
		"live viewer":                          {credstore.Credential{ID: "jwt:v", TenantRoles: []string{"viewer"}}, false},
		"revoked admin, JWT claim live":        {credstore.Credential{ID: "jwt:r", OwnerRoles: []string{"admin"}}, false},
		"revoked grc_engineer, JWT claim live": {credstore.Credential{ID: "jwt:s", OwnerRoles: []string{"grc_engineer"}}, false},
		"API key grc_engineer label":           {credstore.Credential{ID: "key_g", OwnerRoles: []string{"grc_engineer"}}, true},
		"API key viewer label":                 {credstore.Credential{ID: "key_v", OwnerRoles: []string{"viewer"}}, false},
		"bare credential":                      {credstore.Credential{ID: "key_0"}, false},
	} {
		r := httptest.NewRequest(http.MethodPost, "/v1/mapping-overlays", nil)
		r = r.WithContext(authctx.WithCredential(r.Context(), tc.cred))
		w := httptest.NewRecorder()
		if got := requireOverlayWrite(w, r); got != tc.want {
			t.Errorf("%s: requireOverlayWrite = %v, want %v", name, got, tc.want)
		}
		if !tc.want && w.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", name, w.Code)
		}
	}
}
