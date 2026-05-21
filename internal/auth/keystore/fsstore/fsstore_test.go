package fsstore_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/mgoodric/security-atlas/internal/auth/keystore"
	"github.com/mgoodric/security-atlas/internal/auth/keystore/fsstore"
)

// TestFirstBootGeneratesKeypair covers ISC-5 + AC-10: first boot with an
// empty keystore path generates a new ES256 keypair.
func TestFirstBootGeneratesKeypair(t *testing.T) {
	dir := t.TempDir()
	store, err := fsstore.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	sk, vks, err := store.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if sk.Key == nil {
		t.Fatal("signing key nil after first boot")
	}
	if sk.Key.Curve != keystore.Curve() {
		t.Fatalf("expected curve P-256, got %v", sk.Key.Curve)
	}
	if sk.KeyID == "" {
		t.Fatal("signing key id empty")
	}
	if len(vks) != 1 {
		t.Fatalf("expected 1 verification key, got %d", len(vks))
	}
	if vks[0].KeyID != sk.KeyID {
		t.Fatalf("verification key id %q does not match signing key id %q", vks[0].KeyID, sk.KeyID)
	}
	// The signing key's public half must equal the verification key.
	if !sk.Key.PublicKey.Equal(vks[0].Key) {
		t.Fatal("signing key public half does not match verification key")
	}
	// Files must exist on disk.
	if _, err := os.Stat(filepath.Join(dir, sk.KeyID+".key")); err != nil {
		t.Fatalf("private key file missing: %v", err)
	}
}

// TestReuseExistingKeypair covers ISC-6 + AC-10: second boot reuses the
// already-generated keypair instead of creating a new one.
func TestReuseExistingKeypair(t *testing.T) {
	dir := t.TempDir()
	first, err := fsstore.Open(dir)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	sk1, _, err := first.Get(context.Background())
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}

	second, err := fsstore.Open(dir)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	sk2, _, err := second.Get(context.Background())
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if sk1.KeyID != sk2.KeyID {
		t.Fatalf("expected same key id after reopen, got %q vs %q", sk1.KeyID, sk2.KeyID)
	}
	if !ecdsaEqual(sk1.Key, sk2.Key) {
		t.Fatal("expected same private key bytes after reopen")
	}
}

// TestPrivateKeyFileModeIs0600 covers ISC-7 + ISC-40 + AC-13 + P0-187-5:
// the private key file must be readable only by the owning user.
func TestPrivateKeyFileModeIs0600(t *testing.T) {
	dir := t.TempDir()
	store, err := fsstore.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	sk, _, err := store.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, sk.KeyID+".key"))
	if err != nil {
		t.Fatalf("Stat private key: %v", err)
	}
	mode := info.Mode().Perm()
	want := fs.FileMode(0o600)
	if mode != want {
		t.Fatalf("private key mode = %o, want %o", mode, want)
	}
}

// TestRotateReturnsUnsupportedInV1 covers ISC-2: the interface declares
// Rotate; the v1 implementation returns ErrRotateUnsupported.
func TestRotateReturnsUnsupportedInV1(t *testing.T) {
	dir := t.TempDir()
	store, err := fsstore.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	err = store.Rotate(context.Background())
	if err == nil {
		t.Fatal("expected Rotate to return ErrRotateUnsupported, got nil")
	}
	if err != keystore.ErrRotateUnsupported {
		t.Fatalf("expected ErrRotateUnsupported, got %v", err)
	}
}

// TestDefaultPathFromEnv covers ISC-4 + D3: ATLAS_KEYSTORE_PATH overrides
// the compiled-in default, and an empty path resolves to the default.
func TestDefaultPathFromEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ATLAS_KEYSTORE_PATH", dir)
	resolved := fsstore.ResolvePath("")
	if resolved != dir {
		t.Fatalf("ResolvePath(\"\") = %q, want %q from env", resolved, dir)
	}
	// Explicit non-empty argument wins over env.
	resolved = fsstore.ResolvePath("/some/other/path")
	if resolved != "/some/other/path" {
		t.Fatalf("explicit path should win over env, got %q", resolved)
	}
	// Empty arg + empty env => the compiled-in default.
	t.Setenv("ATLAS_KEYSTORE_PATH", "")
	resolved = fsstore.ResolvePath("")
	if resolved != fsstore.DefaultPath {
		t.Fatalf("empty path + empty env should return DefaultPath %q, got %q", fsstore.DefaultPath, resolved)
	}
}

func ecdsaEqual(a, b *ecdsa.PrivateKey) bool {
	if a == nil || b == nil {
		return false
	}
	// Compare via the standard PKCS#8 encoding rather than reaching
	// into the internal big.Int fields (deprecated since Go 1.25 — see
	// the doc comment on ecdsa.PrivateKey.D).
	aBytes, errA := x509.MarshalPKCS8PrivateKey(a)
	bBytes, errB := x509.MarshalPKCS8PrivateKey(b)
	if errA != nil || errB != nil {
		return false
	}
	if len(aBytes) != len(bBytes) {
		return false
	}
	for i := range aBytes {
		if aBytes[i] != bBytes[i] {
			return false
		}
	}
	return true
}
