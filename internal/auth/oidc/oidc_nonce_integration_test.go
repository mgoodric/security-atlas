//go:build integration

// Integration tests for slice 365 — OIDC ID-token nonce generation +
// validation (closes slice 327 audit finding H-1).
//
// These tests stand up an in-process httptest.Server that serves the
// minimum OIDC discovery surface (well-known config + JWKS) and mint
// ID tokens locally so we can exercise both the happy path (nonce
// matches the flow cookie) and the H-1 attack path (an ID token whose
// `nonce` claim does NOT match the cookie — the case the v1 substrate
// previously failed to reject).
//
// Why an in-process fixture instead of a Postgres-backed integration:
// the surface under test is purely in `internal/auth/oidc/oidc.go` —
// no DB interaction. Postgres-backed tests would just add latency.
// The `//go:build integration` tag is preserved so this test runs in
// the same CI job (`Go · integration`) as the rest of the suite, per
// slice 069 testing discipline.
//
// Run via: just test-integration  (or `go test -tags=integration
// ./internal/auth/oidc/...`).
package oidc_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/auth/oidc"
)

// idpFixture is a minimal in-process OIDC IdP suitable for testing
// the RP-side flow. It signs ID tokens with a local RSA key and
// publishes the matching JWKS at /jwks so go-oidc's verifier can
// resolve the signature.
type idpFixture struct {
	srv        *httptest.Server
	signer     jose.Signer
	pub        *rsa.PublicKey
	kid        string
	clientID   string
	mintNonce  string // nonce claim minted into the next ID token
	mintSub    string
	mintEmail  string
	mintIssuer string // set lazily once srv.URL is known
}

func newIDPFixture(t *testing.T, clientID string) *idpFixture {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	kid := "test-kid-1"
	signerOpts := (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", kid)
	signer, err := jose.NewSigner(jose.SigningKey{
		Algorithm: jose.RS256,
		Key:       jose.JSONWebKey{Key: priv, KeyID: kid, Algorithm: string(jose.RS256), Use: "sig"},
	}, signerOpts)
	if err != nil {
		t.Fatalf("jose.NewSigner: %v", err)
	}
	f := &idpFixture{
		signer:    signer,
		pub:       &priv.PublicKey,
		kid:       kid,
		clientID:  clientID,
		mintSub:   "test-subject-001",
		mintEmail: "user@example.com",
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                f.mintIssuer,
			"authorization_endpoint":                f.mintIssuer + "/authorize",
			"token_endpoint":                        f.mintIssuer + "/token",
			"jwks_uri":                              f.mintIssuer + "/jwks",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		jwks := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
			{Key: f.pub, KeyID: f.kid, Algorithm: string(jose.RS256), Use: "sig"},
		}}
		_ = json.NewEncoder(w).Encode(jwks)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		// Mint an ID token with the nonce currently configured on the
		// fixture. Skip the access_token/refresh_token discipline —
		// the test only exercises ID-token verification.
		idToken := f.mintIDToken(t)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "access-token-stub",
			"token_type":   "Bearer",
			"id_token":     idToken,
			"expires_in":   3600,
		})
	})
	srv := httptest.NewServer(mux)
	f.srv = srv
	f.mintIssuer = srv.URL
	t.Cleanup(srv.Close)
	return f
}

func (f *idpFixture) mintIDToken(t *testing.T) string {
	t.Helper()
	claims := map[string]any{
		"iss":   f.mintIssuer,
		"sub":   f.mintSub,
		"aud":   f.clientID,
		"exp":   time.Now().Add(5 * time.Minute).Unix(),
		"iat":   time.Now().Unix(),
		"email": f.mintEmail,
		"name":  "Test User",
		"nonce": f.mintNonce,
	}
	tok, err := jwt.Signed(f.signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatalf("jwt.Signed: %v", err)
	}
	return tok
}

// stubResolver returns a fixed IdpConfig for the test tenant.
type stubResolver struct{ cfg oidc.IdpConfig }

func (s stubResolver) ResolveIdp(_ context.Context, _ uuid.UUID, _ string) (oidc.IdpConfig, error) {
	return s.cfg, nil
}

// extractCookie returns the cookie with the given name from a slice,
// or nil if not present.
func extractCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, c := range cookies {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// TestNonceMismatchRejected is the H-1 fix's RED test: an ID token
// whose `nonce` claim does NOT match the cookie value the RP set
// during BeginLogin must be rejected by HandleCallback.
func TestNonceMismatchRejected(t *testing.T) {
	tenantID := uuid.New()
	clientID := "test-client"
	idp := newIDPFixture(t, clientID)

	resolver := stubResolver{cfg: oidc.IdpConfig{
		ID:           uuid.New(),
		TenantID:     tenantID,
		Name:         "primary",
		IssuerURL:    idp.srv.URL,
		ClientID:     clientID,
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

	nonceCookie := extractCookie(loginResult.Cookies, oidc.NonceCookie)
	if nonceCookie == nil {
		t.Fatalf("BeginLogin did not return %q cookie (AC-1 regression)", oidc.NonceCookie)
	}

	// Mint an ID token with a DIFFERENT nonce claim than the cookie.
	idp.mintNonce = "attacker-supplied-or-stale-nonce"

	stateCookie := extractCookie(loginResult.Cookies, oidc.StateCookie)
	verifierCookie := extractCookie(loginResult.Cookies, oidc.VerifierCookie)
	idpCookie := extractCookie(loginResult.Cookies, oidc.IdpCookie)
	if stateCookie == nil || verifierCookie == nil || idpCookie == nil {
		t.Fatalf("missing flow cookies (state=%v verifier=%v idp=%v)",
			stateCookie != nil, verifierCookie != nil, idpCookie != nil)
	}

	req := buildCallbackRequest(t, stateCookie, verifierCookie, idpCookie, nonceCookie)

	_, err = a.HandleCallback(context.Background(), req, tenantID)
	if err == nil {
		t.Fatalf("HandleCallback accepted ID token with mismatched nonce — H-1 NOT fixed")
	}
	if !errors.Is(err, oidc.ErrNonceMismatch) {
		t.Fatalf("HandleCallback returned %v, want ErrNonceMismatch", err)
	}
}

// TestNonceMatchAccepted is the happy-path GREEN test: an ID token
// whose `nonce` claim matches the cookie value must be accepted.
func TestNonceMatchAccepted(t *testing.T) {
	tenantID := uuid.New()
	clientID := "test-client"
	idp := newIDPFixture(t, clientID)

	resolver := stubResolver{cfg: oidc.IdpConfig{
		ID:           uuid.New(),
		TenantID:     tenantID,
		Name:         "primary",
		IssuerURL:    idp.srv.URL,
		ClientID:     clientID,
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

	nonceCookie := extractCookie(loginResult.Cookies, oidc.NonceCookie)
	stateCookie := extractCookie(loginResult.Cookies, oidc.StateCookie)
	verifierCookie := extractCookie(loginResult.Cookies, oidc.VerifierCookie)
	idpCookie := extractCookie(loginResult.Cookies, oidc.IdpCookie)
	if nonceCookie == nil || stateCookie == nil || verifierCookie == nil || idpCookie == nil {
		t.Fatalf("missing flow cookies")
	}

	// Mint an ID token whose nonce matches the cookie — happy path.
	idp.mintNonce = nonceCookie.Value

	req := buildCallbackRequest(t, stateCookie, verifierCookie, idpCookie, nonceCookie)

	cb, err := a.HandleCallback(context.Background(), req, tenantID)
	if err != nil {
		t.Fatalf("HandleCallback: %v", err)
	}
	if cb.Email != idp.mintEmail {
		t.Fatalf("CallbackResult.Email = %q, want %q", cb.Email, idp.mintEmail)
	}
	if cb.Subject != idp.mintSub {
		t.Fatalf("CallbackResult.Subject = %q, want %q", cb.Subject, idp.mintSub)
	}
}

// TestStateStillEnforcedAlongsideNonce confirms P0-365-1 — the
// existing `state` CSRF guard remains active. A request with a
// matching nonce but a wrong state value MUST still be rejected
// with ErrStateMismatch. This locks in the ADDITIVE invariant
// (nonce does not replace state).
func TestStateStillEnforcedAlongsideNonce(t *testing.T) {
	tenantID := uuid.New()
	clientID := "test-client"
	idp := newIDPFixture(t, clientID)

	resolver := stubResolver{cfg: oidc.IdpConfig{
		ID:           uuid.New(),
		TenantID:     tenantID,
		Name:         "primary",
		IssuerURL:    idp.srv.URL,
		ClientID:     clientID,
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

	nonceCookie := extractCookie(loginResult.Cookies, oidc.NonceCookie)
	stateCookie := extractCookie(loginResult.Cookies, oidc.StateCookie)
	verifierCookie := extractCookie(loginResult.Cookies, oidc.VerifierCookie)
	idpCookie := extractCookie(loginResult.Cookies, oidc.IdpCookie)
	if nonceCookie == nil || stateCookie == nil || verifierCookie == nil || idpCookie == nil {
		t.Fatalf("missing flow cookies")
	}

	idp.mintNonce = nonceCookie.Value

	// Build callback with a DIFFERENT state query param than the cookie.
	// The redirect URL the IdP would send back includes ?state=<cookie>;
	// we tamper with it to simulate a CSRF attempt.
	rawURL := fmt.Sprintf("%s?code=test-code&state=%s",
		"http://localhost:9999/auth/oidc/callback",
		url.QueryEscape("forged-state-value"))
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}
	req.AddCookie(stateCookie)
	req.AddCookie(verifierCookie)
	req.AddCookie(idpCookie)
	req.AddCookie(nonceCookie)

	_, err = a.HandleCallback(context.Background(), req, tenantID)
	if !errors.Is(err, oidc.ErrStateMismatch) {
		t.Fatalf("HandleCallback returned %v, want ErrStateMismatch (state guard regression — P0-365-1)", err)
	}
}

// buildCallbackRequest constructs an http.Request mimicking what the
// IdP would redirect the browser to: a GET to the callback URL with
// ?code=...&state=<cookie value>, plus the four flow cookies on the
// request. The token endpoint at the fixture's httptest server is
// reached from HandleCallback's oa.Exchange call via go-oidc's
// discovered token endpoint, so this helper doesn't need a reference
// to the fixture itself.
func buildCallbackRequest(
	t *testing.T,
	stateCookie, verifierCookie, idpCookie, nonceCookie *http.Cookie,
) *http.Request {
	t.Helper()
	rawURL := fmt.Sprintf("%s?code=test-code&state=%s",
		"http://localhost:9999/auth/oidc/callback",
		url.QueryEscape(stateCookie.Value))
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}
	req.AddCookie(stateCookie)
	req.AddCookie(verifierCookie)
	req.AddCookie(idpCookie)
	req.AddCookie(nonceCookie)
	return req
}
