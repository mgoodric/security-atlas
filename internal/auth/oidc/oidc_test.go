// Unit tests for the OIDC RP package — slice 365.
//
// These tests exercise BeginLogin's cookie + URL synthesis without
// the full ID-token verification fixture (which lives behind the
// `//go:build integration` tag in oidc_nonce_integration_test.go).
// A minimal discovery-only httptest.Server lets us drive BeginLogin
// end-to-end and assert on the LoginResult shape — the cost is one
// in-process HTTP round-trip per test, well inside unit-test budget.
package oidc_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/auth/oidc"
)

// newDiscoveryOnlyIDP serves just the well-known config endpoint and
// a stub JWKS — enough for go-oidc's NewProvider call to succeed,
// but does not mint tokens (BeginLogin never calls the token
// endpoint).
func newDiscoveryOnlyIDP(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var issuer string
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                issuer,
			"authorization_endpoint":                issuer + "/authorize",
			"token_endpoint":                        issuer + "/token",
			"jwks_uri":                              issuer + "/jwks",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"keys":[]}`))
	})
	srv := httptest.NewServer(mux)
	issuer = srv.URL
	t.Cleanup(srv.Close)
	return srv
}

type unitStubResolver struct{ cfg oidc.IdpConfig }

func (s unitStubResolver) ResolveIdp(_ context.Context, _ uuid.UUID, _ string) (oidc.IdpConfig, error) {
	return s.cfg, nil
}

// runBeginLogin spins up the discovery-only IdP, builds a stub
// resolver, and calls BeginLogin once. Returns the LoginResult so
// each test can assert on its own concern without repeating the
// 15-line setup.
func runBeginLogin(t *testing.T) oidc.LoginResult {
	t.Helper()
	idp := newDiscoveryOnlyIDP(t)
	tenantID := uuid.New()
	resolver := unitStubResolver{cfg: oidc.IdpConfig{
		ID:           uuid.New(),
		TenantID:     tenantID,
		Name:         "primary",
		IssuerURL:    idp.URL,
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		RedirectURL:  "http://localhost:9999/auth/oidc/callback",
	}}
	a := oidc.New(resolver)
	loginResult, err := a.BeginLogin(context.Background(), oidc.LoginInput{
		TenantID: tenantID,
		IdpName:  "primary",
	}, false)
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	return loginResult
}

// TestNonceCookieNameIsStable locks the cookie name string. Other
// repo code (web/proxy.ts, audit-log queries) may grep for the
// literal string; a silent rename would break them.
func TestNonceCookieNameIsStable(t *testing.T) {
	if oidc.NonceCookie != "atlas_oidc_nonce" {
		t.Fatalf("NonceCookie = %q, want %q (slice 365 ABI lock)", oidc.NonceCookie, "atlas_oidc_nonce")
	}
}

// TestFlowCookieMaxAgeIsTenMinutes locks the published constant the
// nonce cookie inherits from the existing state/verifier/idp cookies.
func TestFlowCookieMaxAgeIsTenMinutes(t *testing.T) {
	if oidc.FlowCookieMaxAge != 10*time.Minute {
		t.Fatalf("FlowCookieMaxAge = %v, want 10m", oidc.FlowCookieMaxAge)
	}
}

// TestErrNonceMismatchDistinctFromStateMismatch confirms the two
// sentinels are NOT aliased (slice 365 D1: separate sentinels for
// forensic clarity).
func TestErrNonceMismatchDistinctFromStateMismatch(t *testing.T) {
	if oidc.ErrNonceMismatch == oidc.ErrStateMismatch {
		t.Fatalf("ErrNonceMismatch == ErrStateMismatch — must be distinct sentinels (D1)")
	}
	if oidc.ErrNonceMismatch.Error() == oidc.ErrStateMismatch.Error() {
		t.Fatalf("ErrNonceMismatch and ErrStateMismatch have identical messages")
	}
}

// failingResolver returns a fixed error from ResolveIdp. Lets us
// exercise BeginLogin's early-return path (before any network call).
type failingResolver struct{ err error }

func (f failingResolver) ResolveIdp(_ context.Context, _ uuid.UUID, _ string) (oidc.IdpConfig, error) {
	return oidc.IdpConfig{}, f.err
}

// TestBeginLoginPropagatesResolverError confirms the resolver-error
// branch returns the error verbatim and emits no cookies.
func TestBeginLoginPropagatesResolverError(t *testing.T) {
	a := oidc.New(failingResolver{err: oidc.ErrUnknownIdp})
	_, err := a.BeginLogin(context.Background(), oidc.LoginInput{
		TenantID: uuid.New(),
		IdpName:  "missing",
	}, false)
	if err != oidc.ErrUnknownIdp {
		t.Fatalf("BeginLogin returned %v, want ErrUnknownIdp", err)
	}
}

// findCookie returns the named cookie or nil if absent.
func findCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, c := range cookies {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// TestBeginLoginEmitsNonceCookie satisfies slice 365 AC-7: BeginLogin
// must return a non-empty `atlas_oidc_nonce` cookie in LoginResult.
func TestBeginLoginEmitsNonceCookie(t *testing.T) {
	res := runBeginLogin(t)
	c := findCookie(res.Cookies, oidc.NonceCookie)
	if c == nil {
		t.Fatalf("BeginLogin did not return %q cookie (AC-1/AC-7 regression)", oidc.NonceCookie)
	}
	if c.Value == "" {
		t.Fatalf("%q cookie value is empty", oidc.NonceCookie)
	}
}

// TestBeginLoginNonceCookieMaxAgeMatchesConstant satisfies slice 365
// AC-7 second clause: the nonce cookie's MaxAge must match the
// existing FlowCookieMaxAge published constant.
func TestBeginLoginNonceCookieMaxAgeMatchesConstant(t *testing.T) {
	res := runBeginLogin(t)
	c := findCookie(res.Cookies, oidc.NonceCookie)
	if c == nil {
		t.Fatalf("nonce cookie not found")
	}
	want := int(oidc.FlowCookieMaxAge / time.Second)
	if c.MaxAge != want {
		t.Fatalf("nonce cookie MaxAge = %d, want %d (AC-7)", c.MaxAge, want)
	}
}

// TestBeginLoginAuthURLContainsNonceParam satisfies slice 365 AC-2:
// the authorize URL must include `nonce=<value>` matching the cookie.
func TestBeginLoginAuthURLContainsNonceParam(t *testing.T) {
	res := runBeginLogin(t)
	c := findCookie(res.Cookies, oidc.NonceCookie)
	if c == nil || c.Value == "" {
		t.Fatalf("nonce cookie missing or empty")
	}
	parsed, err := url.Parse(res.AuthURL)
	if err != nil {
		t.Fatalf("url.Parse(AuthURL): %v", err)
	}
	urlNonce := parsed.Query().Get("nonce")
	if urlNonce == "" {
		t.Fatalf("authorize URL missing nonce= parameter; have %q", res.AuthURL)
	}
	if urlNonce != c.Value {
		t.Fatalf("authorize URL nonce = %q, cookie nonce = %q — must match (AC-2)", urlNonce, c.Value)
	}
}

// TestBeginLoginPreservesStateAndPKCECookies is the P0-365-1 + P0-365-2
// regression guard: the nonce addition must NOT remove or alter the
// existing state, verifier, or idp cookies.
func TestBeginLoginPreservesStateAndPKCECookies(t *testing.T) {
	res := runBeginLogin(t)

	have := map[string]bool{}
	for _, c := range res.Cookies {
		if c.Value == "" {
			t.Fatalf("cookie %q has empty value", c.Name)
		}
		have[c.Name] = true
	}
	for _, want := range []string{oidc.StateCookie, oidc.VerifierCookie, oidc.IdpCookie, oidc.NonceCookie} {
		if !have[want] {
			t.Fatalf("BeginLogin did not emit cookie %q (P0-365-1/2 regression)", want)
		}
	}

	parsed, err := url.Parse(res.AuthURL)
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	q := parsed.Query()
	if q.Get("state") == "" {
		t.Fatalf("authorize URL missing state= param (P0-365-1)")
	}
	if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
		t.Fatalf("authorize URL missing PKCE params (P0-365-2)")
	}
}
