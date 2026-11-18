//go:build integration

// Handler-level integration tests for #2034: what an admin saves through
// PATCH /v1/admin/sso is what an OIDC sign-in uses. Each test saves the
// config through the admin handler, then signs in through
// /auth/oidc/login + /auth/oidc/callback against a fake IdP, with the
// production idpconfig.Resolver reading the row from Postgres.
//
// Run via: go test -tags=integration ./internal/api/auth/...
// (needs DATABASE_URL = BYPASSRLS migrate role, DATABASE_URL_APP = atlas_app).
package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/adminsso"
	authapi "github.com/mgoodric/security-atlas/internal/api/auth"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/auth/idpconfig"
	"github.com/mgoodric/security-atlas/internal/auth/oidc"
	"github.com/mgoodric/security-atlas/internal/auth/sessions"
	"github.com/mgoodric/security-atlas/internal/auth/users"
)

var testSecretKey = bytes.Repeat([]byte{0x5a}, 32)

func testSecretCrypter(t *testing.T) *idpconfig.SecretCrypter {
	t.Helper()
	c, err := idpconfig.NewSecretCrypter(testSecretKey)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// ssoSettings is the PATCH /v1/admin/sso body.
type ssoSettings struct {
	IssuerURL           string   `json:"issuer_url"`
	ClientID            string   `json:"client_id"`
	ClientSecret        string   `json:"client_secret,omitempty"`
	RedirectURL         string   `json:"redirect_url"`
	AllowedEmailDomains []string `json:"allowed_email_domains"`
	TrustUnverified     bool     `json:"trust_unverified_email_for_link"`
}

func settingsFor(idp *fakeIdP, secret string) ssoSettings {
	return ssoSettings{
		IssuerURL:    idp.srv.URL,
		ClientID:     fakeClientID,
		ClientSecret: secret,
		RedirectURL:  "http://localhost:9999/auth/oidc/callback",
	}
}

// saveSSO PATCHes /v1/admin/sso as an admin of tenant and returns the
// response.
func (h harness) saveSSO(t *testing.T, tenant uuid.UUID, s ssoSettings) *httptest.ResponseRecorder {
	t.Helper()
	t.Cleanup(func() {
		_, _ = h.admin.Exec(context.Background(), `DELETE FROM oidc_idp_configs WHERE tenant_id = $1`, tenant)
	})
	sso := adminsso.New(h.app).
		WithPreflightOptions(adminsso.PreflightOptions{AllowPrivateIPs: true}).
		WithSecretCrypter(testSecretCrypter(t))
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID: "key_test_sso", TenantID: tenant.String(), IsAdmin: true, UserID: "user-sso-test",
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Patch("/v1/admin/sso", sso.Patch)
	body, _ := json.Marshal(s)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/v1/admin/sso", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH /v1/admin/sso = %d: %s", rec.Code, rec.Body.String())
	}
	return rec
}

func (h harness) dbResolverAuthn(t *testing.T) *oidc.Authenticator {
	t.Helper()
	return oidc.New(idpconfig.NewResolver(h.app, testSecretCrypter(t), true), testStateKey, loopbackIdP)
}

// signIn runs GET /auth/oidc/login then GET /auth/oidc/callback for tenant
// through authn. It returns the login response when the login is refused,
// otherwise the callback response.
func (h harness) signIn(t *testing.T, authn *oidc.Authenticator, idp *fakeIdP, tenant uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	handler := authapi.New(authn, users.NewStoreWithAuthPool(h.app, h.admin), sessions.NewStore(h.app, time.Hour), false, nil)

	login := httptest.NewRecorder()
	handler.OIDCLogin(login, httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/auth/oidc/login?tenant_id=%s&idp=primary", tenant), nil))
	if login.Code != http.StatusFound {
		return login
	}
	var state string
	cookies := login.Result().Cookies()
	for _, c := range cookies {
		switch c.Name {
		case oidc.StateCookie:
			state = c.Value
		case oidc.NonceCookie:
			idp.nonce = c.Value
		}
	}
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf(
		"/auth/oidc/callback?tenant_id=%s&code=test-code&state=%s", tenant, url.QueryEscape(state)), nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	handler.OIDCCallback(rec, req)
	return rec
}

// captureLogs sends slog's default output to a buffer for the test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// The saved issuer, client and secret are what the sign-in uses, and the
// secret is in no response, no log line and not in the row as plaintext.
func TestSavedSSOConfig_AppliesAtSignIn(t *testing.T) {
	h := newHarness(t)
	logs := captureLogs(t)
	tenant := uuid.New()
	h.insertTenant(t, tenant, "SSO Org", "sso-org", false)
	idp := newFakeIdP(t)
	idp.emailVerified = true
	secret := "saved-secret-" + uuid.NewString()

	patch := h.saveSSO(t, tenant, settingsFor(idp, secret))
	rec := h.signIn(t, h.dbResolverAuthn(t), idp, tenant)
	if rec.Code != http.StatusFound || cookieNamed(rec, sessions.CookieName) == nil {
		t.Fatalf("sign-in = %d, want 302 with a session; body %s", rec.Code, rec.Body.String())
	}
	if idp.gotClientID != fakeClientID || idp.gotClientSecret != secret {
		t.Errorf("token request client = (%q, %q), want the saved (%q, <secret>)", idp.gotClientID, idp.gotClientSecret, fakeClientID)
	}
	if n := h.count(t, `SELECT COUNT(*) FROM users WHERE tenant_id = $1 AND idp_issuer = $2`, tenant, idp.srv.URL); n != 1 {
		t.Errorf("users signed in through the saved issuer = %d, want 1", n)
	}

	for where, text := range map[string]string{
		"PATCH response":    patch.Body.String(),
		"callback response": rec.Body.String() + fmt.Sprint(rec.Header()),
		"logs":              logs.String(),
	} {
		if strings.Contains(text, secret) {
			t.Errorf("%s carries the client secret", where)
		}
	}
	if n := h.count(t, `SELECT COUNT(*) FROM oidc_idp_configs WHERE tenant_id = $1 AND position($2::bytea in client_secret_enc) > 0`,
		tenant, []byte(secret)); n != 0 {
		t.Error("client_secret_enc stores the plaintext secret")
	}
}

// allowed_email_domains applies: an address outside the list is a 403 with
// no user and no session; an empty list admits any verified address.
func TestSavedSSOConfig_EnforcesAllowedDomains(t *testing.T) {
	cases := []struct {
		name    string
		domains []string
		email   string
		want    int
	}{
		{name: "listed domain", domains: []string{"example.com"}, email: "founder@EXAMPLE.com", want: http.StatusFound},
		{name: "lookalike domain", domains: []string{"example.com"}, email: "founder@evil-example.com", want: http.StatusForbidden},
		{name: "subdomain", domains: []string{"example.com"}, email: "founder@sub.example.com", want: http.StatusForbidden},
		{name: "empty list", domains: nil, email: "founder@anywhere.example", want: http.StatusFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			tenant := uuid.New()
			h.insertTenant(t, tenant, "SSO Org", "sso-org", false)
			idp := newFakeIdP(t)
			idp.emailVerified = true
			idp.email = tc.email
			s := settingsFor(idp, "s")
			s.AllowedEmailDomains = tc.domains
			h.saveSSO(t, tenant, s)

			rec := h.signIn(t, h.dbResolverAuthn(t), idp, tenant)
			if rec.Code != tc.want {
				t.Fatalf("sign-in = %d, want %d; body %s", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want == http.StatusForbidden {
				if strings.Contains(rec.Body.String(), tc.email) {
					t.Errorf("refusal names the email: %s", rec.Body.String())
				}
				if n := h.count(t, `SELECT COUNT(*) FROM users WHERE tenant_id = $1`, tenant); n != 0 {
					t.Errorf("users = %d, want 0", n)
				}
				if cookieNamed(rec, sessions.CookieName) != nil {
					t.Error("refused sign-in set a session cookie")
				}
			}
		})
	}
}

// The saved trust_unverified_email_for_link flag decides whether a sign-in
// whose token omits email_verified links to a SCIM-provisioned user.
func TestSavedSSOConfig_TrustFlagGovernsAbsentClaimLinking(t *testing.T) {
	for _, trust := range []bool{false, true} {
		t.Run(fmt.Sprintf("trust=%t", trust), func(t *testing.T) {
			h := newHarness(t)
			tenant := uuid.New()
			h.insertTenant(t, tenant, "SCIM Org", "scim-org", true)
			userID := h.insertUser(t, tenant, seedUser{})
			idp := newFakeIdP(t) // email_verified absent
			s := settingsFor(idp, "s")
			s.TrustUnverified = trust
			h.saveSSO(t, tenant, s)

			rec := h.signIn(t, h.dbResolverAuthn(t), idp, tenant)
			want := http.StatusUnauthorized
			if trust {
				want = http.StatusFound
			}
			if rec.Code != want {
				t.Fatalf("sign-in = %d, want %d; body %s", rec.Code, want, rec.Body.String())
			}
			iss, _ := h.idpTuple(t, userID)
			if linked := iss == idp.srv.URL; linked != trust {
				t.Errorf("SCIM row linked = %t, want %t", linked, trust)
			}
		})
	}
}

// A tenant with no saved config cannot sign in, even when another tenant
// has one for the same IdP: no fallback across tenants.
func TestSavedSSOConfig_OtherTenantsConfigRefused(t *testing.T) {
	h := newHarness(t)
	a, b := uuid.New(), uuid.New()
	h.insertTenant(t, a, "Org A", "org-a", false)
	h.insertTenant(t, b, "Org B", "org-b", false)
	idp := newFakeIdP(t)
	idp.emailVerified = true
	h.saveSSO(t, b, settingsFor(idp, "b-secret"))

	rec := h.signIn(t, h.dbResolverAuthn(t), idp, a)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "unknown IdP") {
		t.Fatalf("tenant A login = %d %s, want 400 unknown IdP", rec.Code, rec.Body.String())
	}
	if idp.gotClientSecret != "" {
		t.Error("the IdP was called with tenant B's client")
	}
	if n := h.count(t, `SELECT COUNT(*) FROM users`); n != 0 {
		t.Errorf("users = %d, want 0", n)
	}
}

// An issuer the tenant never saved is refused even when the flow names the
// right IdP config: the saved issuer is the only one a token may carry.
func TestSavedSSOConfig_UnknownIssuerRefused(t *testing.T) {
	h := newHarness(t)
	tenant := uuid.New()
	h.insertTenant(t, tenant, "SSO Org", "sso-org", false)
	saved, rogue := newFakeIdP(t), newFakeIdP(t)
	saved.emailVerified, rogue.emailVerified = true, true
	h.saveSSO(t, tenant, settingsFor(saved, "s"))

	// The rogue IdP answers the saved IdP's /token: its token is signed by
	// another key and names another issuer.
	authn := h.dbResolverAuthn(t)
	handler := authapi.New(authn, users.NewStoreWithAuthPool(h.app, h.admin), sessions.NewStore(h.app, time.Hour), false, nil)
	login := httptest.NewRecorder()
	handler.OIDCLogin(login, httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/auth/oidc/login?tenant_id=%s&idp=primary", tenant), nil))
	if login.Code != http.StatusFound {
		t.Fatalf("login = %d", login.Code)
	}
	var state string
	for _, c := range login.Result().Cookies() {
		switch c.Name {
		case oidc.StateCookie:
			state = c.Value
		case oidc.NonceCookie:
			rogue.nonce = c.Value
		}
	}
	saved.srv.Config.Handler = rogue.srv.Config.Handler
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf(
		"/auth/oidc/callback?tenant_id=%s&code=c&state=%s", tenant, url.QueryEscape(state)), nil)
	for _, c := range login.Result().Cookies() {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	handler.OIDCCallback(rec, req)
	if rec.Code == http.StatusFound || cookieNamed(rec, sessions.CookieName) != nil {
		t.Fatalf("token from an unsaved issuer signed in: %d", rec.Code)
	}
	if n := h.count(t, `SELECT COUNT(*) FROM users`); n != 0 {
		t.Errorf("users = %d, want 0", n)
	}
}

// ATLAS_OIDC_LOCAL_MODE=true wires LocalModeResolver: a saved config is not
// used and OIDC sign-in answers unknown IdP.
func TestSavedSSOConfig_LocalModeIgnoresIt(t *testing.T) {
	h := newHarness(t)
	tenant := uuid.New()
	h.insertTenant(t, tenant, "SSO Org", "sso-org", false)
	idp := newFakeIdP(t)
	idp.emailVerified = true
	h.saveSSO(t, tenant, settingsFor(idp, "s"))

	authn := oidc.New(idpconfig.LocalModeResolver{}, testStateKey, loopbackIdP)
	rec := h.signIn(t, authn, idp, tenant)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "unknown IdP") {
		t.Fatalf("local-mode login = %d %s, want 400 unknown IdP", rec.Code, rec.Body.String())
	}
}
