package scope_test

import (
	"testing"

	"github.com/mgoodric/security-atlas/internal/scope"
)

// TestCanonicalize ensures the canonical encoder is deterministic:
// the same dimension map produces the same bytes and hash regardless
// of insertion order. The DB UNIQUE on (tenant_id, dimensions_hash)
// relies on this property.
func TestCanonicalize_DeterministicAcrossKeyOrder(t *testing.T) {
	a := map[string]string{"environment": "prod", "business_unit": "platform"}
	b := map[string]string{"business_unit": "platform", "environment": "prod"}

	bytesA, hashA, err := scope.Canonicalize(a)
	if err != nil {
		t.Fatalf("Canonicalize a: %v", err)
	}
	bytesB, hashB, err := scope.Canonicalize(b)
	if err != nil {
		t.Fatalf("Canonicalize b: %v", err)
	}
	if string(bytesA) != string(bytesB) {
		t.Fatalf("bytes differ: %q vs %q", bytesA, bytesB)
	}
	if hashA != hashB {
		t.Fatalf("hashes differ: %q vs %q", hashA, hashB)
	}
	// Hash is sha256 hex — 64 chars.
	if len(hashA) != 64 {
		t.Fatalf("hash length = %d; want 64", len(hashA))
	}
}

func TestCanonicalize_DifferentDimensionsDifferentHash(t *testing.T) {
	a := map[string]string{"environment": "prod"}
	b := map[string]string{"environment": "staging"}
	_, hashA, _ := scope.Canonicalize(a)
	_, hashB, _ := scope.Canonicalize(b)
	if hashA == hashB {
		t.Fatal("hashes collided for different dimension values")
	}
}

func TestCanonicalize_RejectsEmpty(t *testing.T) {
	if _, _, err := scope.Canonicalize(map[string]string{}); err == nil {
		t.Fatal("expected error for empty dimensions")
	}
}
