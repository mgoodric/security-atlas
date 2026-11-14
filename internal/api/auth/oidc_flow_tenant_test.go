package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/auth/oidc"
)

// TestOIDCCallback_TenantOutsideFlowIs400 covers #1968: a callback whose
// tenant differs from the one /auth/oidc/login bound to the flow is a 400,
// answered before the IdP is looked up or a user is touched.
func TestOIDCCallback_TenantOutsideFlowIs400(t *testing.T) {
	t.Parallel()
	h := New(oidc.New(unknownIdpResolver{}), nil, nil, false, nil)

	req := httptest.NewRequest(http.MethodGet,
		"/auth/oidc/callback?tenant_id="+uuid.New().String()+"&code=c&state=S", nil)
	req.AddCookie(&http.Cookie{Name: oidc.StateCookie, Value: "S"})
	req.AddCookie(&http.Cookie{Name: oidc.VerifierCookie, Value: "v"})
	req.AddCookie(&http.Cookie{Name: oidc.IdpCookie, Value: "default"})
	req.AddCookie(&http.Cookie{Name: oidc.NonceCookie, Value: "n"})
	req.AddCookie(&http.Cookie{Name: oidc.TenantCookie, Value: uuid.New().String()})
	rec := httptest.NewRecorder()

	h.OIDCCallback(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "tenant does not match the login flow") {
		t.Errorf("body = %q, want the flow-tenant message", rec.Body.String())
	}
}
