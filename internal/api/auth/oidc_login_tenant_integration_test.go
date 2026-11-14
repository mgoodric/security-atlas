//go:build integration

// Handler-level integration tests for #1761: GET /auth/oidc/login and its
// callback without tenant_id, against real Postgres with the BYPASSRLS
// implicit-tenant resolver wired as cmd/atlas wires it, and an in-process
// fake IdP.
//
// Run via: go test -tags=integration ./internal/api/auth/...
// (needs DATABASE_URL = BYPASSRLS migrate role, DATABASE_URL_APP = atlas_app).
package auth_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	authapi "github.com/mgoodric/security-atlas/internal/api/auth"
	"github.com/mgoodric/security-atlas/internal/auth/oidc"
	"github.com/mgoodric/security-atlas/internal/auth/sessions"
	"github.com/mgoodric/security-atlas/internal/auth/users"
)

// perTenantIdps resolves an IdP config by tenant, so a test can tell which
// tenant's config a login used. An unlisted tenant is an unknown IdP.
type perTenantIdps map[uuid.UUID]oidc.IdpConfig

func (p perTenantIdps) ResolveIdp(_ context.Context, tenantID uuid.UUID, name string) (oidc.IdpConfig, error) {
	cfg, ok := p[tenantID]
	if !ok || cfg.Name != name {
		return oidc.IdpConfig{}, oidc.ErrUnknownIdp
	}
	return cfg, nil
}

func (h harness) oidcHandler(idps perTenantIdps) *authapi.Handler {
	handler := authapi.New(
		oidc.New(idps),
		users.NewStoreWithAuthPool(h.app, h.admin),
		sessions.NewStore(h.app, time.Hour),
		false,
		nil,
	)
	handler.AttachImplicitTenantResolver(authapi.PoolImplicitTenantResolver{Q: h.admin})
	return handler
}

func idpConfigFor(tenantID uuid.UUID, idp *fakeIdP, clientID string) oidc.IdpConfig {
	return oidc.IdpConfig{
		ID:           uuid.New(),
		TenantID:     tenantID,
		Name:         "primary",
		IssuerURL:    idp.srv.URL,
		ClientID:     clientID,
		ClientSecret: "test-secret",
		RedirectURL:  "http://localhost:9999/auth/oidc/callback",
	}
}

func getOIDCLogin(handler *authapi.Handler, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/auth/oidc/login?"+query, nil)
	rec := httptest.NewRecorder()
	handler.OIDCLogin(rec, req)
	return rec
}

// One real tenant plus the all-zeros row: /auth/oidc/login?idp=primary
// with no tenant_id redirects to the sole tenant's IdP, and the callback,
// also without tenant_id, establishes the session in that tenant.
func TestOIDCLogin_SingleTenant_NoTenantID_UsesSoleTenantIdp(t *testing.T) {
	h := newHarness(t)
	h.insertTenant(t, uuid.Nil, "nil", "nil-tenant", false)
	only := uuid.New()
	h.insertTenant(t, only, "Solo Org", "solo-org", true)
	// An established install, so the callback takes the ordinary
	// upsert path rather than the first-install bootstrap branch.
	h.createLocalUser(t, only)

	idp := newFakeIdP(t)
	handler := h.oidcHandler(perTenantIdps{only: idpConfigFor(only, idp, fakeClientID)})

	rec := getOIDCLogin(handler, "idp=primary")
	if rec.Code != http.StatusFound {
		t.Fatalf("login status = %d, want 302; body %s", rec.Code, rec.Body.String())
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if got, want := loc.Scheme+"://"+loc.Host+loc.Path, idp.srv.URL+"/authorize"; got != want {
		t.Errorf("redirect target = %q, want the sole tenant's IdP %q", got, want)
	}
	if got := loc.Query().Get("client_id"); got != fakeClientID {
		t.Errorf("client_id = %q, want the sole tenant's %q", got, fakeClientID)
	}

	flowCookies := rec.Result().Cookies()
	var state string
	for _, c := range flowCookies {
		switch c.Name {
		case oidc.StateCookie:
			state = c.Value
		case oidc.NonceCookie:
			idp.nonce = c.Value
		}
	}
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf(
		"/auth/oidc/callback?code=test-code&state=%s", url.QueryEscape(state)), nil)
	for _, c := range flowCookies {
		req.AddCookie(c)
	}
	cb := httptest.NewRecorder()
	handler.OIDCCallback(cb, req)

	if cb.Code != http.StatusFound {
		t.Fatalf("callback status = %d, want 302; body %s", cb.Code, cb.Body.String())
	}
	if cookieNamed(cb, sessions.CookieName) == nil {
		t.Fatalf("callback set no %s cookie", sessions.CookieName)
	}
	if n := h.count(t, `SELECT COUNT(*) FROM sessions WHERE tenant_id = $1`, only); n != 1 {
		t.Errorf("sessions in the sole tenant = %d, want 1", n)
	}
	if n := h.count(t, `SELECT COUNT(*) FROM sessions WHERE tenant_id <> $1`, only); n != 0 {
		t.Errorf("sessions outside the sole tenant = %d, want 0", n)
	}
}

// Two real tenants: no tenant is implied. Login and callback without
// tenant_id are a 400 that names the tenant_id parameter and discloses no
// tenant; an explicit tenant_id still works.
func TestOIDCLogin_MultiTenant_NoTenantID_Requires(t *testing.T) {
	h := newHarness(t)
	a, b := uuid.New(), uuid.New()
	h.insertTenant(t, a, "Alpha Org", "alpha-org", true)
	h.insertTenant(t, b, "Beta Org", "beta-org", false)

	idp := newFakeIdP(t)
	handler := h.oidcHandler(perTenantIdps{
		a: idpConfigFor(a, idp, fakeClientID),
		b: idpConfigFor(b, idp, "beta-client"),
	})

	login := getOIDCLogin(handler, "idp=primary")
	cbReq := httptest.NewRequest(http.MethodGet, "/auth/oidc/callback?code=test-code&state=x", nil)
	callback := httptest.NewRecorder()
	handler.OIDCCallback(callback, cbReq)

	for name, rec := range map[string]*httptest.ResponseRecorder{"login": login, "callback": callback} {
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400; body %s", name, rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, authapi.ErrMsgTenantIDRequired) {
			t.Errorf("%s: body %q lacks %q", name, body, authapi.ErrMsgTenantIDRequired)
		}
		for _, leak := range []string{a.String(), b.String(), "alpha-org", "beta-org", "Alpha Org", "Beta Org"} {
			if strings.Contains(body, leak) {
				t.Errorf("%s: 400 body discloses %q: %s", name, leak, body)
			}
		}
		if rec.Header().Get("Set-Cookie") != "" {
			t.Errorf("%s: 400 set cookies: %v", name, rec.Header().Values("Set-Cookie"))
		}
	}

	// The explicit tenant still works, against that tenant's IdP config.
	rec := getOIDCLogin(handler, "idp=primary&tenant_id="+b.String())
	if rec.Code != http.StatusFound {
		t.Fatalf("explicit tenant: status = %d, want 302; body %s", rec.Code, rec.Body.String())
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if got := loc.Query().Get("client_id"); got != "beta-client" {
		t.Errorf("explicit tenant: client_id = %q, want beta-client", got)
	}
}
