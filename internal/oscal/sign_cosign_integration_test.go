//go:build integration

// Bundle-level sign→verify round-trip integration tests for both Phase-1
// modes (slice 413 / ADR-0010, AC-6).
//
//   - Mode B (embedded-ed25519): fully hermetic, no external dependency.
//   - Mode A (cosign-kms): exercises the real cosign binary via a
//     local-key adapter standing in for a cloud KMS (clearly marked).
//     A real cloud KMS is unavailable in CI, so a locally-generated
//     throwaway cosign key pair plays the KMS key — driving the SAME
//     `cosign sign-blob`/`verify-blob` exec path the production
//     cosign.Client uses for an `awskms://` URI. The KMSSigner /
//     VerifyBundleWithCosign dispatch + manifest + digest logic above the
//     binary is the production code under test; only the key-resolution
//     leaf differs.
//
// Self-skips when cosign is absent.
package oscal

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// localKeyCosign is a test adapter satisfying oscal.CosignSigner +
// oscal.CosignVerifier by shelling out to the real cosign binary with a
// local key file (the KMS stand-in). It uses the SAME offline argv the
// production cosign.Client emits.
type localKeyCosign struct {
	privPath string
	pubPath  string
}

func (l *localKeyCosign) SignBlob(ctx context.Context, _ string, blob []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "cosign", "sign-blob",
		"--key", l.privPath, "--yes",
		"--use-signing-config=false", "--tlog-upload=false", "-")
	cmd.Env = append(os.Environ(), "COSIGN_PASSWORD=")
	cmd.Stdin = bytes.NewReader(blob)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return bytes.TrimSpace(out), nil
}

func (l *localKeyCosign) VerifyBlob(ctx context.Context, _ string, blob, signature []byte) error {
	sigPath := filepath.Join(os.TempDir(), "atlas-oscal-it-sig")
	if err := os.WriteFile(sigPath, signature, 0o600); err != nil {
		return err
	}
	defer func() { _ = os.Remove(sigPath) }()
	cmd := exec.CommandContext(ctx, "cosign", "verify-blob",
		"--key", l.pubPath, "--signature", sigPath,
		"--insecure-ignore-tlog=true", "-")
	cmd.Stdin = bytes.NewReader(blob)
	return cmd.Run()
}

func newLocalKeyCosign(t *testing.T) *localKeyCosign {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("cosign", "generate-key-pair")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "COSIGN_PASSWORD=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cosign generate-key-pair: %v\n%s", err, out)
	}
	return &localKeyCosign{
		privPath: filepath.Join(dir, "cosign.key"),
		pubPath:  filepath.Join(dir, "cosign.pub"),
	}
}

func requireCosignBinary(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("cosign"); err != nil {
		t.Skipf("cosign not on PATH — skipping cosign-kms bundle integration test: %v", err)
	}
}

// TestIntegration_EmbeddedMode_BundleRoundTrip is Mode B: fully hermetic.
func TestIntegration_EmbeddedMode_BundleRoundTrip(t *testing.T) {
	signer, err := NewEphemeralSigner()
	if err != nil {
		t.Fatalf("NewEphemeralSigner: %v", err)
	}
	b := testBundle(t)
	sig, err := signer.SignBundle(b)
	if err != nil {
		t.Fatalf("SignBundle: %v", err)
	}
	b.Signature = sig
	if sig.Mode != ModeEmbeddedEd25519 {
		t.Errorf("mode = %q, want embedded-ed25519", sig.Mode)
	}
	if err := VerifyBundle(b); err != nil {
		t.Errorf("embedded VerifyBundle: %v", err)
	}
	// Tamper → must fail.
	b.Members[0] = newMember("ssp.json", "system-security-plan", []byte(`{"x":1}`))
	if err := VerifyBundle(b); err == nil {
		t.Error("embedded VerifyBundle must fail on tamper")
	}
}

// TestIntegration_CosignKMSMode_BundleRoundTrip is Mode A against the real
// cosign binary (local-key KMS stand-in).
func TestIntegration_CosignKMSMode_BundleRoundTrip(t *testing.T) {
	requireCosignBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	adapter := newLocalKeyCosign(t)
	// The kmsRef is an obviously-fake-but-well-formed URI; the adapter
	// resolves the actual key from the local file, so the ref value is
	// only recorded in the manifest (and is what a real verifier would
	// pass to cosign).
	signer, err := NewKMSSigner(adapter, "awskms:///alias/atlas-oscal-integration-test")
	if err != nil {
		t.Fatalf("NewKMSSigner: %v", err)
	}
	b := testBundle(t)
	sig, err := signer.SignBundle(ctx, b)
	if err != nil {
		t.Fatalf("cosign-kms SignBundle: %v", err)
	}
	b.Signature = sig

	if sig.Mode != ModeCosignKMS {
		t.Errorf("mode = %q, want cosign-kms", sig.Mode)
	}
	if sig.KeyRef == "" {
		t.Error("cosign-kms signature must record its key_ref in the manifest")
	}

	// Round-trip verify via the real cosign binary.
	if err := VerifyBundleWithCosign(ctx, b, adapter); err != nil {
		t.Fatalf("cosign-kms VerifyBundleWithCosign: %v", err)
	}

	// Tamper a member → digest mismatch must reject (before cosign even
	// runs — see verifyCosignKMS).
	b.Members[0] = newMember("ssp.json", "system-security-plan", []byte(`{"tampered":true}`))
	if err := VerifyBundleWithCosign(ctx, b, adapter); err == nil {
		t.Fatal("cosign-kms verify must fail on a tampered member")
	}
}
