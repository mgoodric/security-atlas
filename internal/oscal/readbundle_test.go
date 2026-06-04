package oscal

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestReadBundle_RoundTripsWriteBundle(t *testing.T) {
	t.Parallel()
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

	dir := t.TempDir()
	if _, err := b.WriteBundle(dir); err != nil {
		t.Fatalf("WriteBundle: %v", err)
	}

	got, err := ReadBundle(dir)
	if err != nil {
		t.Fatalf("ReadBundle: %v", err)
	}
	if got.Signature.Mode != ModeEmbeddedEd25519 {
		t.Errorf("read mode = %q, want embedded-ed25519", got.Signature.Mode)
	}
	if len(got.Members) != len(b.Members) {
		t.Fatalf("member count = %d, want %d", len(got.Members), len(b.Members))
	}
	// A freshly read bundle must verify.
	if err := VerifyBundle(got); err != nil {
		t.Errorf("VerifyBundle(ReadBundle(...)): %v", err)
	}
	if err := VerifyBundleWithCosign(context.Background(), got, nil); err != nil {
		t.Errorf("VerifyBundleWithCosign(ReadBundle(...)): %v", err)
	}
}

func TestReadBundle_DetectsTamperedMemberFile(t *testing.T) {
	t.Parallel()
	signer, _ := NewEphemeralSigner()
	b := testBundle(t)
	sig, _ := signer.SignBundle(b)
	b.Signature = sig
	dir := t.TempDir()
	if _, err := b.WriteBundle(dir); err != nil {
		t.Fatalf("WriteBundle: %v", err)
	}
	// Tamper a member file on disk.
	if err := os.WriteFile(filepath.Join(dir, "ssp.json"), []byte(`{"evil":true}`), 0o600); err != nil {
		t.Fatalf("tamper write: %v", err)
	}
	got, err := ReadBundle(dir)
	if err != nil {
		t.Fatalf("ReadBundle: %v", err)
	}
	if err := VerifyBundle(got); err == nil {
		t.Fatal("VerifyBundle must fail after a member file is tampered on disk")
	}
}

func TestReadBundle_MissingManifest(t *testing.T) {
	t.Parallel()
	if _, err := ReadBundle(t.TempDir()); err == nil {
		t.Fatal("ReadBundle must fail when manifest.json is absent")
	}
}

func TestReadBundle_RejectsPathTraversalMember(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Hand-craft a hostile manifest whose member escapes the dir.
	manifest := `{
      "schema_version": "oscal-export-bundle/v1",
      "audit_period_id": "11111111-1111-1111-1111-111111111111",
      "frozen_at": "2026-05-01T00:00:00Z",
      "oscal_version": "1.1.2",
      "generated_at": "2026-05-14T12:00:00Z",
      "requested_by": "x",
      "members": [{"filename": "../escape.json", "model_type": "x", "sha256": "00", "size_bytes": 1}],
      "signature": {"algorithm": "ed25519", "digest": "00", "signature": "00", "public_key": "00"}
    }`
	if err := os.WriteFile(filepath.Join(dir, ManifestFilename), []byte(manifest), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if _, err := ReadBundle(dir); err == nil {
		t.Fatal("ReadBundle must reject a member filename containing a path separator")
	}
}
