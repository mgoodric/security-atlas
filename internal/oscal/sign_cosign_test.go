package oscal

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// fakeCosign is an in-memory stand-in for the cosign client. It "signs"
// by recording the blob and returning a deterministic token, and
// "verifies" by checking the blob matches what it signed under the same
// keyRef. This exercises the KMSSigner / dispatch / manifest logic with
// NO cosign binary — the binary is covered separately by the integration
// test.
type fakeCosign struct {
	signed    map[string][]byte // keyRef -> last blob signed
	signErr   error
	verifyErr error
	signCalls int
	verCalls  int
}

func newFakeCosign() *fakeCosign { return &fakeCosign{signed: map[string][]byte{}} }

func (f *fakeCosign) SignBlob(_ context.Context, kmsRef string, blob []byte) ([]byte, error) {
	f.signCalls++
	if f.signErr != nil {
		return nil, f.signErr
	}
	cp := make([]byte, len(blob))
	copy(cp, blob)
	f.signed[kmsRef] = cp
	return []byte("fake-cosign-sig-for-" + kmsRef), nil
}

func (f *fakeCosign) VerifyBlob(_ context.Context, keyRef string, blob, signature []byte) error {
	f.verCalls++
	if f.verifyErr != nil {
		return f.verifyErr
	}
	if string(signature) != "fake-cosign-sig-for-"+keyRef {
		return errors.New("fake: signature does not match keyRef")
	}
	prev, ok := f.signed[keyRef]
	if !ok || string(prev) != string(blob) {
		return errors.New("fake: blob does not match what was signed")
	}
	return nil
}

const testKMSRef = "awskms:///alias/atlas-oscal-test"

func TestKMSSigner_SignAndVerifyRoundTrips(t *testing.T) {
	t.Parallel()
	fc := newFakeCosign()
	signer, err := NewKMSSigner(fc, testKMSRef)
	if err != nil {
		t.Fatalf("NewKMSSigner: %v", err)
	}
	b := testBundle(t)
	sig, err := signer.SignBundle(context.Background(), b)
	if err != nil {
		t.Fatalf("SignBundle: %v", err)
	}
	b.Signature = sig

	if sig.Mode != ModeCosignKMS {
		t.Errorf("mode = %q, want %q", sig.Mode, ModeCosignKMS)
	}
	if sig.Algorithm != "cosign-kms" {
		t.Errorf("algorithm = %q, want cosign-kms", sig.Algorithm)
	}
	if sig.KeyRef != testKMSRef {
		t.Errorf("key_ref = %q, want %q", sig.KeyRef, testKMSRef)
	}
	if sig.PublicKey != "" {
		t.Errorf("cosign-kms must not carry an embedded public key, got %q", sig.PublicKey)
	}
	if err := VerifyBundleWithCosign(context.Background(), b, fc); err != nil {
		t.Errorf("VerifyBundleWithCosign on a freshly KMS-signed bundle: %v", err)
	}
}

func TestKMSSigner_DetectsTamperBeforeCosign(t *testing.T) {
	t.Parallel()
	fc := newFakeCosign()
	signer, _ := NewKMSSigner(fc, testKMSRef)
	b := testBundle(t)
	sig, _ := signer.SignBundle(context.Background(), b)
	b.Signature = sig
	// Tamper a member after signing — the recomputed digest no longer
	// matches the recorded digest, so verification rejects WITHOUT even
	// calling cosign.
	b.Members[0] = newMember("ssp.json", "system-security-plan", []byte(`{"system-security-plan":{"tampered":true}}`))
	verCallsBefore := fc.verCalls
	if err := VerifyBundleWithCosign(context.Background(), b, fc); err == nil {
		t.Fatal("VerifyBundleWithCosign must fail on a tampered member")
	}
	if fc.verCalls != verCallsBefore {
		t.Error("digest mismatch must reject before invoking cosign verify")
	}
}

func TestKMSSigner_VerifyFailsOnBadSignature(t *testing.T) {
	t.Parallel()
	fc := newFakeCosign()
	signer, _ := NewKMSSigner(fc, testKMSRef)
	b := testBundle(t)
	sig, _ := signer.SignBundle(context.Background(), b)
	sig.Signature = "fake-cosign-sig-for-wrong-key"
	b.Signature = sig
	if err := VerifyBundleWithCosign(context.Background(), b, fc); err == nil {
		t.Fatal("VerifyBundleWithCosign must fail when cosign rejects the signature")
	}
}

func TestNewKMSSigner_Validation(t *testing.T) {
	t.Parallel()
	if _, err := NewKMSSigner(nil, testKMSRef); err == nil {
		t.Error("NewKMSSigner must reject a nil client")
	}
	if _, err := NewKMSSigner(newFakeCosign(), ""); !errors.Is(err, ErrNoKMSRef) {
		t.Errorf("NewKMSSigner with empty ref: err = %v, want ErrNoKMSRef", err)
	}
}

func TestKMSSigner_RejectsEmptyBundle(t *testing.T) {
	t.Parallel()
	signer, _ := NewKMSSigner(newFakeCosign(), testKMSRef)
	if _, err := signer.SignBundle(context.Background(), &Bundle{AuditPeriodID: uuid.New()}); err == nil {
		t.Error("SignBundle must reject an empty bundle")
	}
}

func TestKMSSigner_SignPropagatesCosignError(t *testing.T) {
	t.Parallel()
	fc := newFakeCosign()
	fc.signErr = errors.New("AccessDenied: kms:Sign")
	signer, _ := NewKMSSigner(fc, testKMSRef)
	_, err := signer.SignBundle(context.Background(), testBundle(t))
	if err == nil {
		t.Fatal("SignBundle must surface a cosign error")
	}
}

func TestVerifyCosignKMS_RequiresVerifier(t *testing.T) {
	t.Parallel()
	fc := newFakeCosign()
	signer, _ := NewKMSSigner(fc, testKMSRef)
	b := testBundle(t)
	sig, _ := signer.SignBundle(context.Background(), b)
	b.Signature = sig
	// Passing nil verifier must fail closed, not pass.
	if err := VerifyBundleWithCosign(context.Background(), b, nil); !errors.Is(err, ErrCosignVerifierRequired) {
		t.Fatalf("err = %v, want ErrCosignVerifierRequired", err)
	}
	// The mode-only VerifyBundle (no cosign dependency) must also refuse a
	// cosign bundle rather than silently passing.
	if err := VerifyBundle(b); !errors.Is(err, ErrCosignVerifierRequired) {
		t.Fatalf("VerifyBundle on a cosign bundle: err = %v, want ErrCosignVerifierRequired", err)
	}
}

func TestVerifyCosignKMS_MalformedSignatureFields(t *testing.T) {
	t.Parallel()
	fc := newFakeCosign()
	signer, _ := NewKMSSigner(fc, testKMSRef)
	base := testBundle(t)
	good, _ := signer.SignBundle(context.Background(), base)

	cases := map[string]func(s Signature) Signature{
		"missing key_ref": func(s Signature) Signature { s.KeyRef = ""; return s },
		"empty signature": func(s Signature) Signature { s.Signature = ""; return s },
		"bad algorithm":   func(s Signature) Signature { s.Algorithm = "ed25519"; return s },
		"malformed digest": func(s Signature) Signature {
			s.Digest = "not-hex"
			return s
		},
	}
	for name, mutate := range cases {
		b := testBundle(t)
		b.Signature = mutate(good)
		if err := VerifyBundleWithCosign(context.Background(), b, fc); err == nil {
			t.Errorf("%s: VerifyBundleWithCosign must fail", name)
		}
	}
}

// TestBackwardCompat_OldManifestVerifies is the load-bearing P0-413-4
// test: a bundle whose manifest was produced BEFORE the Mode field
// existed (no `mode` key in the JSON) must still verify. We simulate the
// old format by marshaling a manifest, deleting the `mode` key from the
// signature object, unmarshaling, and verifying.
func TestBackwardCompat_OldManifestVerifies(t *testing.T) {
	t.Parallel()
	// Produce a current embedded-mode signed bundle.
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

	// Round-trip the manifest through JSON, then strip the `mode` key from
	// the signature to mimic a pre-413 on-disk manifest.
	manifestBytes, err := json.Marshal(b.Manifest())
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(manifestBytes, &raw); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	var sigObj map[string]json.RawMessage
	if err := json.Unmarshal(raw["signature"], &sigObj); err != nil {
		t.Fatalf("unmarshal signature: %v", err)
	}
	if _, ok := sigObj["mode"]; !ok {
		t.Fatal("precondition: current manifest should carry a mode key")
	}
	delete(sigObj, "mode") // simulate pre-413 manifest

	// Reconstruct the Signature from the mode-less object.
	sigless, _ := json.Marshal(sigObj)
	var oldSig Signature
	if err := json.Unmarshal(sigless, &oldSig); err != nil {
		t.Fatalf("unmarshal mode-less signature: %v", err)
	}
	if oldSig.Mode != "" {
		t.Fatalf("precondition: simulated old signature must have empty Mode, got %q", oldSig.Mode)
	}

	// A bundle reconstructed with the mode-less (old-format) signature must
	// still verify via BOTH the embedded-only VerifyBundle and the
	// cosign-aware VerifyBundleWithCosign — dispatch defaults to embedded.
	old := testBundle(t)
	old.Signature = oldSig
	if err := VerifyBundle(old); err != nil {
		t.Errorf("VerifyBundle on a pre-413 (mode-less) bundle: %v", err)
	}
	if err := VerifyBundleWithCosign(context.Background(), old, nil); err != nil {
		t.Errorf("VerifyBundleWithCosign on a pre-413 (mode-less) bundle: %v", err)
	}
}

func TestResolveMode(t *testing.T) {
	t.Parallel()
	if got := ResolveMode(""); got != ModeEmbeddedEd25519 {
		t.Errorf("ResolveMode(empty) = %q, want %q (backward-compat)", got, ModeEmbeddedEd25519)
	}
	if got := ResolveMode(ModeCosignKMS); got != ModeCosignKMS {
		t.Errorf("ResolveMode(kms) = %q, want kms", got)
	}
}

func TestVerifyBundle_KeylessNotSupported(t *testing.T) {
	t.Parallel()
	b := testBundle(t)
	b.Signature = Signature{Mode: ModeCosignKeyless}
	if err := VerifyBundle(b); err == nil {
		t.Fatal("VerifyBundle must reject the keyless mode in Phase 1")
	}
	if err := VerifyBundleWithCosign(context.Background(), b, newFakeCosign()); err == nil {
		t.Fatal("VerifyBundleWithCosign must reject the keyless mode in Phase 1")
	}
}

func TestVerifyBundle_UnknownMode(t *testing.T) {
	t.Parallel()
	b := testBundle(t)
	b.Signature = Signature{Mode: Mode("martian-signing")}
	if err := VerifyBundle(b); err == nil {
		t.Fatal("VerifyBundle must reject an unknown mode")
	}
	if err := VerifyBundleWithCosign(context.Background(), b, newFakeCosign()); err == nil {
		t.Fatal("VerifyBundleWithCosign must reject an unknown mode")
	}
}
