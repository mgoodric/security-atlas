package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runOscalSignCmd runs the `oscal` parent command with args and returns
// combined stdout/stderr plus any error.
func runOscalSignCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newOscalSignCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

// writeUnsignedBundle writes a minimal valid unsigned bundle dir.
func writeUnsignedBundle(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	mustWrite("ssp.json", `{"system-security-plan":{"uuid":"x"}}`)
	mustWrite("poam.json", `{"plan-of-action-and-milestones":{}}`)
	mustWrite("manifest.json", `{
      "schema_version": "oscal-export-bundle/v1",
      "audit_period_id": "11111111-1111-1111-1111-111111111111",
      "frozen_at": "2026-05-01T00:00:00Z",
      "oscal_version": "1.1.2",
      "generated_at": "2026-05-14T12:00:00Z",
      "requested_by": "test",
      "members": [
        {"filename": "ssp.json", "model_type": "system-security-plan", "sha256": "x", "size_bytes": 1},
        {"filename": "poam.json", "model_type": "plan-of-action-and-milestones", "sha256": "x", "size_bytes": 1}
      ],
      "signature": {"algorithm": "", "digest": "", "signature": ""}
    }`)
	return dir
}

func TestOscalCLI_SignVerifyEmbeddedRoundTrip(t *testing.T) {
	// Force embedded mode + a fresh ephemeral key (no env signing key).
	t.Setenv("ATLAS_OSCAL_SIGNING_MODE", "embedded-ed25519")
	t.Setenv("OSCAL_SIGNING_KEY", "")
	dir := writeUnsignedBundle(t)

	signOut, err := runOscalSignCmd(t, "sign", dir)
	if err != nil {
		t.Fatalf("sign: %v\n%s", err, signOut)
	}
	if !strings.Contains(signOut, "mode=embedded-ed25519") {
		t.Errorf("sign output should report embedded mode, got:\n%s", signOut)
	}

	verifyOut, err := runOscalSignCmd(t, "verify", dir)
	if err != nil {
		t.Fatalf("verify: %v\n%s", err, verifyOut)
	}
	if !strings.Contains(verifyOut, "verifies") {
		t.Errorf("verify output should confirm, got:\n%s", verifyOut)
	}
}

func TestOscalCLI_VerifyDetectsTamper(t *testing.T) {
	t.Setenv("ATLAS_OSCAL_SIGNING_MODE", "embedded-ed25519")
	t.Setenv("OSCAL_SIGNING_KEY", "")
	dir := writeUnsignedBundle(t)
	if out, err := runOscalSignCmd(t, "sign", dir); err != nil {
		t.Fatalf("sign: %v\n%s", err, out)
	}
	// Tamper a member file on disk after signing.
	if err := os.WriteFile(filepath.Join(dir, "ssp.json"), []byte(`{"evil":true}`), 0o600); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	if _, err := runOscalSignCmd(t, "verify", dir); err == nil {
		t.Fatal("verify must fail on a tampered member")
	}
}

func TestOscalCLI_ConfigCheckEmbedded(t *testing.T) {
	t.Setenv("ATLAS_OSCAL_SIGNING_MODE", "embedded-ed25519")
	out, err := runOscalSignCmd(t, "config-check")
	if err != nil {
		t.Fatalf("config-check: %v\n%s", err, out)
	}
	if !strings.Contains(out, "embedded-ed25519") || !strings.Contains(out, "OK") {
		t.Errorf("config-check output unexpected:\n%s", out)
	}
}

func TestOscalCLI_ConfigCheckKMSNoBinaryOverrideMissing(t *testing.T) {
	// Point at a nonexistent cosign binary so Available() is false, with a
	// well-formed KMS ref — config-check must report the missing binary.
	t.Setenv("ATLAS_OSCAL_SIGNING_MODE", "cosign-kms")
	t.Setenv("ATLAS_COSIGN_KMS_REF", "awskms:///alias/atlas-oscal")
	t.Setenv("ATLAS_COSIGN_BINARY", "/nonexistent/cosign-xyz")
	_, err := runOscalSignCmd(t, "config-check")
	if err == nil {
		t.Fatal("config-check must fail when the configured cosign binary is absent")
	}
}

func TestOscalCLI_ConfigCheckRejectsKeyless(t *testing.T) {
	t.Setenv("ATLAS_OSCAL_SIGNING_MODE", "cosign-keyless")
	if _, err := runOscalSignCmd(t, "config-check"); err == nil {
		t.Fatal("config-check must reject cosign-keyless in Phase 1 (P0-413-1)")
	}
}

func TestOscalCLI_SignMissingBundle(t *testing.T) {
	t.Setenv("ATLAS_OSCAL_SIGNING_MODE", "embedded-ed25519")
	if _, err := runOscalSignCmd(t, "sign", t.TempDir()); err == nil {
		t.Fatal("sign must fail when manifest.json is absent")
	}
}
