// Package bearer generates and hashes API bearer tokens.
//
// Tokens are 160-bit random secrets, base32-encoded, with a fixed prefix
// (`atlas_` for production, `atlas_test_` for tests). They are hashed at
// rest with HMAC-SHA256 keyed by a server secret loaded from the
// `BEARER_HASH_KEY` env var.
//
// See docs/adr/0002-bearer-token-storage.md for the threat-model rationale.
package bearer

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"os"
	"strings"
)

const (
	// PrefixProd is the bearer prefix for production-issued keys.
	PrefixProd = "atlas_"
	// PrefixTest is the bearer prefix used in integration tests.
	PrefixTest = "atlas_test_"

	// SecretLen is the byte length of a fresh bearer secret before
	// base32-encoding. 20 bytes = 160 bits of entropy = 32 base32 chars.
	SecretLen = 20

	// HashKeyMinBytes is the minimum acceptable length of BEARER_HASH_KEY.
	HashKeyMinBytes = 32
)

// ErrHashKeyMissing reports that BEARER_HASH_KEY is unset or too short.
// cmd/atlas refuses to boot on this error.
var ErrHashKeyMissing = errors.New("bearer: BEARER_HASH_KEY env var must be set to at least 32 bytes (see docs/adr/0002-bearer-token-storage.md)")

// LoadHashKey reads BEARER_HASH_KEY from the environment, requiring at
// least HashKeyMinBytes bytes. Returns ErrHashKeyMissing if missing or
// too short.
func LoadHashKey() ([]byte, error) {
	v := os.Getenv("BEARER_HASH_KEY")
	if len(v) < HashKeyMinBytes {
		return nil, ErrHashKeyMissing
	}
	return []byte(v), nil
}

// Hasher computes HMAC-SHA256 hashes of bearer tokens.
type Hasher struct {
	key []byte
}

// NewHasher wraps a 32-byte (or longer) server secret. The key is borrowed,
// not copied; callers must not mutate it after the call.
func NewHasher(key []byte) (*Hasher, error) {
	if len(key) < HashKeyMinBytes {
		return nil, ErrHashKeyMissing
	}
	return &Hasher{key: key}, nil
}

// Hash returns HMAC-SHA256(token, key). 32-byte output.
func (h *Hasher) Hash(token string) []byte {
	m := hmac.New(sha256.New, h.key)
	m.Write([]byte(token))
	return m.Sum(nil)
}

// Generate returns a fresh bearer token with the supplied prefix. The
// caller should immediately hash it via Hasher.Hash and return the
// plaintext to the user exactly once.
func Generate(prefix string) (string, error) {
	if prefix != PrefixProd && prefix != PrefixTest {
		return "", fmt.Errorf("bearer: unknown prefix %q", prefix)
	}
	raw := make([]byte, SecretLen)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("bearer: random: %w", err)
	}
	body := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
	return prefix + strings.ToLower(body), nil
}

// Last4 returns the last four characters of the bearer plaintext. Safe to
// surface to operators; cannot be used to authenticate.
func Last4(token string) string {
	if len(token) < 4 {
		return token
	}
	return token[len(token)-4:]
}
