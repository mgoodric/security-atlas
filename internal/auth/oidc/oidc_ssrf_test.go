package oidc_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/auth/oidc"
	"github.com/mgoodric/security-atlas/internal/netguard"
)

// #2534: every request to a tenant's IdP (discovery, JWKS, token exchange)
// goes through the netguard client, so an issuer that passed the save-time
// preflight cannot be steered at an internal address at sign-in.

// metadataURL is the cloud metadata address. DialControl refuses it before
// any connect, so a test that points an endpoint at it never touches the
// network.
const metadataURL = "http://169.254.169.254"

// ssrfIDP is a fake IdP whose discovery document advertises its own issuer
// (as the client addressed it) and caller-chosen token and JWKS endpoints.
// hits counts every request it serves.
type ssrfIDP struct {
	srv           *httptest.Server
	hits          atomic.Int64
	tokenEndpoint string
	jwksURI       string
}

func newSSRFIDP(t *testing.T) *ssrfIDP {
	t.Helper()
	idp := &ssrfIDP{}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		issuer := "http://" + r.Host
		token, jwks := idp.tokenEndpoint, idp.jwksURI
		if token == "" {
			token = issuer + "/token"
		}
		if jwks == "" {
			jwks = issuer + "/jwks"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                issuer,
			"authorization_endpoint":                issuer + "/authorize",
			"token_endpoint":                        token,
			"jwks_uri":                              jwks,
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		// An id_token whose claims pass go-oidc's issuer, audience and
		// expiry checks, so Verify goes on to fetch the JWKS. The
		// signature is never checked: the key fetch is refused first.
		seg := func(v any) string {
			b, _ := json.Marshal(v)
			return base64.RawURLEncoding.EncodeToString(b)
		}
		idTok := seg(map[string]any{"alg": "RS256", "kid": "k1"}) + "." +
			seg(map[string]any{
				"iss": "http://" + r.Host, "aud": "test-client", "sub": "u1",
				"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
			}) + "." + base64.RawURLEncoding.EncodeToString([]byte("sig"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at", "token_type": "Bearer", "expires_in": 60, "id_token": idTok,
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"keys":[]}`))
	})
	idp.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idp.hits.Add(1)
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(idp.srv.Close)
	return idp
}

// hostnameURL addresses srv by the name "localhost" rather than its literal
// IP: a hostname that resolves to loopback at sign-in, as a rebinding name
// would after passing preflight against a public answer.
func hostnameURL(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("split %q: %v", srv.URL, err)
	}
	return "http://localhost:" + port
}

func ssrfAuthenticator(issuer string, opts ...oidc.Option) (*oidc.Authenticator, uuid.UUID) {
	tenantID := uuid.New()
	return oidc.New(unitStubResolver{cfg: oidc.IdpConfig{
		ID: uuid.New(), TenantID: tenantID, Name: "primary", IssuerURL: issuer,
		ClientID: "test-client", ClientSecret: "test-secret",
		RedirectURL: "http://localhost:9999/auth/oidc/callback",
	}}, testStateKey, opts...), tenantID
}

func TestIssuerResolvingToLoopbackIsRefusedAtDial(t *testing.T) {
	idp := newSSRFIDP(t)
	a, tenantID := ssrfAuthenticator(hostnameURL(t, idp.srv)) // default: strict policy
	_, err := a.BeginLogin(context.Background(), oidc.LoginInput{TenantID: tenantID, IdpName: "primary"}, false)
	if !errors.Is(err, oidc.ErrProviderUnavailable) {
		t.Fatalf("BeginLogin err = %v, want ErrProviderUnavailable", err)
	}
	if n := idp.hits.Load(); n != 0 {
		t.Fatalf("IdP served %d requests; the dial should have been refused", n)
	}
}

func TestInternalIssuerReachableOnlyWithOptIn(t *testing.T) {
	idp := newSSRFIDP(t)
	for name, tc := range map[string]struct {
		opts []oidc.Option
		ok   bool
	}{
		"strict":  {opts: nil, ok: false},
		"private": {opts: []oidc.Option{oidc.WithIssuerPolicy(netguard.Policy{AllowPrivate: true})}, ok: false},
		"opt-in":  {opts: []oidc.Option{oidc.WithIssuerPolicy(netguard.Policy{AllowLoopback: true, AllowPrivate: true})}, ok: true},
	} {
		t.Run(name, func(t *testing.T) {
			a, tenantID := ssrfAuthenticator(hostnameURL(t, idp.srv), tc.opts...)
			_, err := a.BeginLogin(context.Background(), oidc.LoginInput{TenantID: tenantID, IdpName: "primary"}, false)
			if tc.ok && err != nil {
				t.Fatalf("BeginLogin with opt-in: %v", err)
			}
			if !tc.ok && !errors.Is(err, oidc.ErrProviderUnavailable) {
				t.Fatalf("BeginLogin err = %v, want ErrProviderUnavailable", err)
			}
		})
	}
}

func TestDiscoveryRedirectIsNotFollowed(t *testing.T) {
	internal := newSSRFIDP(t)
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.srv.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(front.Close)

	// Loopback is allowed here so the first hop is reachable; the point is
	// that the 307 is never followed.
	a, tenantID := ssrfAuthenticator(front.URL, oidc.WithIssuerPolicy(netguard.Policy{AllowLoopback: true}))
	_, err := a.BeginLogin(context.Background(), oidc.LoginInput{TenantID: tenantID, IdpName: "primary"}, false)
	if !errors.Is(err, oidc.ErrProviderUnavailable) {
		t.Fatalf("BeginLogin err = %v, want ErrProviderUnavailable", err)
	}
	if n := internal.hits.Load(); n != 0 {
		t.Fatalf("redirect target served %d requests; the redirect should not be followed", n)
	}
}

// callbackAfterLogin runs BeginLogin then HandleCallback against idp, with
// loopback allowed so the IdP itself is reachable.
func callbackAfterLogin(t *testing.T, idp *ssrfIDP) error {
	t.Helper()
	a, tenantID := ssrfAuthenticator(idp.srv.URL, oidc.WithIssuerPolicy(netguard.Policy{AllowLoopback: true}))
	res, err := a.BeginLogin(context.Background(), oidc.LoginInput{TenantID: tenantID, IdpName: "primary"}, false)
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	cookies := map[string]*http.Cookie{}
	for _, base := range allFlowCookies {
		cookies[base] = findCookie(res.Cookies, oidc.CookieName(base, false))
	}
	_, err = a.HandleCallback(context.Background(), callbackRequest(cookies[oidc.StateCookie].Value, cookies, false), tenantID, false)
	return err
}

func TestTokenExchangeGoesThroughGuard(t *testing.T) {
	idp := newSSRFIDP(t)
	idp.tokenEndpoint = metadataURL + "/token"
	err := callbackAfterLogin(t, idp)
	if err == nil || !strings.Contains(err.Error(), "ssrf guard") {
		t.Fatalf("HandleCallback err = %v, want the token exchange refused by the ssrf guard", err)
	}
}

func TestJWKSFetchGoesThroughGuard(t *testing.T) {
	idp := newSSRFIDP(t)
	idp.jwksURI = metadataURL + "/jwks"
	err := callbackAfterLogin(t, idp)
	if err == nil || !strings.Contains(err.Error(), "ssrf guard") {
		t.Fatalf("HandleCallback err = %v, want the JWKS fetch refused by the ssrf guard", err)
	}
}

func TestIssuerPolicyFromEnv(t *testing.T) {
	for _, tc := range []struct {
		val     string
		private bool
		wantErr bool
	}{
		{"", false, false},
		{"false", false, false},
		{"true", true, false},
		{"yes", false, true},
		{"TRUE", false, true},
	} {
		t.Run(tc.val, func(t *testing.T) {
			t.Setenv(oidc.AllowPrivateIssuersEnv, tc.val)
			p, err := oidc.IssuerPolicyFromEnv()
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if p.AllowPrivate != tc.private || p.AllowLoopback != tc.private {
				t.Fatalf("policy = %+v, want private=%v", p, tc.private)
			}
		})
	}
}
