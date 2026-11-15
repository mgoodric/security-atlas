//go:build integration

// clientauth_limit_integration_test.go — #2041: a burst of real
// client-authenticated requests against /oauth/introspect and
// /oauth/revoke is throttled per client_id before the argon2id verify,
// another client is unaffected, and an unknown client_id is throttled
// the same way as a registered one.

package oauth_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/oauth"
	"github.com/mgoodric/security-atlas/internal/auth/keystore/fsstore"
	"github.com/mgoodric/security-atlas/internal/auth/oauthclient"
	"github.com/mgoodric/security-atlas/internal/auth/revocation"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
)

const limitBurst = 3

// newLimitedRevokeIntrospectServer wires both endpoints against real
// stores with a per-client bucket of limitBurst and a pinned clock.
func newLimitedRevokeIntrospectServer(t *testing.T) (*httptest.Server, *tokensign.Signer, *oauthclient.Store) {
	t.Helper()
	pool := openTokenIntegrationPool(t)
	ks, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	signer := tokensign.New(ks)
	clients := oauthclient.New(pool)
	revoked := revocation.New(pool)
	h := oauth.New(ks, oauth.Config{Issuer: testIssuer})
	h.AttachRevocationEndpoint(oauth.NewRevocationEndpoint(signer, revoked, clients, oauth.RevocationEndpointConfig{
		Issuer:              testIssuer,
		ClientRatePerMinute: limitBurst,
		IPRatePerMinute:     100,
		Now:                 pinnedNow,
	}))
	h.AttachIntrospectionEndpoint(oauth.NewIntrospectionEndpoint(signer, revoked, clients, oauth.IntrospectionEndpointConfig{
		Issuer:              testIssuer,
		Now:                 pinnedNow,
		ClientRatePerMinute: limitBurst,
		IPRatePerMinute:     100,
	}))
	srv := httptest.NewServer(newRouter(h))
	t.Cleanup(srv.Close)
	return srv, signer, clients
}

func TestIntegrationClientAuthLimit_BurstThrottledOtherClientsUnaffected(t *testing.T) {
	srv, signer, clients := newLimitedRevokeIntrospectServer(t)
	idA, secretA := issueClientForTest(t, clients)
	idB, secretB := issueClientForTest(t, clients) // each endpoint owns its buckets
	tok, _ := mintJWTForTest(t, signer, "user:limit")

	for _, path := range []string{oauth.PathIntrospect, oauth.PathRevoke} {
		t.Run(path, func(t *testing.T) {
			formFor := func(id, secret string) url.Values {
				return url.Values{"token": {tok}, "client_id": {id}, "client_secret": {secret}}
			}
			for i := 0; i < limitBurst; i++ {
				resp, body := postFormAt(t, srv.URL+path, formFor(idA, secretA), nil)
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("burst %d: status = %d body=%s, want 200", i, resp.StatusCode, body)
				}
			}
			resp, body := postFormAt(t, srv.URL+path, formFor(idA, secretA), nil)
			if resp.StatusCode != http.StatusTooManyRequests {
				t.Fatalf("over limit: status = %d body=%s, want 429", resp.StatusCode, body)
			}
			if resp.Header.Get("Retry-After") != "20" {
				t.Fatalf("Retry-After = %q, want 20", resp.Header.Get("Retry-After"))
			}

			// Client B is unaffected by A's exhaustion.
			resp, body = postFormAt(t, srv.URL+path, formFor(idB, secretB), nil)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("other client: status = %d body=%s, want 200", resp.StatusCode, body)
			}
		})
	}
}

// TestIntegrationClientAuthLimit_UnknownClientThrottledAlike: an
// unregistered client_id gets 401 until its bucket empties, then the
// same 429 a registered client gets, so the limiter does not reveal
// which client_ids exist.
func TestIntegrationClientAuthLimit_UnknownClientThrottledAlike(t *testing.T) {
	srv, _, _ := newLimitedRevokeIntrospectServer(t)
	form := url.Values{"token": {"t"}, "client_id": {"no-such-client"}, "client_secret": {"guess"}}
	for i := 0; i < limitBurst; i++ {
		resp, body := postFormAt(t, srv.URL+oauth.PathIntrospect, form, nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("burst %d: status = %d body=%s, want 401", i, resp.StatusCode, body)
		}
	}
	resp, body := postFormAt(t, srv.URL+oauth.PathIntrospect, form, nil)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("over limit: status = %d body=%s, want 429", resp.StatusCode, body)
	}
}
