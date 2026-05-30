// authorize_extra_test.go — slice 314 unit coverage for the slice-189
// `GET /oauth/authorize` request-validation gates and the
// `grant_type=authorization_code` redemption pre-DB branches.
//
// RFC: RFC 6749 §4.1.1 (authorization request) + RFC 7636 (PKCE) +
// RFC 6749 §5.2 (error responses). Load-bearing functions under test:
// AuthorizeEndpoint.ServeHTTP (the parameter-validation gates that run
// BEFORE the redirect-URI registry lookup) +
// TokenEndpoint.handleAuthorizationCode (the not-configured 503 + the
// missing-params 400 branches) + buildAtlasClaimsForUser (the
// user-mode claim projection).
//
// Scope discipline: the redirect-URI registry lookup, the session
// read, and the code insert/redeem all hit Postgres — those are
// covered by authorize_integration_test.go (now enrolled in CI per
// this slice). Every gate asserted here returns BEFORE the first DB
// call, so the nil-pool stores are never dereferenced. The stub
// Session/User resolvers exist only to satisfy the constructor; the
// gates under test fire before either is consulted.

package oauth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/oauth"
	"github.com/mgoodric/security-atlas/internal/auth/oauthclient"
	"github.com/mgoodric/security-atlas/internal/auth/oauthcode"
	"github.com/mgoodric/security-atlas/internal/auth/sessions"
)

// stubSessions / stubUsers satisfy the AuthorizeEndpoint constructor's
// interface requirements. They are NEVER called by the tests in this
// file — every gate fires before the session/user lookup — so their
// bodies return zero values. Defining them as stubs (not nil) is
// required because NewAuthorizeEndpoint panics on a nil dependency.
type stubSessions struct{}

func (stubSessions) Read(context.Context, uuid.UUID, string) (sessions.Session, error) {
	return sessions.Session{}, nil
}

type stubUsers struct{}

func (stubUsers) ResolveForOAuth(context.Context, uuid.UUID, uuid.UUID) (oauth.UserIdentity, error) {
	return oauth.UserIdentity{}, nil
}

// newAuthorizeUnitServer wires an AuthorizeEndpoint with nil-pool code
// + client stores and stub session/user resolvers. The
// parameter-validation gates run before any of these are touched.
func newAuthorizeUnitServer(t *testing.T) *httptest.Server {
	t.Helper()
	h, signer := newHandler(t)
	// A token endpoint must be wired for the authorize route to leave
	// the 501 stub (the discovery + mount logic gates authorize behind
	// the token endpoint).
	tokenEP := oauth.NewTokenEndpoint(signer, nil, oauth.TokenEndpointConfig{
		Issuer: testIssuer, RatePerMinute: 60, Now: pinnedNow,
	})
	h.AttachTokenEndpoint(tokenEP)
	authEP := oauth.NewAuthorizeEndpoint(oauth.AuthorizeEndpointConfig{
		Codes:    oauthcode.New(nil),
		Clients:  oauthclient.New(nil),
		Sessions: stubSessions{},
		Users:    stubUsers{},
		Issuer:   testIssuer,
		Now:      pinnedNow,
	})
	h.AttachAuthorizeEndpoint(authEP)
	srv := httptest.NewServer(routerFor(h))
	t.Cleanup(srv.Close)
	return srv
}

// getAuthorize issues a GET /oauth/authorize with the supplied query
// params and a client that does NOT follow redirects (so a 302 is
// observable rather than chased).
func getAuthorize(t *testing.T, srvURL string, q url.Values) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srvURL+oauth.PathAuthorize+"?"+q.Encode(), nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	return resp, buf[:n]
}

// validAuthorizeParams returns a fully-populated, otherwise-valid
// authorize query so each test can drop/mutate a single field to
// isolate the gate it targets.
func validAuthorizeParams() url.Values {
	_, challenge := pkceFixture("atlas-test-verifier-43-bytes-minimum-padding")
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", "atlas-cli")
	q.Set("redirect_uri", "https://app.example.test/callback")
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("tenant_id", uuid.NewString())
	q.Set("state", "xyz-state")
	return q
}

// TestAuthorize_RejectsNonCodeResponseType covers RFC 6749 §4.1.1 +
// P0-189-4: only response_type=code is supported. response_type=token
// (the Implicit grant) MUST be rejected 400 + unsupported_response_type.
func TestAuthorize_RejectsNonCodeResponseType(t *testing.T) {
	t.Parallel()
	srv := newAuthorizeUnitServer(t)
	q := validAuthorizeParams()
	q.Set("response_type", "token")
	resp, body := getAuthorize(t, srv.URL, q)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s; want 400", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "unsupported_response_type") {
		t.Errorf("body = %q, want unsupported_response_type", body)
	}
}

// TestAuthorize_RejectsPKCEPlain covers RFC 7636 + P0-189-1: only
// code_challenge_method=S256 is accepted. `plain` MUST be rejected
// 400 + invalid_request — this is the fail-secure PKCE posture.
func TestAuthorize_RejectsPKCEPlain(t *testing.T) {
	t.Parallel()
	srv := newAuthorizeUnitServer(t)
	q := validAuthorizeParams()
	q.Set("code_challenge_method", "plain")
	resp, body := getAuthorize(t, srv.URL, q)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s; want 400", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "invalid_request") {
		t.Errorf("body = %q, want invalid_request", body)
	}
}

// TestAuthorize_MissingRequiredParams covers RFC 6749 §4.1.1: each of
// client_id, redirect_uri, code_challenge, tenant_id is required, and
// a missing/invalid one is 400 + invalid_request BEFORE any redirect.
// P0-189-2: no code is ever issued for a malformed request.
func TestAuthorize_MissingRequiredParams(t *testing.T) {
	t.Parallel()
	srv := newAuthorizeUnitServer(t)

	cases := []struct {
		name   string
		mutate func(url.Values)
	}{
		{"missing-client_id", func(q url.Values) { q.Del("client_id") }},
		{"missing-redirect_uri", func(q url.Values) { q.Del("redirect_uri") }},
		{"missing-code_challenge", func(q url.Values) { q.Del("code_challenge") }},
		{"missing-tenant_id", func(q url.Values) { q.Del("tenant_id") }},
		{"invalid-tenant_id", func(q url.Values) { q.Set("tenant_id", "not-a-uuid") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := validAuthorizeParams()
			c.mutate(q)
			resp, body := getAuthorize(t, srv.URL, q)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s; want 400", resp.StatusCode, body)
			}
			if !strings.Contains(string(body), "invalid_request") {
				t.Errorf("body = %q, want invalid_request", body)
			}
		})
	}
}

// Note on the PKCE-method default (slice-189 D3 / P0-189-1): when
// code_challenge_method is OMITTED the handler defaults to S256 (NOT
// the RFC 7636 §4.3 `plain` default — a deliberate fail-secure
// deviation). Asserting that default's downstream effect requires the
// request to proceed PAST the method gate to the redirect-URI registry
// lookup, which needs a live Postgres; that path is covered in
// authorize_integration_test.go. The explicit-`plain`-rejection gate
// (TestAuthorize_RejectsPKCEPlain) is the unit-testable half.

// ===== authorization_code grant (RFC 6749 §4.1.3) pre-DB branches =====

// TestAuthCodeGrant_NotConfigured covers the defensive 503 branch: a
// TokenEndpoint with no auth-code store wired cannot redeem
// authorization codes — it returns 503 + server_error.
func TestAuthCodeGrant_NotConfigured(t *testing.T) {
	t.Parallel()
	// newTokenTestServer wires a TokenEndpoint WITHOUT an auth-code
	// store (no AttachAuthCodeStore), so the authorization_code grant
	// is not configured.
	srv, _, _ := newTokenTestServer(t)

	form := url.Values{}
	form.Set("grant_type", oauth.GrantTypeAuthorizationCode)
	form.Set("code", "atlas-auth-code")
	form.Set("code_verifier", "atlas-verifier")
	form.Set("redirect_uri", "https://app.example.test/callback")
	form.Set("client_id", "atlas-cli")
	resp, body := postForm(t, srv.URL, form)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s; want 503", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "server_error") {
		t.Errorf("body = %q, want server_error", body)
	}
}

// TestAuthCodeGrant_MissingParams covers RFC 6749 §4.1.3: code,
// code_verifier, redirect_uri, client_id are all required; a missing
// one is 400 + invalid_request. This gate runs before the DB consume,
// so a nil-pool auth-code store is never dereferenced.
func TestAuthCodeGrant_MissingParams(t *testing.T) {
	t.Parallel()
	srv := newAuthCodeServerWithStore(t)

	base := func() url.Values {
		f := url.Values{}
		f.Set("grant_type", oauth.GrantTypeAuthorizationCode)
		f.Set("code", "atlas-auth-code")
		f.Set("code_verifier", "atlas-verifier")
		f.Set("redirect_uri", "https://app.example.test/callback")
		f.Set("client_id", "atlas-cli")
		return f
	}
	for _, field := range []string{"code", "code_verifier", "redirect_uri", "client_id"} {
		t.Run("missing-"+field, func(t *testing.T) {
			f := base()
			f.Del(field)
			resp, body := postForm(t, srv.URL, f)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s; want 400", resp.StatusCode, body)
			}
			if !strings.Contains(string(body), "invalid_request") {
				t.Errorf("body = %q, want invalid_request", body)
			}
		})
	}
}

// newAuthCodeServerWithStore wires a TokenEndpoint WITH a nil-pool
// auth-code store so the authorization_code dispatch reaches the
// missing-params gate (which runs before the DB consume).
func newAuthCodeServerWithStore(t *testing.T) *httptest.Server {
	t.Helper()
	h, signer := newHandler(t)
	tokenEP := oauth.NewTokenEndpoint(signer, nil, oauth.TokenEndpointConfig{
		Issuer: testIssuer, RatePerMinute: 60, Now: pinnedNow,
	})
	tokenEP.AttachAuthCodeStore(oauthcode.New(nil))
	h.AttachTokenEndpoint(tokenEP)
	srv := httptest.NewServer(routerFor(h))
	t.Cleanup(srv.Close)
	return srv
}
