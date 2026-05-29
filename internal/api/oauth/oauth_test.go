package oauth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	jose "github.com/go-jose/go-jose/v4"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/oauth"
	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/keystore/fsstore"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
)

const testIssuer = "https://atlas.example.test"

func newHandler(t *testing.T) (*oauth.Handler, *tokensign.Signer) {
	t.Helper()
	store, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	h := oauth.New(store, oauth.Config{Issuer: testIssuer})
	return h, tokensign.New(store)
}

func newRouter(h *oauth.Handler) chi.Router {
	r := chi.NewRouter()
	h.Mount(r)
	return r
}

// ISC-20 + ISC-22 + ISC-23 + AC-7: JWKS endpoint returns a valid JWK
// Set with cache headers and no auth.
func TestJWKSHandlerReturnsKeys(t *testing.T) {
	t.Parallel()
	h, _ := newHandler(t)
	r := newRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/.well-known/jwks.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age=") {
		t.Fatalf("Cache-Control missing max-age: %q", cc)
	}
	var set jose.JSONWebKeySet
	if err := json.Unmarshal(w.Body.Bytes(), &set); err != nil {
		t.Fatalf("unmarshal JWK Set: %v", err)
	}
	if len(set.Keys) == 0 {
		t.Fatal("JWK Set is empty")
	}
	for _, k := range set.Keys {
		if k.IsPublic() == false {
			t.Fatal("JWK Set MUST contain only public keys")
		}
		if k.KeyID == "" {
			t.Fatal("JWK kid missing")
		}
	}
}

// Slice 366 AC-6: after the keystore rotates, the JWKS endpoint
// advertises BOTH the new active key AND the retained old key for the
// overlap window. Verifiers caching JWKS thus accept tokens signed with
// either key until the old key is pruned.
func TestJWKSPublishesBothKeysAfterRotation(t *testing.T) {
	t.Parallel()
	store, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	skBefore, _, _ := store.Get(context.Background())

	h := oauth.New(store, oauth.Config{Issuer: testIssuer})
	r := newRouter(h)

	// Rotate — the new key becomes active, the old key is retained.
	// KeyID granularity is one second; advance a full second so the new
	// KeyID sorts strictly after the original.
	time.Sleep(time.Until(time.Now().Truncate(time.Second).Add(time.Second)) + 5*time.Millisecond)
	if err := store.Rotate(context.Background()); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	skAfter, _, _ := store.Get(context.Background())
	if skAfter.KeyID == skBefore.KeyID {
		t.Fatalf("rotation did not change the active key")
	}

	req := httptest.NewRequest(http.MethodGet, "/.well-known/jwks.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var set jose.JSONWebKeySet
	if err := json.Unmarshal(w.Body.Bytes(), &set); err != nil {
		t.Fatalf("unmarshal JWK Set: %v", err)
	}
	if len(set.Keys) != 2 {
		t.Fatalf("expected 2 keys in JWKS after rotation, got %d", len(set.Keys))
	}
	kids := map[string]bool{}
	for _, k := range set.Keys {
		if !k.IsPublic() {
			t.Fatal("JWKS must contain only public keys after rotation")
		}
		kids[k.KeyID] = true
	}
	if !kids[skBefore.KeyID] {
		t.Fatalf("JWKS missing pre-rotation kid %q", skBefore.KeyID)
	}
	if !kids[skAfter.KeyID] {
		t.Fatalf("JWKS missing post-rotation kid %q", skAfter.KeyID)
	}
}

// ISC-21: multi-key support — JWKS handler always returns an array
// shape, even when only one key is present.
func TestJWKSHandlerReturnsArrayShape(t *testing.T) {
	t.Parallel()
	h, _ := newHandler(t)
	r := newRouter(h)
	req := httptest.NewRequest(http.MethodGet, "/.well-known/jwks.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Raw JSON must have `"keys": [...]`. A bare object shape would
	// silently break future-rotation interop.
	var probe struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &probe); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if probe.Keys == nil {
		t.Fatal("JWK Set must have `keys` array at top level")
	}
}

// ISC-38 + AC-11: JWKS round-trip. Sign a token with the keystore,
// fetch JWKS, verify the JWT using the published public key.
func TestJWKSRoundTripVerifiesSignedJWT(t *testing.T) {
	t.Parallel()
	h, signer := newHandler(t)
	r := newRouter(h)

	tenant := uuid.New()
	claims := jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    testIssuer,
			Subject:   "user:bob",
			Audience:  []string{testIssuer + "/api"},
			ExpiresAt: 9_999_999_999,
			IssuedAt:  1,
			ID:        "jti-roundtrip",
		},
		CurrentTenantID:  tenant,
		AvailableTenants: []uuid.UUID{tenant},
	}
	tok, err := signer.Sign(context.Background(), claims)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/.well-known/jwks.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var set jose.JSONWebKeySet
	if err := json.Unmarshal(w.Body.Bytes(), &set); err != nil {
		t.Fatalf("unmarshal JWK Set: %v", err)
	}
	kid, err := tokensign.PeekKeyID(tok)
	if err != nil {
		t.Fatalf("PeekKeyID: %v", err)
	}
	matches := set.Key(kid)
	if len(matches) == 0 {
		t.Fatalf("JWK Set missing kid %q", kid)
	}
	parsed, err := jose.ParseSigned(tok, []jose.SignatureAlgorithm{jose.ES256})
	if err != nil {
		t.Fatalf("ParseSigned: %v", err)
	}
	if _, err := parsed.Verify(matches[0]); err != nil {
		t.Fatalf("verify against published JWK: %v", err)
	}
}

// ISC-24..ISC-31 + AC-8 + AC-12 + P0-187-9: OIDC discovery doc
// advertises every required field with the locked atlas:* claim names
// and an empty grant_types_supported array (honest about what's
// stubbed).
func TestOIDCDiscoveryDocument(t *testing.T) {
	t.Parallel()
	h, _ := newHandler(t)
	r := newRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/.well-known/openid-configuration", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("unmarshal discovery: %v", err)
	}

	// Required fields per RFC 8414 §3 + OIDC Discovery §3.
	required := []string{
		"issuer", "jwks_uri", "token_endpoint", "authorization_endpoint",
		"revocation_endpoint", "introspection_endpoint",
		"grant_types_supported", "id_token_signing_alg_values_supported",
		"subject_types_supported", "scopes_supported", "claims_supported",
	}
	for _, k := range required {
		if _, ok := doc[k]; !ok {
			t.Fatalf("discovery doc missing required field %q", k)
		}
	}

	// issuer must match configured external URL
	if got := doc["issuer"]; got != testIssuer {
		t.Fatalf("issuer = %v, want %v", got, testIssuer)
	}
	// jwks_uri must be issuer-rooted
	if got := doc["jwks_uri"]; got != testIssuer+"/.well-known/jwks.json" {
		t.Fatalf("jwks_uri = %v, want %v", got, testIssuer+"/.well-known/jwks.json")
	}
	// grant_types_supported MUST be empty array (honesty — nothing live yet)
	gts, ok := doc["grant_types_supported"].([]any)
	if !ok {
		t.Fatalf("grant_types_supported wrong type: %T", doc["grant_types_supported"])
	}
	if len(gts) != 0 {
		t.Fatalf("grant_types_supported should be empty in slice 187, got %v", gts)
	}
	// id_token_signing_alg_values_supported must contain ES256
	algs, ok := doc["id_token_signing_alg_values_supported"].([]any)
	if !ok || len(algs) == 0 || algs[0] != "ES256" {
		t.Fatalf("id_token_signing_alg_values_supported = %v, want [\"ES256\"]", doc["id_token_signing_alg_values_supported"])
	}
	// claims_supported must include the locked atlas:* set
	claimsAny, ok := doc["claims_supported"].([]any)
	if !ok {
		t.Fatalf("claims_supported wrong type: %T", doc["claims_supported"])
	}
	have := map[string]bool{}
	for _, c := range claimsAny {
		have[c.(string)] = true
	}
	required = []string{
		"iss", "sub", "aud", "exp", "iat", "jti",
		"atlas:idp_issuer", "atlas:current_tenant_id",
		"atlas:available_tenants", "atlas:roles", "atlas:super_admin",
	}
	for _, k := range required {
		if !have[k] {
			t.Fatalf("claims_supported missing %q (have %v)", k, claimsAny)
		}
	}
}

// ISC-32..ISC-35 + AC-9 + P0-187-1: stub endpoints return 501 with the
// `slice_pending` error body pointing at the future slice that lands
// the real handler.
func TestOAuthStubsReturn501(t *testing.T) {
	t.Parallel()
	h, _ := newHandler(t)
	r := newRouter(h)

	cases := []struct {
		method, path, expectSlice string
	}{
		{http.MethodPost, "/oauth/token", "188"},
		{http.MethodGet, "/oauth/authorize", "189"},
		{http.MethodPost, "/oauth/revoke", "190"},
		{http.MethodPost, "/oauth/introspect", "190"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusNotImplemented {
				t.Fatalf("status = %d, want 501", w.Code)
			}
			var body struct {
				Error string `json:"error"`
				Slice string `json:"slice"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("unmarshal: %v (body=%q)", err, w.Body.String())
			}
			if body.Error != "slice_pending" {
				t.Fatalf("error = %q, want slice_pending", body.Error)
			}
			if body.Slice != tc.expectSlice {
				t.Fatalf("slice = %q, want %q", body.Slice, tc.expectSlice)
			}
		})
	}
}
