//go:build integration

package oauth_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	jose "github.com/go-jose/go-jose/v4"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/oauth"
	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/keystore"
	"github.com/mgoodric/security-atlas/internal/auth/keystore/fsstore"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
)

// TestKeystoreFirstBootGeneratesAndRebootReuses covers AC-10 + ISC-36 +
// ISC-37: first boot creates a keypair; reopening the same directory
// returns the same key id and the same key bytes on disk.
func TestKeystoreFirstBootGeneratesAndRebootReuses(t *testing.T) {
	dir := t.TempDir()
	first, err := fsstore.Open(dir)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	sk1, vks1, err := first.Get(context.Background())
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}
	if sk1.Key == nil {
		t.Fatal("first-boot signing key nil")
	}
	if len(vks1) != 1 {
		t.Fatalf("first-boot verification keys count = %d, want 1", len(vks1))
	}

	second, err := fsstore.Open(dir)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	sk2, vks2, err := second.Get(context.Background())
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if sk1.KeyID != sk2.KeyID {
		t.Fatalf("reboot generated NEW keypair: %q vs %q", sk1.KeyID, sk2.KeyID)
	}
	if len(vks2) != 1 || vks2[0].KeyID != vks1[0].KeyID {
		t.Fatalf("reboot verification set drift: %v vs %v", vks1, vks2)
	}
}

// TestJWKSRoundTripEndToEnd covers AC-11 + ISC-38: sign a token with
// the live keystore, fetch JWKS through the HTTP handler, verify the
// signature using the published JWK.
func TestJWKSRoundTripEndToEnd(t *testing.T) {
	store, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	h := oauth.New(store, oauth.Config{Issuer: "https://atlas.example.test"})
	r := chi.NewRouter()
	h.Mount(r)
	srv := httptest.NewServer(r)
	defer srv.Close()

	signer := tokensign.New(store)
	tenant := uuid.New()
	tok, err := signer.Sign(context.Background(), jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "https://atlas.example.test",
			Subject:   "user:integration",
			Audience:  []string{"https://atlas.example.test/api"},
			ExpiresAt: 9_999_999_999,
			IssuedAt:  1,
			ID:        "jti-integration",
		},
		CurrentTenantID:  tenant,
		AvailableTenants: []uuid.UUID{tenant},
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	resp, err := http.Get(srv.URL + oauth.PathJWKS)
	if err != nil {
		t.Fatalf("GET JWKS: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var set jose.JSONWebKeySet
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		t.Fatalf("decode JWKS: %v", err)
	}

	kid, err := tokensign.PeekKeyID(tok)
	if err != nil {
		t.Fatalf("PeekKeyID: %v", err)
	}
	keys := set.Key(kid)
	if len(keys) == 0 {
		t.Fatalf("JWKS missing kid %q", kid)
	}
	parsed, err := jose.ParseSigned(tok, []jose.SignatureAlgorithm{jose.ES256})
	if err != nil {
		t.Fatalf("ParseSigned: %v", err)
	}
	if _, err := parsed.Verify(keys[0]); err != nil {
		t.Fatalf("verify against JWKS: %v", err)
	}
}

// TestOIDCDiscoveryRequiredFieldsEndToEnd covers AC-12 + ISC-39: the
// discovery document validates against the RFC 8414 + OIDC Discovery
// 1.0 required-field set.
func TestOIDCDiscoveryRequiredFieldsEndToEnd(t *testing.T) {
	store, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	h := oauth.New(store, oauth.Config{Issuer: "https://atlas.example.test"})
	r := chi.NewRouter()
	h.Mount(r)
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + oauth.PathDiscovery)
	if err != nil {
		t.Fatalf("GET discovery: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var doc map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// RFC 8414 §3 REQUIRED metadata
	for _, k := range []string{"issuer", "authorization_endpoint", "token_endpoint", "jwks_uri", "response_types_supported"} {
		if _, ok := doc[k]; !ok {
			t.Errorf("missing required RFC 8414 field %q", k)
		}
	}
	// OIDC Discovery 1.0 REQUIRED metadata (subset)
	for _, k := range []string{"subject_types_supported", "id_token_signing_alg_values_supported"} {
		if _, ok := doc[k]; !ok {
			t.Errorf("missing required OIDC Discovery field %q", k)
		}
	}
}

// TestKeystoreFileModeIs0600Integration covers AC-13 + ISC-40 +
// P0-187-5: integration-level assertion that the private key file on
// disk has mode 0600 after the keystore generates it.
func TestKeystoreFileModeIs0600Integration(t *testing.T) {
	dir := t.TempDir()
	store, err := fsstore.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	sk, _, err := store.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, sk.KeyID+".key"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("private key file mode = %o, want 0600", mode)
	}
}

// TestNoKeyMaterialLeaksToLogs covers AC-14 + ISC-41 + P0-187-6: route
// the standard structured logger through an in-memory buffer; exercise
// the keystore + sign/verify path; assert NO private-key bytes appear
// in any log line at any level.
func TestNoKeyMaterialLeaksToLogs(t *testing.T) {
	var logBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	dir := t.TempDir()
	store, err := fsstore.Open(dir)
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	signer := tokensign.New(store)
	tenant := uuid.New()
	tok, err := signer.Sign(context.Background(), jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "https://atlas.example.test",
			Subject:   "user:logleak",
			Audience:  []string{"https://atlas.example.test/api"},
			ExpiresAt: 9_999_999_999,
		},
		CurrentTenantID:  tenant,
		AvailableTenants: []uuid.UUID{tenant},
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := signer.Verify(context.Background(), tok); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// Read the persisted PKCS#8 PEM file and pull out the raw key bytes.
	sk, _, err := store.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	keyBytes, err := privateKeyDER(t, sk)
	if err != nil {
		t.Fatalf("privateKeyDER: %v", err)
	}

	logStr := logBuf.String()
	if strings.Contains(logStr, string(keyBytes)) {
		t.Fatal("private key DER bytes appeared in logs")
	}
	// The PEM file on disk MUST exist (sanity) but its contents must
	// not appear in logs.
	pemData, err := os.ReadFile(filepath.Join(dir, sk.KeyID+".key"))
	if err != nil {
		t.Fatalf("read PEM: %v", err)
	}
	if strings.Contains(logStr, string(pemData)) {
		t.Fatal("private key PEM appeared in logs")
	}
	// Spot-check: a sufficiently-long base64 chunk of the key shouldn't
	// appear either (defends against partial-line leakage).
	block, _ := pem.Decode(pemData)
	if block != nil && len(block.Bytes) >= 32 {
		// Take a 32-byte slice mid-key and ensure it never appears.
		chunk := block.Bytes[8:40]
		if bytes.Contains([]byte(logStr), chunk) {
			t.Fatal("32-byte slice of private key DER appeared in logs")
		}
	}
}

func privateKeyDER(t *testing.T, sk keystore.SigningKey) ([]byte, error) {
	t.Helper()
	return x509.MarshalPKCS8PrivateKey(sk.Key)
}
