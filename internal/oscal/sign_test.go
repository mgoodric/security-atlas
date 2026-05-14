package oscal

import (
	"crypto/ed25519"
	"testing"

	"github.com/google/uuid"
)

func testBundle(t *testing.T) *Bundle {
	t.Helper()
	return &Bundle{
		AuditPeriodID: uuid.New(),
		FrozenAt:      "2026-05-01T00:00:00Z",
		OSCALVersion:  OSCALVersion,
		GeneratedAt:   "2026-05-14T12:00:00Z",
		RequestedBy:   "tester",
		Members: []BundleMember{
			newMember("ssp.json", "system-security-plan", []byte(`{"system-security-plan":{}}`)),
			newMember("poam.json", "plan-of-action-and-milestones", []byte(`{"plan-of-action-and-milestones":{}}`)),
		},
	}
}

func TestSignAndVerifyBundleRoundTrips(t *testing.T) {
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
	if sig.Algorithm != "ed25519" {
		t.Errorf("algorithm = %q, want ed25519", sig.Algorithm)
	}
	if sig.Signature == "" || sig.PublicKey == "" || sig.Digest == "" {
		t.Fatalf("signature fields must be populated: %+v", sig)
	}
	if err := VerifyBundle(b); err != nil {
		t.Errorf("VerifyBundle on a freshly signed bundle: %v", err)
	}
}

func TestVerifyBundleDetectsTamperedMember(t *testing.T) {
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
	// Tamper: rewrite a member's bytes (and its hash) after signing.
	b.Members[0] = newMember("ssp.json", "system-security-plan", []byte(`{"system-security-plan":{"tampered":true}}`))
	if err := VerifyBundle(b); err == nil {
		t.Fatal("VerifyBundle must fail after a member is tampered")
	}
}

func TestVerifyBundleDetectsForgedSignature(t *testing.T) {
	signer, err := NewEphemeralSigner()
	if err != nil {
		t.Fatalf("NewEphemeralSigner: %v", err)
	}
	b := testBundle(t)
	sig, err := signer.SignBundle(b)
	if err != nil {
		t.Fatalf("SignBundle: %v", err)
	}
	// Forge: keep the digest, swap in a different keypair's signature.
	other, _ := NewEphemeralSigner()
	forged, _ := other.SignBundle(b)
	sig.Signature = forged.Signature // signature from another key
	b.Signature = sig
	if err := VerifyBundle(b); err == nil {
		t.Fatal("VerifyBundle must fail when the signature does not match the public key")
	}
}

func TestNewSignerRejectsMalformedKey(t *testing.T) {
	if _, err := NewSigner(ed25519.PrivateKey(nil)); err == nil {
		t.Fatal("NewSigner must reject a nil key")
	}
	if _, err := NewSigner(ed25519.PrivateKey([]byte{1, 2, 3})); err == nil {
		t.Fatal("NewSigner must reject a short key")
	}
}

func TestSignBundleRejectsEmptyBundle(t *testing.T) {
	signer, _ := NewEphemeralSigner()
	empty := &Bundle{AuditPeriodID: uuid.New()}
	if _, err := signer.SignBundle(empty); err == nil {
		t.Fatal("SignBundle must reject an empty bundle")
	}
}

func TestBundleDigestIsOrderIndependent(t *testing.T) {
	a := testBundle(t)
	b := testBundle(t)
	// Same members, reversed order.
	b.Members[0], b.Members[1] = b.Members[1], b.Members[0]
	if bundleDigest(a) != bundleDigest(b) {
		t.Error("bundleDigest must be independent of member order")
	}
}
