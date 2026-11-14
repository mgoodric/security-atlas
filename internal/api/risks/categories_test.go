package risks

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
)

// TestHasTaxonomyAdmin covers #1810's write gate: admin and grc_engineer,
// by flag or by JWT tenant role, may manage categories; nobody else may.
func TestHasTaxonomyAdmin(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		cred credstore.Credential
		want bool
	}{
		"admin flag":             {credstore.Credential{IsAdmin: true}, true},
		"approver flag":          {credstore.Credential{IsApprover: true}, true},
		"tenant admin role":      {credstore.Credential{OwnerRoles: []string{"admin"}}, true},
		"grc_engineer role":      {credstore.Credential{OwnerRoles: []string{"viewer", "grc_engineer"}}, true},
		"control owner role":     {credstore.Credential{OwnerRoles: []string{"control_owner"}}, false},
		"auditor role":           {credstore.Credential{OwnerRoles: []string{"auditor"}}, false},
		"bare tenant credential": {credstore.Credential{TenantID: "t"}, false},
	} {
		if got := hasTaxonomyAdmin(tc.cred); got != tc.want {
			t.Errorf("%s: hasTaxonomyAdmin = %v, want %v", name, got, tc.want)
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
