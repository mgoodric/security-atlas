//go:build integration

// Integration tests for the cosign wrapper against the REAL cosign
// binary (slice 413 / ADR-0010 Phase 1, AC-6 Mode A).
//
// These tests exercise the production exec boundary (execRunner) end to
// end: the real `cosign sign-blob` / `cosign verify-blob` argv, the
// offline no-transparency-log flags, the env allowlist, and the
// stdin/stdout plumbing — proving the wrapper drives the installed
// cosign correctly.
//
// KMS stand-in (clearly marked per the slice brief): a real cloud KMS is
// not available in CI, so these tests use a LOCALLY-GENERATED throwaway
// cosign key pair (`cosign generate-key-pair`, empty password — no real
// key material, GitGuardian-safe) in place of a cloud-KMS key reference.
// The local-key `--key cosign.key` path drives the IDENTICAL sign-blob /
// verify-blob code path cosign uses for a `awskms://`/`gcpkms://` URI —
// the only difference is where cosign resolves the private key. The
// dispatch + manifest + Mode logic above the wrapper is fully exercised
// by the no-binary unit tests in sign_cosign_test.go (fake cosign).
//
// If cosign is not installed, every test self-skips with a note (the
// internal/oscal optional-dependency convention).
package cosign

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// requireCosign skips the test when the cosign binary is absent.
func requireCosign(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath(DefaultBinary); err != nil {
		t.Skipf("cosign binary not found on PATH — skipping real-binary integration test: %v", err)
	}
}

// genLocalKeyPair generates a throwaway, empty-password cosign key pair in
// a temp dir and returns the private + public key paths. Empty password
// is fine — these keys live only for the test and are never committed.
func genLocalKeyPair(t *testing.T) (privPath, pubPath string) {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command(DefaultBinary, "generate-key-pair")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "COSIGN_PASSWORD=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cosign generate-key-pair failed: %v\n%s", err, out)
	}
	privPath = filepath.Join(dir, "cosign.key")
	pubPath = filepath.Join(dir, "cosign.pub")
	for _, p := range []string{privPath, pubPath} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected generated key %s: %v", p, err)
		}
	}
	return privPath, pubPath
}

// signWithKeyFile runs the production sign argv against a local key file
// (the KMS stand-in). It mirrors Client.SignBlob's argv exactly except
// the validateKMSRef gate (a local path is not a KMS URI), so this proves
// the real binary accepts the offline flag set the wrapper emits.
func signWithKeyFile(ctx context.Context, t *testing.T, keyPath string, blob []byte) ([]byte, error) {
	t.Helper()
	r := execRunner{}
	env := append(os.Environ(), "COSIGN_PASSWORD=")
	stdout, stderr, err := r.run(ctx, DefaultBinary, env, blob,
		"sign-blob", "--key", keyPath, "--yes",
		"--use-signing-config=false", "--tlog-upload=false", "-")
	if err != nil {
		t.Logf("sign stderr: %s", stderr)
		return nil, err
	}
	return stdout, nil
}

func TestIntegration_SignVerifyRoundTrip_LocalKey(t *testing.T) {
	requireCosign(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	priv, pub := genLocalKeyPair(t)
	blob := []byte("atlas-oscal-bundle-digest-0123456789abcdef")

	rawSig, err := signWithKeyFile(ctx, t, priv, blob)
	if err != nil {
		t.Fatalf("sign-blob with local key: %v", err)
	}

	// Write the detached signature to a temp file and verify via the
	// production VerifyBlob-style argv (the real exec boundary).
	sigPath := filepath.Join(t.TempDir(), "blob.sig")
	if err := os.WriteFile(sigPath, rawSig, 0o600); err != nil {
		t.Fatalf("write sig: %v", err)
	}

	r := execRunner{}
	_, stderr, err := r.run(ctx, DefaultBinary, os.Environ(), blob,
		"verify-blob", "--key", pub, "--signature", sigPath,
		"--insecure-ignore-tlog=true", "-")
	if err != nil {
		t.Fatalf("verify-blob of a freshly signed blob failed: %v\nstderr: %s", err, stderr)
	}
}

func TestIntegration_VerifyFailsOnTamper_LocalKey(t *testing.T) {
	requireCosign(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	priv, pub := genLocalKeyPair(t)
	blob := []byte("original-digest")

	rawSig, err := signWithKeyFile(ctx, t, priv, blob)
	if err != nil {
		t.Fatalf("sign-blob: %v", err)
	}
	sigPath := filepath.Join(t.TempDir(), "blob.sig")
	if err := os.WriteFile(sigPath, rawSig, 0o600); err != nil {
		t.Fatalf("write sig: %v", err)
	}

	// Verify against a TAMPERED blob — must fail.
	r := execRunner{}
	_, _, err = r.run(ctx, DefaultBinary, os.Environ(), []byte("TAMPERED-digest"),
		"verify-blob", "--key", pub, "--signature", sigPath,
		"--insecure-ignore-tlog=true", "-")
	if err == nil {
		t.Fatal("verify-blob must FAIL for a tampered blob")
	}
}

// TestIntegration_ClientAvailable confirms the Client.Available pre-flight
// reports true when cosign is genuinely installed.
func TestIntegration_ClientAvailable(t *testing.T) {
	requireCosign(t)
	if !New().Available() {
		t.Fatal("Client.Available must be true when cosign is on PATH")
	}
}

// TestIntegration_NoTlogEntry asserts the offline sign produces NO
// transparency-log entry — the P0-413-1 guarantee that cosign-kms never
// depends on Rekor. We sign to a --bundle and assert the bundle has no
// tlogEntries.
func TestIntegration_NoTlogEntry(t *testing.T) {
	requireCosign(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	priv, _ := genLocalKeyPair(t)
	bundlePath := filepath.Join(t.TempDir(), "out.bundle")

	r := execRunner{}
	env := append(os.Environ(), "COSIGN_PASSWORD=")
	_, stderr, err := r.run(ctx, DefaultBinary, env, []byte("blob-for-tlog-check"),
		"sign-blob", "--key", priv, "--yes",
		"--use-signing-config=false", "--tlog-upload=false",
		"--bundle", bundlePath, "-")
	if err != nil {
		t.Fatalf("offline sign-blob: %v\nstderr: %s", err, stderr)
	}
	data, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	if strings.Contains(string(data), "tlogEntries") {
		t.Errorf("offline sign must NOT produce a transparency-log entry (P0-413-1); bundle:\n%s", data)
	}
}
