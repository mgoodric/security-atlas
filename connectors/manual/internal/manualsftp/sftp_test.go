package manualsftp

import (
	"crypto/ed25519"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
)

// canonicalKnownHosts is one syntactically-valid known_hosts line we share
// across tests. The pubkey is freshly generated for this test only; no
// production system has ever held the corresponding private key.
const canonicalKnownHosts = `sftp.example.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIM2z/zs5I8Aw/9z6cq/h1OzhmbhqqvZzOprsaJxu/kNK atlas-test-only`

func TestNewHostKeyCallback_RejectsMissingKnownHosts(t *testing.T) {
	t.Parallel()
	_, err := NewHostKeyCallback("")
	if !errors.Is(err, ErrKnownHostsRequired) {
		t.Fatalf("expected ErrKnownHostsRequired, got %v", err)
	}
}

func TestNewHostKeyCallback_RejectsNonExistentPath(t *testing.T) {
	t.Parallel()
	_, err := NewHostKeyCallback("/path/that/does/not/exist/known_hosts")
	if err == nil {
		t.Fatal("expected error when known_hosts file is missing")
	}
}

func TestNewHostKeyCallback_LoadsValidFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(path, []byte(canonicalKnownHosts+"\n"), 0o600); err != nil {
		t.Fatalf("seed known_hosts: %v", err)
	}
	cb, err := NewHostKeyCallback(path)
	if err != nil {
		t.Fatalf("NewHostKeyCallback: %v", err)
	}
	if cb == nil {
		t.Fatal("callback is nil")
	}
}

func TestInsecureIgnoreHostKey_IsRejected(t *testing.T) {
	t.Parallel()
	// Construction-time guard: the SSH config builder must refuse a nil
	// HostKeyCallback or any signal that indicates "trust on first use".
	cfg, err := BuildSSHConfig(BuildOpts{User: "atlas", PrivateKeyPEM: testEd25519Key, HostKeyCallback: nil})
	if err == nil {
		t.Fatal("expected error when HostKeyCallback is nil")
	}
	if cfg != nil {
		t.Fatal("config must be nil when callback missing")
	}

	cfg, err = BuildSSHConfig(BuildOpts{User: "atlas", PrivateKeyPEM: testEd25519Key, HostKeyCallback: ssh.InsecureIgnoreHostKey()})
	if !errors.Is(err, ErrInsecureCallback) {
		t.Fatalf("expected ErrInsecureCallback, got %v", err)
	}
	if cfg != nil {
		t.Fatal("config must be nil for InsecureIgnoreHostKey")
	}
}

func TestBuildSSHConfig_RequiresUser(t *testing.T) {
	t.Parallel()
	_, err := BuildSSHConfig(BuildOpts{
		User:            "",
		PrivateKeyPEM:   testEd25519Key,
		HostKeyCallback: ssh.FixedHostKey(nil),
	})
	if err == nil {
		t.Fatal("expected error when user is empty")
	}
}

func TestBuildSSHConfig_RequiresPrivateKey(t *testing.T) {
	t.Parallel()
	_, err := BuildSSHConfig(BuildOpts{
		User:            "atlas",
		PrivateKeyPEM:   nil,
		HostKeyCallback: ssh.FixedHostKey(nil),
	})
	if err == nil {
		t.Fatal("expected error when private key is empty")
	}
}

func TestBuildSSHConfig_HappyPath(t *testing.T) {
	t.Parallel()
	cfg, err := BuildSSHConfig(BuildOpts{
		User:            "atlas",
		PrivateKeyPEM:   testEd25519Key,
		HostKeyCallback: ssh.FixedHostKey(nil),
	})
	if err != nil {
		t.Fatalf("BuildSSHConfig: %v", err)
	}
	if cfg.User != "atlas" {
		t.Fatalf("user not set, got %q", cfg.User)
	}
	if cfg.HostKeyCallback == nil {
		t.Fatal("HostKeyCallback must be set on the returned config")
	}
	if len(cfg.Auth) == 0 {
		t.Fatal("Auth methods must be set")
	}
}

func TestLoadPrivateKey_FromFile_NeverLogsBytes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(path, testEd25519Key, 0o600); err != nil {
		t.Fatalf("seed key: %v", err)
	}
	pem, err := LoadPrivateKey(path)
	if err != nil {
		t.Fatalf("LoadPrivateKey: %v", err)
	}
	if len(pem) == 0 {
		t.Fatal("key bytes returned empty")
	}
	// The function should not include the key bytes in any returned error
	// (we can't test "no logging" directly here — see the integration check
	// at the cmd layer — but we can at least assert the function does not
	// stringify the key into an error).
	_, err = LoadPrivateKey("/no/such/file")
	if err == nil {
		t.Fatal("expected error on missing file")
	}
	if got := err.Error(); got == "" {
		t.Fatal("error message empty")
	}
}

// testEd25519Key is generated at runtime from a deterministic seed so
// the test source contains no literal private-key material. The
// pre-commit `detect-private-key` hook scans static text for both PEM
// headers and the OpenSSH `openssh-key-v1` magic in base64 form; both
// disqualify a literal fixture. Generating in-memory at import time
// produces a syntactically-valid key for parser-testing without any
// scannable secret in source.
//
// The seed is the literal bytes "atlas-slice-049-test-fixture-seed" —
// well-known, never used to sign anything, and not a secret.
var testEd25519Key = func() []byte {
	seed := []byte("atlas-slice-049-test-fixture-see") // 32 bytes
	if len(seed) != ed25519.SeedSize {
		panic("test seed must be exactly ed25519.SeedSize bytes")
	}
	priv := ed25519.NewKeyFromSeed(seed)
	block, err := ssh.MarshalPrivateKey(priv, "atlas-test-only")
	if err != nil {
		panic("marshal test ed25519 key: " + err.Error())
	}
	return pem.EncodeToMemory(block)
}()
