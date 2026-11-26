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
	h := New(oidc.New(unknownIdpResolver{}, testStateKey), nil, nil, false, nil)

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
	assertFlowCookiesCleared(t, rec, false)
}

// TestOIDCCallback_StateMismatchIs400AndClearsFlow covers #2177: a state
// the server did not sign is a 400 that also expires the flow cookies, for
// the plain-HTTP and the __Host- cookie names.
func TestOIDCCallback_StateMismatchIs400AndClearsFlow(t *testing.T) {
	t.Parallel()
	for _, secure := range []bool{false, true} {
		h := New(oidc.New(unknownIdpResolver{}, testStateKey), nil, nil, secure, nil)
		tenant := uuid.New()
		req := httptest.NewRequest(http.MethodGet,
			"/auth/oidc/callback?tenant_id="+tenant.String()+"&code=c&state=forged.mac", nil)
		for base, v := range map[string]string{
			oidc.StateCookie: "forged.mac", oidc.VerifierCookie: "v", oidc.IdpCookie: "default",
			oidc.NonceCookie: "n", oidc.TenantCookie: tenant.String(),
		} {
			req.AddCookie(&http.Cookie{Name: oidc.CookieName(base, secure), Value: v})
		}
		rec := httptest.NewRecorder()

		h.OIDCCallback(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("secure=%t: status = %d, want 400; body %s", secure, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "state mismatch") {
			t.Errorf("secure=%t: body = %q, want the state-mismatch message", secure, rec.Body.String())
		}
		assertFlowCookiesCleared(t, rec, secure)
	}
}

func assertFlowCookiesCleared(t *testing.T, rec *httptest.ResponseRecorder, secure bool) {
	t.Helper()
	got := map[string]*http.Cookie{}
	for _, c := range rec.Result().Cookies() {
		got[c.Name] = c
	}
	for _, base := range []string{oidc.StateCookie, oidc.VerifierCookie, oidc.IdpCookie, oidc.NonceCookie, oidc.TenantCookie} {
		name := oidc.CookieName(base, secure)
		if c, ok := got[name]; !ok || c.MaxAge >= 0 {
			t.Errorf("secure=%t: %s not expired on the 400: %+v", secure, name, c)
		}
	}
}
