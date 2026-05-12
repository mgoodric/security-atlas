package password_test

import (
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/auth/password"
)

func TestHashEncodesArgon2id(t *testing.T) {
	enc, err := password.Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if !strings.HasPrefix(enc, "$argon2id$v=") {
		t.Fatalf("expected argon2id prefix, got %q", enc)
	}
}

func TestVerifyAcceptsMatch(t *testing.T) {
	enc, err := password.Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	ok, err := password.Verify("correct horse battery staple", enc)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok {
		t.Fatalf("Verify returned false for matching plaintext")
	}
}

func TestVerifyRejectsMismatch(t *testing.T) {
	enc, err := password.Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	ok, err := password.Verify("wrong password", enc)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if ok {
		t.Fatalf("Verify returned true for mismatched plaintext")
	}
}

func TestVerifyRejectsInvalidEncoded(t *testing.T) {
	if _, err := password.Verify("anything", "not-an-argon2id-hash"); err == nil {
		t.Fatalf("expected error for invalid encoded hash")
	}
}

func TestHashRandomSalt(t *testing.T) {
	// Two hashes of the same plaintext MUST differ (random salt).
	a, _ := password.Hash("same-password")
	b, _ := password.Hash("same-password")
	if a == b {
		t.Fatalf("hashes collided across two Hash calls (salt not random?)")
	}
}
