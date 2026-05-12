package sample_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/audit/sample"
)

// fixedPopulation returns N uuids derived deterministically from a seed
// index, so the test population is itself reproducible.
func fixedPopulation(n int) []uuid.UUID {
	out := make([]uuid.UUID, n)
	for i := range out {
		// Stable UUIDs via byte-pattern: first byte = high index, second
		// = low. uuid.UUID is a [16]byte underneath.
		var u uuid.UUID
		u[0] = byte(i >> 8)
		u[1] = byte(i & 0xff)
		out[i] = u
	}
	return out
}

func TestSample_DeterministicAcrossCalls(t *testing.T) {
	// AC-2 load-bearing assertion: same seed + same population -> identical
	// N IDs in the same order, two independent calls.
	pop := fixedPopulation(100)
	a, err := sample.Sample(pop, 10, "test-seed-001")
	if err != nil {
		t.Fatalf("Sample first call: %v", err)
	}
	b, err := sample.Sample(pop, 10, "test-seed-001")
	if err != nil {
		t.Fatalf("Sample second call: %v", err)
	}
	if len(a) != len(b) {
		t.Fatalf("length mismatch: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("position %d differs: %v vs %v", i, a[i], b[i])
		}
	}
}

func TestSample_DifferentSeedsDiffer(t *testing.T) {
	pop := fixedPopulation(100)
	a, _ := sample.Sample(pop, 10, "seed-A")
	b, _ := sample.Sample(pop, 10, "seed-B")
	if len(a) != 10 || len(b) != 10 {
		t.Fatalf("expected length 10 each, got %d / %d", len(a), len(b))
	}
	same := 0
	for i := range a {
		if a[i] == b[i] {
			same++
		}
	}
	// Two different seeds should produce different orderings for 10 of 100;
	// allowing some collisions but not the full slice.
	if same == 10 {
		t.Fatalf("two seeds produced identical samples (RNG is broken)")
	}
}

func TestSample_NoDuplicates(t *testing.T) {
	pop := fixedPopulation(100)
	out, err := sample.Sample(pop, 50, "no-dup-seed")
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	seen := make(map[uuid.UUID]struct{}, len(out))
	for _, id := range out {
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate id in sample: %v", id)
		}
		seen[id] = struct{}{}
	}
}

func TestSample_NClampedToPopulation(t *testing.T) {
	pop := fixedPopulation(5)
	out, err := sample.Sample(pop, 50, "clamp-seed")
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	if len(out) != 5 {
		t.Fatalf("expected n clamped to population size 5, got %d", len(out))
	}
}

func TestSample_RejectsEmptyPopulation(t *testing.T) {
	_, err := sample.Sample(nil, 10, "seed")
	if err == nil {
		t.Fatalf("expected ErrEmptyPopulation, got nil")
	}
}

func TestSample_RejectsZeroN(t *testing.T) {
	pop := fixedPopulation(10)
	_, err := sample.Sample(pop, 0, "seed")
	if err == nil {
		t.Fatalf("expected ErrInvalidN, got nil")
	}
}

func TestSample_RejectsEmptySeed(t *testing.T) {
	pop := fixedPopulation(10)
	_, err := sample.Sample(pop, 5, "")
	if err == nil {
		t.Fatalf("expected ErrEmptySeed, got nil")
	}
}

func TestSample_DoesNotMutateInput(t *testing.T) {
	// The anti-criterion is "samples don't mutate the ledger"; one stop
	// further is "samples don't mutate the in-memory population either".
	pop := fixedPopulation(20)
	snap := make([]uuid.UUID, len(pop))
	copy(snap, pop)

	_, _ = sample.Sample(pop, 10, "no-mutate")

	for i := range pop {
		if pop[i] != snap[i] {
			t.Fatalf("Sample mutated input at index %d: %v -> %v",
				i, snap[i], pop[i])
		}
	}
}
