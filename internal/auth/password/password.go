// Package password hashes and verifies human passwords with argon2id.
//
// Parameters follow the RFC 9106 recommendation: m=64MiB, t=1, p=4. The
// encoded form is the standard `$argon2id$v=19$m=...$...$...` string so
// future operators can identify the algorithm from a column dump.
//
// See docs/adr/0002-bearer-token-storage.md for why bearer tokens use a
// different (HMAC-SHA256) algorithm.
package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Algorithm identifier persisted alongside the hash. Future migrations
// may add `argon2id-v2` etc; the verifier dispatches on this string.
const Algorithm = "argon2id"

// Default RFC 9106 first-recommendation parameters.
const (
	defaultMemoryKiB  uint32 = 64 * 1024 // 64 MiB
	defaultIterations uint32 = 1
	defaultThreads    uint8  = 4
	defaultKeyLen     uint32 = 32
	defaultSaltLen    uint32 = 16
)

// ErrInvalidHash reports that the encoded hash form was not recognised.
var ErrInvalidHash = errors.New("password: invalid encoded hash form")

// Hash returns the encoded argon2id form of plaintext. The encoded form
// includes algorithm id, version, parameters, salt, and key — enough to
// re-verify without storing parameters separately.
func Hash(plaintext string) (string, error) {
	salt := make([]byte, defaultSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("password: random salt: %w", err)
	}
	key := argon2.IDKey(
		[]byte(plaintext),
		salt,
		defaultIterations,
		defaultMemoryKiB,
		defaultThreads,
		defaultKeyLen,
	)
	return encode(salt, key, defaultMemoryKiB, defaultIterations, defaultThreads), nil
}

// Verify checks plaintext against an encoded argon2id hash. Constant-time
// comparison via subtle.ConstantTimeCompare.
func Verify(plaintext, encoded string) (bool, error) {
	m, t, p, salt, want, err := decode(encoded)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(plaintext), salt, t, m, p, uint32(len(want)))
	if subtle.ConstantTimeCompare(got, want) == 1 {
		return true, nil
	}
	return false, nil
}

func encode(salt, key []byte, mKiB, t uint32, p uint8) string {
	saltB64 := base64.RawStdEncoding.EncodeToString(salt)
	keyB64 := base64.RawStdEncoding.EncodeToString(key)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, mKiB, t, p, saltB64, keyB64)
}

func decode(encoded string) (m, t uint32, p uint8, salt, key []byte, err error) {
	parts := strings.Split(encoded, "$")
	// Empty leading element from leading `$`, then 5 segments.
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		err = ErrInvalidHash
		return
	}
	var version int
	if _, e := fmt.Sscanf(parts[2], "v=%d", &version); e != nil {
		err = ErrInvalidHash
		return
	}
	if version != argon2.Version {
		err = ErrInvalidHash
		return
	}
	if _, e := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); e != nil {
		err = ErrInvalidHash
		return
	}
	salt, err = base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		err = ErrInvalidHash
		return
	}
	key, err = base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		err = ErrInvalidHash
		return
	}
	return
}
