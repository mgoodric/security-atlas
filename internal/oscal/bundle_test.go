package oscal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func signedTestBundle(t *testing.T) *Bundle {
	t.Helper()
	b := &Bundle{
		AuditPeriodID: uuid.New(),
		FrozenAt:      "2026-05-01T00:00:00Z",
		OSCALVersion:  OSCALVersion,
		GeneratedAt:   "2026-05-14T12:00:00Z",
		RequestedBy:   "tester",
		Members: []BundleMember{
			newMember("ssp.json", "system-security-plan", []byte(`{"system-security-plan":{}}`)),
			newMember("assessment-plan.json", "assessment-plan", []byte(`{"assessment-plan":{}}`)),
			newMember("assessment-results.json", "assessment-results", []byte(`{"assessment-results":{}}`)),
			newMember("poam.json", "plan-of-action-and-milestones", []byte(`{"plan-of-action-and-milestones":{}}`)),
		},
	}
	signer, err := NewEphemeralSigner()
	if err != nil {
		t.Fatalf("NewEphemeralSigner: %v", err)
	}
	sig, err := signer.SignBundle(b)
	if err != nil {
		t.Fatalf("SignBundle: %v", err)
	}
	b.Signature = sig
	return b
}

func TestWriteBundleWritesAllMembersAndManifest(t *testing.T) {
	b := signedTestBundle(t)
	dir := t.TempDir()
	manifestPath, err := b.WriteBundle(dir)
	if err != nil {
		t.Fatalf("WriteBundle: %v", err)
	}
	// All four OSCAL members on disk.
	for _, name := range []string{"ssp.json", "assessment-plan.json", "assessment-results.json", "poam.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("missing bundle member %s: %v", name, err)
		}
	}
	// manifest.json on disk and parseable.
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if len(m.Members) != 4 {
		t.Errorf("manifest lists %d members, want 4", len(m.Members))
	}
	if m.OSCALVersion != OSCALVersion {
		t.Errorf("manifest oscal_version = %q, want %q", m.OSCALVersion, OSCALVersion)
	}
	// The signature must be embedded in the manifest metadata (AC-5).
	if m.Signature.Algorithm != "ed25519" || m.Signature.Signature == "" {
		t.Errorf("manifest signature not embedded: %+v", m.Signature)
	}
}

func TestWriteBundleRefusesUnsignedBundle(t *testing.T) {
	b := signedTestBundle(t)
	b.Signature = Signature{} // strip the signature
	dir := t.TempDir()
	if _, err := b.WriteBundle(dir); err == nil {
		t.Fatal("WriteBundle must refuse an unsigned bundle (P0 anti-criterion)")
	}
	// Nothing should have been written.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("WriteBundle wrote %d files for an unsigned bundle, want 0", len(entries))
	}
}

func TestWriteBundleRefusesEmptyBundle(t *testing.T) {
	b := &Bundle{
		AuditPeriodID: uuid.New(),
		Signature: Signature{
			Algorithm: "ed25519",
			Signature: "deadbeef",
		},
	}
	if _, err := b.WriteBundle(t.TempDir()); err == nil {
		t.Fatal("WriteBundle must refuse an empty bundle")
	}
}

func TestManifestRoundTripsThroughJSON(t *testing.T) {
	b := signedTestBundle(t)
	m := b.Manifest()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Manifest
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.AuditPeriodID != m.AuditPeriodID {
		t.Errorf("audit_period_id round-trip mismatch")
	}
	if back.Signature.Digest != m.Signature.Digest {
		t.Errorf("signature digest round-trip mismatch")
	}
}
