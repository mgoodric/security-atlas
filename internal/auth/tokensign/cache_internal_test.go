package tokensign

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"testing"

	jose "github.com/go-jose/go-jose/v4"

	"github.com/mgoodric/security-atlas/internal/auth/keystore"
)

// BenchmarkSignerCachedVsUncached isolates the jose.Signer ACQUISITION
// step — the exact operation slice 381 F-OAUTH-2's cache eliminates.
//
// AC-7 originally asked for "cached Sign ns/op < 50% of uncached Sign
// ns/op". The full Sign path is dominated by the ES256 P-256 scalar
// multiplication (~20µs on commodity hardware), which BOTH the cached
// and uncached paths pay identically — caching jose.NewSigner cannot
// move the full-Sign total by 50% because the construction it removes
// (~230ns) is ~1% of the total. See slice 381 decisions log D2.
//
// So the meaningful, honest gate measures the thing the cache actually
// optimizes: signer acquisition. "Cached" = a sync.Map hit on
// cachedSigner; "Uncached" = jose.NewSigner per call. The cached path is
// an order of magnitude under 50% of the uncached path, locking the
// regression: a future change that drops the cache (back to per-call
// NewSigner) shows up immediately as a ~10x acquisition-cost blowup.
func BenchmarkSignerCachedVsUncached(b *testing.B) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		b.Fatalf("GenerateKey: %v", err)
	}
	sk := keystore.SigningKey{KeyID: "20260529T000000Z", Key: priv}

	b.Run("Cached", func(b *testing.B) {
		s := &Signer{}
		// Warm the cache so we measure the steady-state hit, not the
		// first build.
		if _, err := s.cachedSigner(sk); err != nil {
			b.Fatalf("warm cachedSigner: %v", err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			if _, err := s.cachedSigner(sk); err != nil {
				b.Fatalf("cachedSigner: %v", err)
			}
		}
	})

	b.Run("Uncached", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			if _, err := jose.NewSigner(jose.SigningKey{
				Algorithm: SignatureAlgorithm,
				Key: jose.JSONWebKey{
					Key:   sk.Key,
					KeyID: sk.KeyID,
				},
			}, (&jose.SignerOptions{}).WithType("JWT")); err != nil {
				b.Fatalf("NewSigner: %v", err)
			}
		}
	})
}
