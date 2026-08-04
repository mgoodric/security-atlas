package oidc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type cacheStubResolver struct{ cfg IdpConfig }

func (s cacheStubResolver) ResolveIdp(_ context.Context, _ uuid.UUID, _ string) (IdpConfig, error) {
	return s.cfg, nil
}

func TestBeginLoginRefreshesExpiredProviderCacheAndRecovers(t *testing.T) {
	var issuer string
	available := true
	discoveryCalls := 0

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		discoveryCalls++
		if !available {
			http.Error(w, "idp unavailable", http.StatusServiceUnavailable)
			return
		}
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
	t.Cleanup(srv.Close)
	issuer = srv.URL

	tenantID := uuid.New()
	a := New(cacheStubResolver{cfg: IdpConfig{
		ID:           uuid.New(),
		TenantID:     tenantID,
		Name:         "primary",
		IssuerURL:    issuer,
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		RedirectURL:  "http://localhost:9999/auth/oidc/callback",
	}})
	now := time.Unix(1783200000, 0)
	a.now = func() time.Time { return now }
	a.providerCacheTTL = time.Second

	if _, err := a.BeginLogin(context.Background(), LoginInput{TenantID: tenantID, IdpName: "primary"}, false); err != nil {
		t.Fatalf("initial BeginLogin: %v", err)
	}
	if discoveryCalls != 1 {
		t.Fatalf("discoveryCalls after initial login = %d, want 1", discoveryCalls)
	}

	available = false
	now = now.Add(500 * time.Millisecond)
	if _, err := a.BeginLogin(context.Background(), LoginInput{TenantID: tenantID, IdpName: "primary"}, false); err != nil {
		t.Fatalf("BeginLogin before cache expiry should use cached provider: %v", err)
	}
	if discoveryCalls != 1 {
		t.Fatalf("discoveryCalls before cache expiry = %d, want 1", discoveryCalls)
	}

	now = now.Add(time.Second)
	_, err := a.BeginLogin(context.Background(), LoginInput{TenantID: tenantID, IdpName: "primary"}, false)
	if err == nil {
		t.Fatalf("BeginLogin after cache expiry succeeded while IdP was unavailable")
	}
	if !strings.Contains(err.Error(), "oidc: discover") {
		t.Fatalf("BeginLogin after cache expiry error = %v, want discovery error", err)
	}
	if discoveryCalls != 2 {
		t.Fatalf("discoveryCalls after expired-cache failure = %d, want 2", discoveryCalls)
	}

	available = true
	if _, err := a.BeginLogin(context.Background(), LoginInput{TenantID: tenantID, IdpName: "primary"}, false); err != nil {
		t.Fatalf("BeginLogin after IdP recovery: %v", err)
	}
	if discoveryCalls != 3 {
		t.Fatalf("discoveryCalls after recovery = %d, want 3", discoveryCalls)
	}
}
