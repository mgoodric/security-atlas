package cloud

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Secret wraps the plaintext provider API key so it can NEVER be accidentally
// logged or serialized (P0-499-4 / AC-11). It satisfies fmt.Stringer,
// fmt.GoStringer, and encoding.TextMarshaler so %s / %v / %#v / %q,
// json.Marshal, and a structured-logger field all render the redaction
// placeholder, never the value. The plaintext is reachable ONLY through
// Reveal(), which the crypter + the cloud adapters call at the moment of use
// and never log. Mirrors internal/notify.Secret (the established repo pattern);
// kept package-local so the cloud layer carries no dependency on notify.
type Secret string

const redactedPlaceholder = "<redacted>"

// String implements fmt.Stringer — %s / %v render the placeholder.
func (s Secret) String() string { return redactedPlaceholder }

// GoString implements fmt.GoStringer — %#v / %+v render the placeholder.
func (s Secret) GoString() string { return redactedPlaceholder }

// MarshalText implements encoding.TextMarshaler — json.Marshal and any text
// encoder render the placeholder, never the value.
func (s Secret) MarshalText() ([]byte, error) { return []byte(redactedPlaceholder), nil }

// Reveal returns the plaintext for use at the transport boundary (encrypting,
// or setting an Authorization header). Callers MUST NOT log the result.
func (s Secret) Reveal() string { return string(s) }

// IsZero reports whether the secret is empty.
func (s Secret) IsZero() bool { return s == "" }

// Crypter encrypts/decrypts the provider API key for at-rest storage using
// AES-256-GCM. The 32-byte master key comes from the deployment, not the DB
// (the keystore-style "key material from a 0600 file / env, never alongside the
// ciphertext it protects" pattern). The ciphertext stored in
// tenant_llm_routing.api_key_ciphertext is base64(nonce || gcm_ciphertext).
type Crypter struct {
	aead cipher.AEAD
}

// masterKeyEnv is the env var carrying the base64-encoded 32-byte AES master
// key. masterKeyFileEnv points at a 0600 file holding the same (preferred for
// self-host; the file is read once at boot, the path never logged).
const (
	masterKeyEnv     = "ATLAS_LLM_CLOUD_KEY"
	masterKeyFileEnv = "ATLAS_LLM_CLOUD_KEY_FILE"
)

// aesKeyLen is the AES-256 key length in bytes.
const aesKeyLen = 32

// ErrCrypterUnconfigured is returned by NewCrypterFromEnv when no master key is
// configured. A deployment that has not set a cloud master key cannot store a
// cloud provider key — the config Store surfaces this as a clear "cloud routing
// not configured on this deployment" error rather than storing a key it cannot
// protect.
var ErrCrypterUnconfigured = errors.New("cloud: no LLM cloud master key configured (set ATLAS_LLM_CLOUD_KEY or ATLAS_LLM_CLOUD_KEY_FILE)")

// ErrDecrypt is returned when ciphertext cannot be decrypted (wrong key,
// truncated/tampered ciphertext). It never echoes the ciphertext or key.
var ErrDecrypt = errors.New("cloud: provider key decryption failed")

// NewCrypter builds a Crypter from a 32-byte AES-256 key.
func NewCrypter(key []byte) (*Crypter, error) {
	if len(key) != aesKeyLen {
		return nil, fmt.Errorf("cloud: master key must be %d bytes (AES-256), got %d", aesKeyLen, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("cloud: aes new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cloud: new gcm: %w", err)
	}
	return &Crypter{aead: aead}, nil
}

// NewCrypterFromEnv reads the master key from ATLAS_LLM_CLOUD_KEY_FILE (a 0600
// file, preferred) or ATLAS_LLM_CLOUD_KEY (base64), and builds a Crypter. It
// returns ErrCrypterUnconfigured when neither is set so the caller can decide
// whether cloud routing is available on this deployment. The key material is
// never logged; an error mentions only the env var names.
func NewCrypterFromEnv() (*Crypter, error) {
	if path := os.Getenv(masterKeyFileEnv); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("cloud: read %s: %w", masterKeyFileEnv, err)
		}
		key, err := decodeMasterKey(strings.TrimSpace(string(raw)))
		if err != nil {
			return nil, err
		}
		return NewCrypter(key)
	}
	if b64 := os.Getenv(masterKeyEnv); b64 != "" {
		key, err := decodeMasterKey(strings.TrimSpace(b64))
		if err != nil {
			return nil, err
		}
		return NewCrypter(key)
	}
	return nil, ErrCrypterUnconfigured
}

// decodeMasterKey base64-decodes the master key and validates its length. The
// error never includes the (attempted) key bytes.
func decodeMasterKey(b64 string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("cloud: master key is not valid base64: %w", err)
	}
	if len(key) != aesKeyLen {
		return nil, fmt.Errorf("cloud: master key must decode to %d bytes (AES-256), got %d", aesKeyLen, len(key))
	}
	return key, nil
}

// Encrypt returns base64(nonce || gcm_ciphertext) for the plaintext key. A
// fresh random nonce is generated per call, so re-encrypting the same key
// yields different ciphertext (no deterministic-ciphertext leak). The result is
// what the Store writes to tenant_llm_routing.api_key_ciphertext.
func (c *Crypter) Encrypt(plaintext Secret) (string, error) {
	if plaintext.IsZero() {
		return "", errors.New("cloud: refusing to encrypt an empty key")
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("cloud: nonce: %w", err)
	}
	ct := c.aead.Seal(nonce, nonce, []byte(plaintext.Reveal()), nil)
	return base64.StdEncoding.EncodeToString(ct), nil
}

// Decrypt reverses Encrypt, returning the plaintext key as a Secret so it
// remains masked in any log/format path until the adapter Reveal()s it at the
// transport boundary. A wrong key / tampered ciphertext yields ErrDecrypt,
// never the bytes.
func (c *Crypter) Decrypt(ciphertextB64 string) (Secret, error) {
	raw, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return "", ErrDecrypt
	}
	ns := c.aead.NonceSize()
	if len(raw) < ns {
		return "", ErrDecrypt
	}
	nonce, ct := raw[:ns], raw[ns:]
	pt, err := c.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", ErrDecrypt
	}
	return Secret(pt), nil
}
