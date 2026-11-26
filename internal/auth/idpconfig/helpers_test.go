package idpconfig

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/netguard"
)

func testCrypter(t *testing.T, fill byte) *SecretCrypter {
	t.Helper()
	c, err := NewSecretCrypter(bytes.Repeat([]byte{fill}, secretKeyLen))
	if err != nil {
		t.Fatalf("NewSecretCrypter: %v", err)
	}
	return c
}

func TestSecretCrypter_RoundTripAndBinding(t *testing.T) {
	t.Parallel()
	c := testCrypter(t, 1)
	tenant, other := uuid.New(), uuid.New()
	sealed, err := c.Seal(tenant, "primary", "s3cret-value")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Contains(sealed, []byte("s3cret-value")) {
		t.Fatal("sealed value carries the plaintext")
	}
	if !IsSealed(sealed) {
		t.Fatal("IsSealed(sealed) = false")
	}
	got, err := c.Open(tenant, "primary", sealed)
	if err != nil || got != "s3cret-value" {
		t.Fatalf("Open = %q, %v; want the plaintext", got, err)
	}

	tampered := append([]byte{}, sealed...)
	tampered[len(tampered)-1] ^= 0xff
	refusals := map[string]func() (string, error){
		"another tenant":   func() (string, error) { return c.Open(other, "primary", sealed) },
		"another name":     func() (string, error) { return c.Open(tenant, "secondary", sealed) },
		"another key":      func() (string, error) { return testCrypter(t, 2).Open(tenant, "primary", sealed) },
		"tampered":         func() (string, error) { return c.Open(tenant, "primary", tampered) },
		"legacy plaintext": func() (string, error) { return c.Open(tenant, "primary", []byte("s3cret-value")) },
		"prefix only":      func() (string, error) { return c.Open(tenant, "primary", sealedPrefix) },
	}
	for name, open := range refusals {
		pt, err := open()
		if !errors.Is(err, ErrSecretUnreadable) || pt != "" {
			t.Errorf("%s: Open = %q, %v; want ErrSecretUnreadable", name, pt, err)
		}
	}
	if _, err := c.Seal(tenant, "primary", ""); err == nil {
		t.Error("Seal of an empty secret succeeded")
	}
}

func TestSecretCrypterFromEnv(t *testing.T) {
	good := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, secretKeyLen))

	t.Setenv(SecretKeyEnv, "")
	t.Setenv(SecretKeyFileEnv, "")
	if _, err := SecretCrypterFromEnv(); !errors.Is(err, ErrSecretKeyUnconfigured) {
		t.Errorf("unset: err = %v, want ErrSecretKeyUnconfigured", err)
	}

	t.Setenv(SecretKeyEnv, good)
	if c, err := SecretCrypterFromEnv(); err != nil || c == nil {
		t.Errorf("env key: %v", err)
	}

	short := base64.StdEncoding.EncodeToString([]byte("short-key-material"))
	t.Setenv(SecretKeyEnv, short)
	_, err := SecretCrypterFromEnv()
	if err == nil {
		t.Fatal("short key accepted")
	}
	if strings.Contains(err.Error(), short) {
		t.Errorf("error echoes the key: %v", err)
	}

	t.Setenv(SecretKeyEnv, "not base64 !!")
	if _, err := SecretCrypterFromEnv(); err == nil {
		t.Error("non-base64 key accepted")
	}

	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte(good+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(SecretKeyFileEnv, path)
	if c, err := SecretCrypterFromEnv(); err != nil || c == nil {
		t.Errorf("key file wins over a bad env key: %v", err)
	}
}

func TestValidateIssuerURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw      string
		loopback bool
		ok       bool
	}{
		{"https://idp.example.com", false, true},
		{"https://idp.example.com/realms/atlas", false, true},
		{"http://idp.example.com", false, false},
		{"http://idp.example.com", true, false},
		{"http://127.0.0.1:8080", false, false},
		{"http://127.0.0.1:8080", true, true},
		{"http://[::1]:8080", true, true},
		{"http://localhost:8080", true, true},
		{"ftp://idp.example.com", true, false},
		{"https://user:pw@idp.example.com", false, false},
		{"https://idp.example.com?x=1", false, false},
		{"https://idp.example.com#frag", false, false},
		{"idp.example.com", false, false},
		{"", false, false},
	}
	for _, tc := range cases {
		err := ValidateIssuerURL(tc.raw, netguard.Policy{AllowLoopback: tc.loopback, AllowPrivate: tc.loopback})
		if (err == nil) != tc.ok {
			t.Errorf("ValidateIssuerURL(%q, loopback=%t) = %v, want ok=%t", tc.raw, tc.loopback, err, tc.ok)
		}
	}
}

func TestLocalModeFromEnv(t *testing.T) {
	for v, want := range map[string]bool{"": false, "false": false, "true": true} {
		t.Setenv(LocalModeEnv, v)
		got, err := LocalModeFromEnv()
		if err != nil || got != want {
			t.Errorf("%q: got %t, %v; want %t", v, got, err, want)
		}
	}
	t.Setenv(LocalModeEnv, "yes")
	if _, err := LocalModeFromEnv(); err == nil {
		t.Error(`"yes" accepted`)
	}
}
