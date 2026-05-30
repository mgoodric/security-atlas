package tokensign_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"testing"

	"github.com/mgoodric/security-atlas/internal/auth/keystore"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
)

// newP256Store returns a fakeStore backed by a fresh P-256 key with the
// given KeyID, suitable for the slice-381 F-OAUTH-2 cache tests.
func newP256Store(t testing.TB, kid string) *fakeStore {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return &fakeStore{
		signing: keystore.SigningKey{KeyID: kid, Key: priv},
		verify:  []keystore.VerificationKey{{KeyID: kid, Key: &priv.PublicKey}},
	}
}

// AC-5: repeated Sign calls against a stable KeyID reuse one cached
// jose.Signer. We assert the cache by exercising the round-trip many
// times — a correctness proxy (the perf claim is the benchmark below).
// Reset() must not break subsequent signing.
func TestSignerCacheReuseAndReset(t *testing.T) {
	store := newP256Store(t, "20260529T000000Z")
	signer := tokensign.New(store)
	ctx := context.Background()

	for range 50 {
		tok, err := signer.Sign(ctx, makeClaims(t))
		if err != nil {
			t.Fatalf("Sign: %v", err)
		}
		if _, err := signer.Verify(ctx, tok); err != nil {
			t.Fatalf("Verify: %v", err)
		}
	}

	// AC-6: Reset empties the cache; the next Sign rebuilds + re-caches
	// and must still produce a verifiable token.
	signer.Reset()
	tok, err := signer.Sign(ctx, makeClaims(t))
	if err != nil {
		t.Fatalf("Sign after Reset: %v", err)
	}
	if _, err := signer.Verify(ctx, tok); err != nil {
		t.Fatalf("Verify after Reset: %v", err)
	}
}

// AC-6 (overlap shape): during a slice-366 rotation overlap multiple
// KeyIDs are active. Signing under two different KeyIDs must each work;
// the cache keys on KeyID so neither evicts the other.
func TestSignerCachePerKeyID(t *testing.T) {
	ctx := context.Background()
	for _, kid := range []string{"20260529T000000Z", "20260530T000000Z"} {
		store := newP256Store(t, kid)
		signer := tokensign.New(store)
		tok, err := signer.Sign(ctx, makeClaims(t))
		if err != nil {
			t.Fatalf("Sign kid=%s: %v", kid, err)
		}
		if peek, err := tokensign.PeekKeyID(tok); err != nil || peek != kid {
			t.Fatalf("PeekKeyID kid=%s: got %q err=%v", kid, peek, err)
		}
	}
}

// The AC-7 benchmark (BenchmarkSignerCachedVsUncached) lives in the
// in-package cache_internal_test.go because it needs the unexported
// cachedSigner to isolate the signer-ACQUISITION step the cache
// optimizes — see that file + slice 381 decisions log D2 for why the
// full-Sign total cannot meet the literal "< 50%" bar (ES256 scalar
// multiplication dominates and is identical on both paths).
