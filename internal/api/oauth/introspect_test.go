// introspect_test.go — slice 314 unit coverage for the slice-190
// `POST /oauth/introspect` endpoint.
//
// RFC: RFC 7662 (OAuth 2.0 Token Introspection), §2.1 (request) +
// §2.2 (response) + §2.3 (error response). Load-bearing functions
// under test: IntrospectionEndpoint.ServeHTTP +
// authenticateInspector (the pre-DB request-validation + inspector-
// authentication branches).
//
// Scope discipline: these are the branches reachable WITHOUT a live
// Postgres — the malformed-request rejections (RFC 7662 §2.1) and the
// inspector-authentication-failure path (RFC 7662 §2.3 → 401
// invalid_client). The happy path (200 {active:true,...}) and the
// revoked/expired {active:false} branches need a DB-backed
// oauthclient.Store + revocation.Store and are exercised by
// revoke_introspect_integration_test.go (now enrolled in CI per this
// slice). oauthclient.Store.Verify returns ErrUnknownClient WITHOUT
// touching the pool when either credential is empty, so the
// auth-failure branch is reachable with a nil-pool store.

package oauth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/internal/api/oauth"
	"github.com/mgoodric/security-atlas/internal/auth/keystore/fsstore"
	"github.com/mgoodric/security-atlas/internal/auth/oauthclient"
	"github.com/mgoodric/security-atlas/internal/auth/revocation"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
)

// newIntrospectUnitServer wires an IntrospectionEndpoint with nil-pool
// stores. The pre-DB branches (content-type, missing token) and the
// no-credentials auth-failure branch run without ever dereferencing
// the pool; tests deliberately avoid the success path (which would
// require a real client row).
func newIntrospectUnitServer(t *testing.T) *httptest.Server {
	t.Helper()
	ks, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	signer := tokensign.New(ks)
	ep := oauth.NewIntrospectionEndpoint(
		signer,
		revocation.New(nil),
		oauthclient.New(nil),
		oauth.IntrospectionEndpointConfig{Issuer: testIssuer, Now: pinnedNow},
	)
	h := oauth.New(ks, oauth.Config{Issuer: testIssuer})
	h.AttachIntrospectionEndpoint(ep)
	srv := httptest.NewServer(routerFor(h))
	t.Cleanup(srv.Close)
	return srv
}

func postIntrospect(t *testing.T, srvURL string, form url.Values, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srvURL+oauth.PathIntrospect, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 8192)
	n, _ := resp.Body.Read(buf)
	return resp, buf[:n]
}

// TestIntrospect_RejectsNonFormContentType covers RFC 7662 §2.1: the
// introspection request body is application/x-www-form-urlencoded;
// any other content type is rejected 400 + invalid_request BEFORE any
// auth or DB work.
func TestIntrospect_RejectsNonFormContentType(t *testing.T) {
	t.Parallel()
	srv := newIntrospectUnitServer(t)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+oauth.PathIntrospect, strings.NewReader(`{"token":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["error"] != "invalid_request" {
		t.Errorf("error = %q, want invalid_request", body["error"])
	}
}

// TestIntrospect_MissingToken covers RFC 7662 §2.1: the `token`
// parameter is REQUIRED; its absence is 400 + invalid_request. This
// check runs before inspector authentication, so no client row is
// needed.
func TestIntrospect_MissingToken(t *testing.T) {
	t.Parallel()
	srv := newIntrospectUnitServer(t)

	resp, body := postIntrospect(t, srv.URL, url.Values{}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s; want 400", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "invalid_request") {
		t.Errorf("body = %q, want invalid_request", body)
	}
}

// TestIntrospect_NoInspectorCredentials covers RFC 7662 §2.3: the
// introspection endpoint requires the INSPECTOR to authenticate as a
// trusted client. A request with a `token` but no client credentials
// (neither Basic header nor form params) MUST be rejected 401 +
// invalid_client — NOT 200 {active:false}. The 401 distinguishes the
// inspector's own auth failure from the target token's invalidity
// (P0-190-5).
//
// oauthclient.Store.Verify short-circuits to ErrUnknownClient when a
// credential is empty, so this branch runs against the nil-pool store
// without a DB.
func TestIntrospect_NoInspectorCredentials(t *testing.T) {
	t.Parallel()
	srv := newIntrospectUnitServer(t)

	form := url.Values{}
	form.Set("token", "atlas-opaque-target-token-value")
	resp, body := postIntrospect(t, srv.URL, form, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s; want 401", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "invalid_client") {
		t.Errorf("body = %q, want invalid_client", body)
	}
}

// TestIntrospect_EmptyFormCredentials covers the authenticateInspector
// form-param branch: a present-but-empty client_id/client_secret pair
// is treated as no-credentials and rejected 401 + invalid_client.
func TestIntrospect_EmptyFormCredentials(t *testing.T) {
	t.Parallel()
	srv := newIntrospectUnitServer(t)

	form := url.Values{}
	form.Set("token", "atlas-opaque-target-token-value")
	form.Set("client_id", "")
	form.Set("client_secret", "")
	resp, body := postIntrospect(t, srv.URL, form, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s; want 401", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "invalid_client") {
		t.Errorf("body = %q, want invalid_client", body)
	}
}

// TestIntrospect_DiscoveryAdvertisesAuthMethods covers RFC 8414 §2:
// when the introspection endpoint is wired, the discovery document
// MUST advertise introspection_endpoint_auth_methods_supported. This
// asserts the slice-190 honest-advertising contract through the live
// handler rather than the function in isolation.
func TestIntrospect_DiscoveryAdvertisesAuthMethods(t *testing.T) {
	t.Parallel()
	srv := newIntrospectUnitServer(t)

	resp, err := http.Get(srv.URL + oauth.PathDiscovery)
	if err != nil {
		t.Fatalf("GET discovery: %v", err)
	}
	defer resp.Body.Close()
	var doc map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	methods, ok := doc["introspection_endpoint_auth_methods_supported"].([]any)
	if !ok {
		t.Fatalf("introspection_endpoint_auth_methods_supported missing/wrong type: %T",
			doc["introspection_endpoint_auth_methods_supported"])
	}
	want := map[string]bool{"client_secret_basic": false, "client_secret_post": false}
	for _, m := range methods {
		if s, _ := m.(string); s != "" {
			want[s] = true
		}
	}
	for m, seen := range want {
		if !seen {
			t.Errorf("discovery introspection auth methods missing %q (have %v)", m, methods)
		}
	}
}

// ensure the time import stays used even if the harness evolves.
var _ = time.Second
