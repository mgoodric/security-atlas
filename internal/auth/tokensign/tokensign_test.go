package tokensign_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/keystore"
	"github.com/mgoodric/security-atlas/internal/auth/keystore/fsstore"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
)

func makeClaims(t testing.TB) jwt.AtlasClaims {
	t.Helper()
	now := time.Now().UTC()
	tenant := uuid.New()
	return jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "https://atlas.example.test",
			Subject:   "user:alice",
			Audience:  []string{"https://atlas.example.test/api"},
			ExpiresAt: now.Add(1 * time.Hour).Unix(),
			IssuedAt:  now.Unix(),
			ID:        "jti-tokensign-test",
		},
		CurrentTenantID:  tenant,
		AvailableTenants: []uuid.UUID{tenant},
		Roles:            map[uuid.UUID][]string{tenant: {"admin"}},
	}
}

// ISC-16 + ISC-17 + ISC-18 + AC-4: sign-then-verify round-trips against
// the keystore's verification keys.
func TestSignVerifyRoundTrip(t *testing.T) {
	store, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	signer := tokensign.New(store)
	claims := makeClaims(t)
	tok, err := signer.Sign(context.Background(), claims)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if tok == "" {
		t.Fatal("empty token returned")
	}
	out, err := signer.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if out.Subject != claims.Subject {
		t.Fatalf("subject round-trip mismatch: got %q want %q", out.Subject, claims.Subject)
	}
	if out.CurrentTenantID != claims.CurrentTenantID {
		t.Fatalf("tenant round-trip mismatch: got %v want %v", out.CurrentTenantID, claims.CurrentTenantID)
	}
}

// ISC-15: signature mismatch rejected. Build a token with one key, then
// try to verify it with a different keystore (the signing key is not in
// the verification set).
func TestVerifyRejectsSignatureMismatch(t *testing.T) {
	// Signer keystore.
	storeA, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("storeA: %v", err)
	}
	signerA := tokensign.New(storeA)
	tok, err := signerA.Sign(context.Background(), makeClaims(t))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	// Verifier keystore with an unrelated key.
	storeB, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("storeB: %v", err)
	}
	signerB := tokensign.New(storeB)
	if _, err := signerB.Verify(context.Background(), tok); err == nil {
		t.Fatal("expected signature-mismatch failure when verifier doesn't hold the signing key")
	}
}

// Token mutated mid-flight rejected.
func TestVerifyRejectsTampering(t *testing.T) {
	store, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	signer := tokensign.New(store)
	tok, err := signer.Sign(context.Background(), makeClaims(t))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	// Flip the last byte before the signature segment to corrupt the
	// payload while keeping the JWS structure intact.
	mut := []byte(tok)
	if mut[len(mut)/2] == 'A' {
		mut[len(mut)/2] = 'B'
	} else {
		mut[len(mut)/2] = 'A'
	}
	if _, err := signer.Verify(context.Background(), string(mut)); err == nil {
		t.Fatal("expected tampered token to be rejected")
	}
}

// The signer sets the JWS `kid` header from the keystore's active key
// so JWKS-based verification works without out-of-band hints.
func TestSignSetsKeyIDHeader(t *testing.T) {
	store, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	sk, _, err := store.Get(context.Background())
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	signer := tokensign.New(store)
	tok, err := signer.Sign(context.Background(), makeClaims(t))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	kid, err := tokensign.PeekKeyID(tok)
	if err != nil {
		t.Fatalf("PeekKeyID: %v", err)
	}
	if kid != sk.KeyID {
		t.Fatalf("kid header %q does not match signing key id %q", kid, sk.KeyID)
	}
}

// Sanity-check ECDSA private keys aren't accepted for non-P-256 curves
// by the signer — a future regression where someone wires RS256 or
// P-384 by accident should fail fast.
func TestSignRejectsWrongCurve(t *testing.T) {
	// Build an ad-hoc P-384 key and feed it via a fake store.
	priv, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	store := &fakeStore{
		signing: keystore.SigningKey{KeyID: "wrong-curve", Key: priv},
		verify:  []keystore.VerificationKey{{KeyID: "wrong-curve", Key: &priv.PublicKey}},
	}
	signer := tokensign.New(store)
	if _, err := signer.Sign(context.Background(), makeClaims(t)); err == nil {
		t.Fatal("expected Sign to reject non-P-256 key")
	}
}

type fakeStore struct {
	signing keystore.SigningKey
	verify  []keystore.VerificationKey
}

func (f *fakeStore) Get(context.Context) (keystore.SigningKey, []keystore.VerificationKey, error) {
	return f.signing, f.verify, nil
}
func (f *fakeStore) Rotate(context.Context) error { return keystore.ErrRotateUnsupported }
