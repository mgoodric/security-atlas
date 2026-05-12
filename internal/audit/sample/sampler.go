// Package sample implements the deterministic sampling primitive that AC-2
// of slice 026 demands: given a population (a stable, sorted slice of
// evidence record ids), an N, and a seed string, return the same N members
// in the same order on every call.
//
// Design choices (constitutional commitments for downstream slices):
//
//   - RNG: math/rand/v2.NewChaCha8([32]byte). ChaCha8 is stdlib (since Go
//     1.22), cryptographically-derived, and reproducible given the same
//     32-byte seed. It is roughly 4x faster than the legacy math/rand
//     Source and produces a stronger statistical distribution. We do NOT
//     use crypto/rand because the auditor needs to REPLAY samples; a
//     non-deterministic RNG is the explicit anti-criterion.
//
//   - Seed derivation: SHA-256(seed_text) -> 32-byte ChaCha8 key. The
//     user-supplied seed is free-form text (e.g., "soc2-2026q2-test-1");
//     hashing gives us a uniform 32-byte key regardless of input length
//     while preserving determinism. The hash function is FIPS-blessed
//     (SHA-256) so a future federal-baseline deployment doesn't get
//     blocked on "what's that custom seed mangler".
//
//   - Selection algorithm: partial Fisher-Yates shuffle. We don't need a
//     full shuffle when N << len(population); the partial form does
//     exactly N swaps and stops, which is O(N) instead of O(P). The
//     output order is the shuffle order (NOT a re-sort), so callers see
//     a consistent "auditor's random sample" view.
//
//   - Pre-condition for determinism: callers MUST pass a sorted population
//     (the sqlc query ORDER BY id satisfies this). If the input order
//     varies, the output varies even with the same seed. The package
//     enforces nothing here -- it trusts the caller -- but the integration
//     test exercises this contract end-to-end.
package sample

import (
	"crypto/sha256"
	"errors"
	"math/rand/v2"

	"github.com/google/uuid"
)

// ErrEmptyPopulation is returned when Sample is called with zero records.
// AC-1 still ALLOWS creating a population with zero rows -- that's just an
// audit finding ("the auditor's target control has no evidence in the
// window"). Sample() is the path that surfaces it as an error so the
// handler can return 400.
var ErrEmptyPopulation = errors.New("sample: population is empty")

// ErrInvalidN is returned when n <= 0.
var ErrInvalidN = errors.New("sample: n must be positive")

// ErrEmptySeed is returned when seed is the empty string.
var ErrEmptySeed = errors.New("sample: seed must be non-empty")

// Sample picks N members from population using a ChaCha8 RNG keyed by
// SHA-256(seed). The returned slice has length min(n, len(population))
// and the members are in shuffle order (NOT input order).
//
// Determinism contract: Sample(pop, n, seed) is a pure function of its
// arguments. Two calls with the same args return identical slices.
//
// The population MUST be pre-sorted by the caller; if not, the output
// becomes non-deterministic across calls even with the same seed. See
// the integration test for the round-trip assertion.
func Sample(population []uuid.UUID, n int, seed string) ([]uuid.UUID, error) {
	if len(population) == 0 {
		return nil, ErrEmptyPopulation
	}
	if n <= 0 {
		return nil, ErrInvalidN
	}
	if seed == "" {
		return nil, ErrEmptySeed
	}

	// Clamp n to the population size. The caller asked for "up to N"; if
	// the population is smaller, return what's available rather than
	// failing. This is the auditor-friendly behavior -- they can decide
	// whether the smaller sample is enough.
	if n > len(population) {
		n = len(population)
	}

	rng := newRNG(seed)

	// Copy the input so the caller's slice is not mutated. Sampling is
	// read-only on the ledger AND read-only on the caller's view.
	out := make([]uuid.UUID, len(population))
	copy(out, population)

	// Partial Fisher-Yates: swap the next chosen item with position i,
	// for i in [0, n). After the loop, out[0:n] is a uniformly-random
	// sample without replacement.
	for i := 0; i < n; i++ {
		// pick j uniformly in [i, len(out))
		j := i + int(rng.Uint64()%uint64(len(out)-i))
		out[i], out[j] = out[j], out[i]
	}

	return out[:n], nil
}

// newRNG hashes the seed text into a 32-byte ChaCha8 key. The hash is
// SHA-256; the key is the raw digest. Same seed -> same key -> same RNG
// state -> same Fisher-Yates output.
func newRNG(seed string) *rand.ChaCha8 {
	var key [32]byte
	sum := sha256.Sum256([]byte(seed))
	copy(key[:], sum[:])
	return rand.NewChaCha8(key)
}
