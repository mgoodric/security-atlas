package oauth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/pkg/sdk-go/oauth"
)

// fakeIssuer returns an httptest.Server that mints a deterministic
// JWT-shaped token on POST /oauth/token. The token value and
// expires_in are sourced from the supplied generators so tests can
// pin behavior across calls.
func fakeIssuer(t *testing.T, accessTokens []string, expiresIn int) (*httptest.Server, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if r.FormValue("grant_type") != "client_credentials" {
			http.Error(w, "grant_type", http.StatusBadRequest)
			return
		}
		tok := accessTokens[calls%len(accessTokens)]
		calls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": tok,
			"token_type":   "Bearer",
			"expires_in":   expiresIn,
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// TestNewClient_RequiresAllFields covers ErrInvalidConfig branches.
func TestNewClient_RequiresAllFields(t *testing.T) {
	cases := []struct {
		name string
		cfg  oauth.Config
	}{
		{"no client_id", oauth.Config{ClientSecret: "s", IssuerURL: "https://x"}},
		{"no client_secret", oauth.Config{ClientID: "i", IssuerURL: "https://x"}},
		{"no issuer", oauth.Config{ClientID: "i", ClientSecret: "s"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := oauth.NewClient(tc.cfg); err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}

// TestToken_CachesUntilExpiry exercises the cache: two Token() calls
// in a row return the same access_token without re-hitting the
// issuer (calls counter stays at 1).
func TestToken_CachesUntilExpiry(t *testing.T) {
	srv, calls := fakeIssuer(t, []string{"tok-1"}, 3600)
	now := time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC)
	c, err := oauth.NewClient(oauth.Config{
		ClientID:     "i",
		ClientSecret: "s",
		IssuerURL:    srv.URL,
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	tok1, err := c.Token(context.Background())
	if err != nil {
		t.Fatalf("first Token: %v", err)
	}
	tok2, err := c.Token(context.Background())
	if err != nil {
		t.Fatalf("second Token: %v", err)
	}
	if tok1 != tok2 {
		t.Fatalf("expected same cached token, got %q vs %q", tok1, tok2)
	}
	if *calls != 1 {
		t.Fatalf("expected 1 issuer call, got %d", *calls)
	}
}

// TestToken_RefreshesNearExpiry advances the clock to inside the
// 60-second leeway and asserts a second Token() acquires a fresh
// token from the issuer.
func TestToken_RefreshesNearExpiry(t *testing.T) {
	srv, calls := fakeIssuer(t, []string{"tok-1", "tok-2"}, 60)
	base := time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC)
	clock := base
	c, err := oauth.NewClient(oauth.Config{
		ClientID:     "i",
		ClientSecret: "s",
		IssuerURL:    srv.URL,
		Now:          func() time.Time { return clock },
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	tok1, err := c.Token(context.Background())
	if err != nil {
		t.Fatalf("first Token: %v", err)
	}
	if tok1 != "tok-1" {
		t.Fatalf("first token = %q, want tok-1", tok1)
	}
	// Advance to inside the 60s leeway (expires at +60s, leeway 60s
	// means refresh at +1s). +30s puts us in the refresh window.
	clock = base.Add(30 * time.Second)
	tok2, err := c.Token(context.Background())
	if err != nil {
		t.Fatalf("second Token: %v", err)
	}
	if tok2 != "tok-2" {
		t.Fatalf("expected refresh to tok-2, got %q", tok2)
	}
	if *calls != 2 {
		t.Fatalf("expected 2 issuer calls, got %d", *calls)
	}
}

// TestToken_Concurrent confirms the mutex serializes refreshes;
// 10 concurrent callers should produce at most a small bounded
// number of issuer calls (in practice 1 if all callers race during
// the same initial-acquire).
func TestToken_Concurrent(t *testing.T) {
	srv, calls := fakeIssuer(t, []string{"tok-1"}, 3600)
	now := time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC)
	c, err := oauth.NewClient(oauth.Config{
		ClientID:     "i",
		ClientSecret: "s",
		IssuerURL:    srv.URL,
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(10)
	for i := 0; i < 10; i++ {
		go func() {
			defer wg.Done()
			if _, err := c.Token(context.Background()); err != nil {
				t.Errorf("Token: %v", err)
			}
		}()
	}
	wg.Wait()
	if *calls != 1 {
		t.Fatalf("expected 1 issuer call under mutex, got %d", *calls)
	}
}

// TestToken_HonorsIssuerError surfaces non-200 issuer responses as
// errors rather than caching a bad token.
func TestToken_HonorsIssuerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	c, err := oauth.NewClient(oauth.Config{
		ClientID:     "i",
		ClientSecret: "s",
		IssuerURL:    srv.URL,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := c.Token(context.Background()); err == nil {
		t.Fatal("expected error on 401 issuer response")
	}
}
