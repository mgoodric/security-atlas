// Package idpconfig is the DB-backed source of a tenant's OIDC IdP config
// (#2034). The Resolver reads oidc_idp_configs under the tenant's RLS
// context and hands the oidc Authenticator the issuer, client, decrypted
// client secret, allowed email domains and the unverified-email link flag
// an admin saved through PATCH /v1/admin/sso. The SecretCrypter seals the
// client secret at rest; the plaintext never reaches a log, an error
// string or an API response.
package idpconfig

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/google/uuid"
)

const (
	// SecretKeyEnv carries the base64-encoded 32-byte AES-256 key that seals
	// oidc_idp_configs.client_secret_enc. SecretKeyFileEnv names a 0600 file
	// holding the same, and wins when both are set. The key lives with the
	// deployment, never in the database beside the ciphertext.
	SecretKeyEnv     = "ATLAS_OIDC_SECRET_KEY"
	SecretKeyFileEnv = "ATLAS_OIDC_SECRET_KEY_FILE"

	secretKeyLen = 32
)

// sealedPrefix marks a client_secret_enc value this package sealed. A value
// without it is a secret stored before #2034, in plaintext, and is refused.
var sealedPrefix = []byte("atlas-idp-secret-v1:")

// ErrSecretKeyUnconfigured is returned by SecretCrypterFromEnv when neither
// key variable is set. Sign-in through a saved IdP config then fails closed,
// and PATCH /v1/admin/sso refuses to store a new secret.
var ErrSecretKeyUnconfigured = fmt.Errorf("idpconfig: no OIDC client-secret key configured (set %s or %s)", SecretKeyEnv, SecretKeyFileEnv)

// ErrSecretUnreadable is returned by Open for a value it cannot decrypt: a
// pre-#2034 plaintext secret, a different key, a tampered value, or a value
// sealed for another tenant or IdP name. It never carries the bytes.
var ErrSecretUnreadable = errors.New("idpconfig: client secret cannot be decrypted; re-enter it on the SSO settings page")

// SecretCrypter seals IdP client secrets with AES-256-GCM. The additional
// data binds each ciphertext to its tenant and IdP name, so a value copied
// into another tenant's row does not open.
type SecretCrypter struct {
	aead cipher.AEAD
}

// NewSecretCrypter builds a SecretCrypter over a 32-byte key.
func NewSecretCrypter(key []byte) (*SecretCrypter, error) {
	if len(key) != secretKeyLen {
		return nil, fmt.Errorf("idpconfig: client-secret key must be %d bytes, got %d", secretKeyLen, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("idpconfig: aes: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("idpconfig: gcm: %w", err)
	}
	return &SecretCrypter{aead: aead}, nil
}

// SecretCrypterFromEnv reads the key from SecretKeyFileEnv or SecretKeyEnv.
// It returns ErrSecretKeyUnconfigured when neither is set, and an error that
// names only the variable when the value is malformed.
func SecretCrypterFromEnv() (*SecretCrypter, error) {
	var b64, from string
	if path := os.Getenv(SecretKeyFileEnv); path != "" {
		raw, err := os.ReadFile(path) //nolint:gosec // operator-supplied key path
		if err != nil {
			return nil, fmt.Errorf("idpconfig: read %s: %w", SecretKeyFileEnv, err)
		}
		b64, from = string(raw), SecretKeyFileEnv
	} else if v := os.Getenv(SecretKeyEnv); v != "" {
		b64, from = v, SecretKeyEnv
	} else {
		return nil, ErrSecretKeyUnconfigured
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return nil, fmt.Errorf("idpconfig: %s is not valid base64", from)
	}
	if len(key) != secretKeyLen {
		return nil, fmt.Errorf("idpconfig: %s must decode to %d bytes, got %d", from, secretKeyLen, len(key))
	}
	return NewSecretCrypter(key)
}

// Seal returns the client_secret_enc value for plaintext under tenantID and
// IdP name: the prefix, then nonce || ciphertext.
func (c *SecretCrypter) Seal(tenantID uuid.UUID, name, plaintext string) ([]byte, error) {
	if plaintext == "" {
		return nil, errors.New("idpconfig: refusing to seal an empty client secret")
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("idpconfig: nonce: %w", err)
	}
	out := append([]byte{}, sealedPrefix...)
	out = append(out, nonce...)
	return c.aead.Seal(out, nonce, []byte(plaintext), secretAAD(tenantID, name)), nil
}

// Open reverses Seal for the same tenantID and name. Any other input is
// ErrSecretUnreadable.
func (c *SecretCrypter) Open(tenantID uuid.UUID, name string, sealed []byte) (string, error) {
	rest, ok := bytes.CutPrefix(sealed, sealedPrefix)
	if !ok || len(rest) < c.aead.NonceSize() {
		return "", ErrSecretUnreadable
	}
	ns := c.aead.NonceSize()
	pt, err := c.aead.Open(nil, rest[:ns], rest[ns:], secretAAD(tenantID, name))
	if err != nil {
		return "", ErrSecretUnreadable
	}
	return string(pt), nil
}

// IsSealed reports whether a stored value was sealed by this package, as
// opposed to a pre-#2034 plaintext secret.
func IsSealed(stored []byte) bool { return bytes.HasPrefix(stored, sealedPrefix) }

// secretAAD is the length-prefixed tenant id and IdP name.
func secretAAD(tenantID uuid.UUID, name string) []byte {
	aad := append([]byte{}, tenantID[:]...)
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(name)))
	aad = append(aad, n[:]...)
	return append(aad, name...)
}
