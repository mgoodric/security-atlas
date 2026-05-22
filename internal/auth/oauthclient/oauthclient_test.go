package oauthclient

import (
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/auth/password"
)

// TestGenerateSecretEntropyAndShape covers AC-19 (unit slice): the
// secret generator produces a stable-shape, sufficient-entropy
// plaintext on every call.
func TestGenerateSecretEntropyAndShape(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 16; i++ {
		s, err := generateSecret()
		if err != nil {
			t.Fatalf("generateSecret: %v", err)
		}
		// Base64-RawURL encoding of 32 bytes is exactly 43 chars
		// (no padding). Anything shorter signals an entropy bug.
		if len(s) != 43 {
			t.Errorf("generateSecret() length = %d, want 43", len(s))
		}
		if strings.ContainsAny(s, "+/=") {
			t.Errorf("generateSecret() contains URL-unsafe characters: %q", s)
		}
		if _, dup := seen[s]; dup {
			t.Errorf("generateSecret produced a duplicate after %d calls: %q", i, s)
		}
		seen[s] = struct{}{}
	}
}

// TestHashRoundtripAcceptsValidRejectsInvalid covers AC-19 (unit
// slice): the argon2id hash round-trips through password.Verify and
// rejects a wrong-plaintext attempt. This is the constant-time
// compare boundary that protects the Verify hot path.
func TestHashRoundtripAcceptsValidRejectsInvalid(t *testing.T) {
	secret, err := generateSecret()
	if err != nil {
		t.Fatalf("generateSecret: %v", err)
	}
	hash, err := password.Hash(secret)
	if err != nil {
		t.Fatalf("password.Hash: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("hash prefix = %q, want $argon2id$ prefix", hash[:min(20, len(hash))])
	}

	ok, err := password.Verify(secret, hash)
	if err != nil {
		t.Fatalf("password.Verify (correct): %v", err)
	}
	if !ok {
		t.Fatal("password.Verify rejected the correct secret")
	}

	// Single-byte mutation MUST fail.
	bad := secret + "x"
	ok, err = password.Verify(bad, hash)
	if err != nil {
		t.Fatalf("password.Verify (bad): %v", err)
	}
	if ok {
		t.Fatal("password.Verify accepted a mutated secret")
	}

	// Empty plaintext MUST fail (defends against an unset-form-field
	// attack on the token endpoint that supplies "" through to Verify).
	ok, err = password.Verify("", hash)
	if err != nil {
		t.Fatalf("password.Verify (empty): %v", err)
	}
	if ok {
		t.Fatal("password.Verify accepted an empty secret")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
