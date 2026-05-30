// wellknown_test.go — slice 314 unit coverage for the discovery
// metadata surface that the slice-187/190/191 Attach* calls reshape.
//
// RFC: RFC 8414 (OAuth 2.0 Authorization Server Metadata) §2 +
// §3 (the `.well-known/oauth-authorization-server` /
// `.well-known/openid-configuration` document) + RFC 8628 §4 (the
// `device_authorization_endpoint` metadata field). Load-bearing
// functions under test: Handler.rebuildDiscovery via the public
// Attach* seams + discoveryDocument's conditional fields +
// serveDiscovery.
//
// The base-document required-field + claims_supported assertions live
// in oauth_test.go (TestOIDCDiscoveryDocument). This file covers the
// conditional fields that only appear once specific endpoints are
// attached — the honest-advertising discipline (slice 190 / 191).

package oauth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/mgoodric/security-atlas/internal/api/oauth"
	"github.com/mgoodric/security-atlas/internal/auth/oauthclient"
)

// fetchDiscovery serves the discovery doc from a fresh router built on
// the supplied handler and returns the decoded map.
func fetchDiscovery(t *testing.T, h *oauth.Handler) map[string]any {
	t.Helper()
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest(http.MethodGet, oauth.PathDiscovery, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("discovery status = %d, want 200", w.Code)
	}
	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("discovery unmarshal: %v", err)
	}
	return doc
}

// TestDiscovery_BareHandlerOmitsConditionalFields covers RFC 8414 §2:
// a Handler with NO endpoints attached MUST NOT advertise the
// revocation/introspection auth-method lists nor the
// device_authorization_endpoint — advertising endpoints we don't serve
// would be dishonest to clients (slice-187 P0-187-9). The endpoint
// URLs themselves are still present (the stub routes exist) but the
// auth-method + device fields are gated.
func TestDiscovery_BareHandlerOmitsConditionalFields(t *testing.T) {
	t.Parallel()
	h, _ := newHandler(t)
	doc := fetchDiscovery(t, h)

	for _, gated := range []string{
		"revocation_endpoint_auth_methods_supported",
		"introspection_endpoint_auth_methods_supported",
		"device_authorization_endpoint",
	} {
		if _, present := doc[gated]; present {
			t.Errorf("bare handler advertises gated field %q: %v", gated, doc[gated])
		}
	}
	// grant_types_supported is an empty array (nothing live yet).
	gts, ok := doc["grant_types_supported"].([]any)
	if !ok {
		t.Fatalf("grant_types_supported wrong type: %T", doc["grant_types_supported"])
	}
	if len(gts) != 0 {
		t.Errorf("bare handler grant_types_supported = %v, want empty", gts)
	}
}

// TestDiscovery_EndpointURLsAreIssuerRooted covers RFC 8414 §2: every
// advertised endpoint URL MUST be absolute and issuer-rooted so a
// client constructing requests off the metadata hits the right host.
func TestDiscovery_EndpointURLsAreIssuerRooted(t *testing.T) {
	t.Parallel()
	h, _ := newHandler(t)
	doc := fetchDiscovery(t, h)

	cases := map[string]string{
		"jwks_uri":               testIssuer + "/.well-known/jwks.json",
		"token_endpoint":         testIssuer + "/oauth/token",
		"authorization_endpoint": testIssuer + "/oauth/authorize",
		"revocation_endpoint":    testIssuer + "/oauth/revoke",
		"introspection_endpoint": testIssuer + "/oauth/introspect",
	}
	for field, want := range cases {
		if got := doc[field]; got != want {
			t.Errorf("%s = %v, want %v", field, got, want)
		}
	}
}

// TestDiscovery_DeviceAuthorizationAdvertisedWhenAttached covers RFC
// 8628 §4: attaching the device-authorization endpoint AND a token
// endpoint MUST surface the device_authorization_endpoint metadata
// field AND add the device-code grant URN to grant_types_supported.
// The grant only lights up when BOTH are present (honest advertising).
func TestDiscovery_DeviceAuthorizationAdvertisedWhenAttached(t *testing.T) {
	t.Parallel()
	h, signer := newHandler(t)

	// A token endpoint (nil clients acceptable per slice 188) so the
	// device-code grant can appear in grant_types_supported.
	tokenEP := oauth.NewTokenEndpoint(signer, nil, oauth.TokenEndpointConfig{
		Issuer:        testIssuer,
		RatePerMinute: 60,
		Now:           pinnedNow,
	})
	h.AttachTokenEndpoint(tokenEP)

	// Before attaching the device endpoint: the URN must NOT appear and
	// the metadata field must be absent.
	pre := fetchDiscovery(t, h)
	if _, present := pre["device_authorization_endpoint"]; present {
		t.Fatal("device_authorization_endpoint advertised before device endpoint attached")
	}

	// Attach the device-authorization endpoint (nil-pool stores are
	// fine — discovery never touches them).
	devEP := oauth.NewDeviceAuthorizationEndpoint(
		oauthclient.New(nil),
		oauth.NewDeviceCodeStore(nil),
		oauth.DeviceAuthorizationConfig{Issuer: testIssuer, Now: pinnedNow},
	)
	h.AttachDeviceAuthorizationEndpoint(devEP)

	post := fetchDiscovery(t, h)
	if got := post["device_authorization_endpoint"]; got != testIssuer+oauth.PathDeviceAuthorization {
		t.Errorf("device_authorization_endpoint = %v, want %v", got, testIssuer+oauth.PathDeviceAuthorization)
	}
	gts, _ := post["grant_types_supported"].([]any)
	found := false
	for _, g := range gts {
		if g == oauth.GrantTypeDeviceCode {
			found = true
		}
	}
	if !found {
		t.Errorf("grant_types_supported missing device-code URN after attach: %v", gts)
	}
}

// TestDiscovery_PKCEMethodsAndResponseTypes covers the locked PKCE +
// response-type contract: code_challenge_methods_supported is exactly
// ["S256"] (P0-189-1 — plain rejected) and response_types_supported is
// exactly ["code"] (P0-189-4 — implicit rejected).
func TestDiscovery_PKCEMethodsAndResponseTypes(t *testing.T) {
	t.Parallel()
	h, _ := newHandler(t)
	doc := fetchDiscovery(t, h)

	cms, _ := doc["code_challenge_methods_supported"].([]any)
	if len(cms) != 1 || cms[0] != "S256" {
		t.Errorf("code_challenge_methods_supported = %v, want [S256]", cms)
	}
	rts, _ := doc["response_types_supported"].([]any)
	if len(rts) != 1 || rts[0] != "code" {
		t.Errorf("response_types_supported = %v, want [code]", rts)
	}
}
