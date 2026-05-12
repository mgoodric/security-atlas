package bearer_test

import (
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/auth/bearer"
)

func TestGenerateProdPrefix(t *testing.T) {
	tok, err := bearer.Generate(bearer.PrefixProd)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.HasPrefix(tok, bearer.PrefixProd) {
		t.Fatalf("expected prefix %q, got %q", bearer.PrefixProd, tok)
	}
	if len(tok) != len(bearer.PrefixProd)+32 {
		t.Fatalf("expected len %d, got %d", len(bearer.PrefixProd)+32, len(tok))
	}
}

func TestGenerateTestPrefix(t *testing.T) {
	tok, err := bearer.Generate(bearer.PrefixTest)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.HasPrefix(tok, bearer.PrefixTest) {
		t.Fatalf("expected prefix %q, got %q", bearer.PrefixTest, tok)
	}
}

func TestGenerateRejectsBadPrefix(t *testing.T) {
	if _, err := bearer.Generate("nope_"); err == nil {
		t.Fatalf("expected error for bad prefix")
	}
}

func TestGenerateRandomness(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 64; i++ {
		tok, err := bearer.Generate(bearer.PrefixTest)
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if _, dup := seen[tok]; dup {
			t.Fatalf("duplicate bearer token %q across 64 generations", tok)
		}
		seen[tok] = struct{}{}
	}
}

func TestHasherDeterministic(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	h, err := bearer.NewHasher(key)
	if err != nil {
		t.Fatalf("NewHasher: %v", err)
	}
	a := h.Hash("test-token")
	b := h.Hash("test-token")
	if string(a) != string(b) {
		t.Fatalf("hash not deterministic")
	}
	c := h.Hash("test-token-2")
	if string(a) == string(c) {
		t.Fatalf("different inputs hashed to same output")
	}
	if len(a) != 32 {
		t.Fatalf("expected 32-byte hash, got %d", len(a))
	}
}

func TestHasherRejectsShortKey(t *testing.T) {
	short := make([]byte, 16)
	if _, err := bearer.NewHasher(short); err == nil {
		t.Fatalf("expected ErrHashKeyMissing for short key")
	}
}

func TestLast4(t *testing.T) {
	if got := bearer.Last4("atlas_test_abcdwxyz"); got != "wxyz" {
		t.Fatalf("Last4 = %q, want %q", got, "wxyz")
	}
}
