package cloud

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
)

// fakeKey32 is an obviously-fake 32-byte AES key for tests (never a real
// credential — GitGuardian-neutral).
func fakeKey32(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, aesKeyLen)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		t.Fatalf("rand key: %v", err)
	}
	return key
}

func newTestCrypter(t *testing.T) *Crypter {
	t.Helper()
	c, err := NewCrypter(fakeKey32(t))
	if err != nil {
		t.Fatalf("NewCrypter: %v", err)
	}
	return c
}

func TestCrypter_RoundTrip(t *testing.T) {
	t.Parallel()
	c := newTestCrypter(t)
	// Obviously-fake provider key value.
	plaintext := Secret("test-provider-key-NOT-REAL-000")

	ct, err := c.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if ct == "" {
		t.Fatal("empty ciphertext")
	}
	if strings.Contains(ct, plaintext.Reveal()) {
		t.Fatal("ciphertext contains plaintext")
	}

	got, err := c.Decrypt(ct)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if got.Reveal() != plaintext.Reveal() {
		t.Fatalf("round-trip mismatch: got %q", got.Reveal())
	}
}

func TestCrypter_NonceVariesPerEncrypt(t *testing.T) {
	t.Parallel()
	c := newTestCrypter(t)
	pt := Secret("same-key-value-xyz")
	ct1, _ := c.Encrypt(pt)
	ct2, _ := c.Encrypt(pt)
	if ct1 == ct2 {
		t.Fatal("two encryptions of the same key produced identical ciphertext (nonce reuse)")
	}
}

func TestCrypter_WrongKeyFails(t *testing.T) {
	t.Parallel()
	c1 := newTestCrypter(t)
	c2 := newTestCrypter(t)
	ct, _ := c1.Encrypt(Secret("abc"))
	if _, err := c2.Decrypt(ct); err == nil {
		t.Fatal("decrypt with wrong key succeeded")
	}
}

func TestCrypter_TamperedCiphertextFails(t *testing.T) {
	t.Parallel()
	c := newTestCrypter(t)
	ct, _ := c.Encrypt(Secret("abc"))
	// Flip a byte in the base64 payload.
	tampered := ct[:len(ct)-2] + "AA"
	if _, err := c.Decrypt(tampered); err == nil {
		t.Fatal("decrypt of tampered ciphertext succeeded")
	}
}

func TestCrypter_RejectsBadKeyLength(t *testing.T) {
	t.Parallel()
	if _, err := NewCrypter([]byte("short")); err == nil {
		t.Fatal("NewCrypter accepted a short key")
	}
}

func TestCrypter_EmptyPlaintextRejected(t *testing.T) {
	t.Parallel()
	c := newTestCrypter(t)
	if _, err := c.Encrypt(Secret("")); err == nil {
		t.Fatal("Encrypt accepted an empty key")
	}
}

func TestNewCrypterFromEnv_UnconfiguredAndConfigured(t *testing.T) {
	// Not parallel: mutates env.
	t.Setenv(masterKeyEnv, "")
	t.Setenv(masterKeyFileEnv, "")
	if _, err := NewCrypterFromEnv(); err != ErrCrypterUnconfigured {
		t.Fatalf("want ErrCrypterUnconfigured, got %v", err)
	}

	b64 := base64.StdEncoding.EncodeToString(fakeKey32(t))
	t.Setenv(masterKeyEnv, b64)
	if _, err := NewCrypterFromEnv(); err != nil {
		t.Fatalf("configured env: %v", err)
	}
}

func TestNewCrypterFromEnv_BadBase64(t *testing.T) {
	t.Setenv(masterKeyFileEnv, "")
	t.Setenv(masterKeyEnv, "!!!not-base64!!!")
	if _, err := NewCrypterFromEnv(); err == nil {
		t.Fatal("accepted non-base64 master key")
	}
}

// ----- Secret masking (P0-499-4 / AC-11) -----

func TestSecret_MasksInEveryFormatPath(t *testing.T) {
	t.Parallel()
	const realKey = "super-secret-provider-key-123"
	s := Secret(realKey)

	cases := map[string]string{
		"%s":  s.String(),
		"%v":  fmt.Sprintf("%v", s),
		"%q":  fmt.Sprintf("%q", s),
		"%#v": fmt.Sprintf("%#v", s),
		"%+v": fmt.Sprintf("%+v", s),
	}
	for verb, out := range cases {
		if strings.Contains(out, realKey) {
			t.Errorf("%s leaked the secret: %q", verb, out)
		}
		if !strings.Contains(out, redactedPlaceholder) {
			t.Errorf("%s did not render the placeholder: %q", verb, out)
		}
	}

	// JSON marshal of a struct embedding the secret must not leak it.
	type wrap struct {
		Key Secret `json:"key"`
	}
	b, err := json.Marshal(wrap{Key: s})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), realKey) {
		t.Fatalf("json leaked the secret: %s", b)
	}

	// Reveal is the ONLY path to the plaintext.
	if s.Reveal() != realKey {
		t.Fatal("Reveal did not return the plaintext")
	}
}
