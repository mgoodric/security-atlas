// clientauth_limit_test.go — #2041 unit coverage for the client-auth
// throttle on `/oauth/revoke` and `/oauth/introspect`.
//
// These drive the pre-DB arms with nil-pool stores: a request that
// presents a client_id but no secret is counted by the limiter, then
// oauthclient.Store.Verify short-circuits to ErrUnknownClient without
// touching the pool, so the endpoint answers 401 until the bucket
// empties and 429 after. The clock is pinned, so buckets never refill.
// The DB-backed burst (real argon2 verify) is in
// clientauth_limit_integration_test.go.

package oauth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/oauth"
	"github.com/mgoodric/security-atlas/internal/auth/keystore/fsstore"
	"github.com/mgoodric/security-atlas/internal/auth/oauthclient"
	"github.com/mgoodric/security-atlas/internal/auth/revocation"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
)

type limitCfg struct {
	clientRate, ipRate int
	clientIP           func(*http.Request) string
}

// newLimitUnitServer mounts both endpoints with nil-pool stores and
// the supplied limits.
func newLimitUnitServer(t *testing.T, c limitCfg) *httptest.Server {
	t.Helper()
	ks, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	signer := tokensign.New(ks)
	revoked := revocation.New(nil)
	clients := oauthclient.New(nil)
	h := oauth.New(ks, oauth.Config{Issuer: testIssuer})
	h.AttachRevocationEndpoint(oauth.NewRevocationEndpoint(signer, revoked, clients, oauth.RevocationEndpointConfig{
		Issuer:              testIssuer,
		ClientRatePerMinute: c.clientRate,
		IPRatePerMinute:     c.ipRate,
		ClientIP:            c.clientIP,
		Now:                 pinnedNow,
	}))
	h.AttachIntrospectionEndpoint(oauth.NewIntrospectionEndpoint(signer, revoked, clients, oauth.IntrospectionEndpointConfig{
		Issuer:              testIssuer,
		Now:                 pinnedNow,
		ClientRatePerMinute: c.clientRate,
		IPRatePerMinute:     c.ipRate,
		ClientIP:            c.clientIP,
	}))
	srv := httptest.NewServer(routerFor(h))
	t.Cleanup(srv.Close)
	return srv
}

func postLimited(t *testing.T, srvURL, path string, form url.Values, basic [2]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srvURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if basic[0] != "" || basic[1] != "" {
		req.SetBasicAuth(basic[0], basic[1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func assertRateLimited(t *testing.T, resp *http.Response, wantRetryAfter string) {
	t.Helper()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", resp.StatusCode)
	}
	if got := resp.Header.Get("Retry-After"); got != wantRetryAfter {
		t.Fatalf("Retry-After = %q, want %q", got, wantRetryAfter)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode 429 body: %v", err)
	}
	if body["error"] != "invalid_request" || body["error_description"] == "" {
		t.Fatalf("429 body = %v, want RFC 6749 error shape", body)
	}
}

// TestClientAuthLimit_PerClientBurstThrottled: on both endpoints, a
// burst against one client_id is throttled after the bucket empties,
// and a different client_id from the same source is unaffected.
func TestClientAuthLimit_PerClientBurstThrottled(t *testing.T) {
	t.Parallel()
	for _, path := range []string{oauth.PathIntrospect, oauth.PathRevoke} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			srv := newLimitUnitServer(t, limitCfg{clientRate: 2, ipRate: 100})
			form := url.Values{"token": {"t"}, "client_id": {"client-a"}}
			for i := 0; i < 2; i++ {
				if resp := postLimited(t, srv.URL, path, form, [2]string{}); resp.StatusCode != http.StatusUnauthorized {
					t.Fatalf("request %d: status = %d, want 401", i, resp.StatusCode)
				}
			}
			// 60/2 = 30s until the next token.
			assertRateLimited(t, postLimited(t, srv.URL, path, form, [2]string{}), "30")

			other := url.Values{"token": {"t"}, "client_id": {"client-b"}}
			if resp := postLimited(t, srv.URL, path, other, [2]string{}); resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("other client: status = %d, want 401 (not throttled)", resp.StatusCode)
			}
		})
	}
}

// TestClientAuthLimit_PerIPAcrossClients: one source cycling
// client_ids is throttled by the per-IP bucket, while another source
// is unaffected. The resolver is injected, standing in for
// auth.ClientIP.
func TestClientAuthLimit_PerIPAcrossClients(t *testing.T) {
	t.Parallel()
	srv := newLimitUnitServer(t, limitCfg{
		clientRate: 100,
		ipRate:     2,
		clientIP:   func(r *http.Request) string { return r.Header.Get("X-Test-Client") },
	})
	post := func(clientID, source string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+oauth.PathIntrospect,
			strings.NewReader(url.Values{"token": {"t"}, "client_id": {clientID}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Test-Client", source)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		return resp
	}
	for i, id := range []string{"c1", "c2"} {
		if resp := post(id, "198.51.100.7"); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("request %d: status = %d, want 401", i, resp.StatusCode)
		}
	}
	assertRateLimited(t, post("c3", "198.51.100.7"), "30")
	if resp := post("c4", "203.0.113.9"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("other source: status = %d, want 401 (not throttled)", resp.StatusCode)
	}
}

// TestClientAuthLimit_BasicPrecedenceCannotBypass: an empty Basic
// header falls through to the form client_id in the endpoints, so the
// limiter must count the form client_id too. Default (nil) ClientIP
// resolver covers the direct-peer fallback.
func TestClientAuthLimit_BasicPrecedenceCannotBypass(t *testing.T) {
	t.Parallel()
	srv := newLimitUnitServer(t, limitCfg{clientRate: 1})
	form := url.Values{"token": {"t"}, "client_id": {"client-a"}}
	if resp := postLimited(t, srv.URL, oauth.PathIntrospect, form, [2]string{"", "x"}); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("first: status = %d, want 401", resp.StatusCode)
	}
	assertRateLimited(t, postLimited(t, srv.URL, oauth.PathIntrospect, form, [2]string{"", "x"}), "60")

	// Basic carrying the same client_id shares its bucket.
	assertRateLimited(t, postLimited(t, srv.URL, oauth.PathIntrospect, url.Values{"token": {"t"}}, [2]string{"client-a", "s"}), "60")
}

// TestClientAuthLimit_NoClientIDNotCounted: a revoke request with no
// client credentials (the self-revocation bearer path, which never
// runs argon2) is not counted, so RFC 7009 semantics are unchanged.
func TestClientAuthLimit_NoClientIDNotCounted(t *testing.T) {
	t.Parallel()
	srv := newLimitUnitServer(t, limitCfg{clientRate: 1, ipRate: 1})
	for i := 0; i < 3; i++ {
		if resp := postLimited(t, srv.URL, oauth.PathRevoke, url.Values{"token": {"t"}}, [2]string{}); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("request %d: status = %d, want 401", i, resp.StatusCode)
		}
	}
}
