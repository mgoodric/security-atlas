//go:build integration

// Bundle-level keyless sign→write→read→verify round-trip integration test
// (slice 414 / 368b / ADR-0016, AC-3).
//
// Keyless's real backend is an OPERATOR-RUN PRIVATE Sigstore (Fulcio +
// Rekor) — a stack that cannot be stood up in CI, and which the slice's
// test strategy explicitly says NOT to require network access to. So this
// integration test STUBS the Fulcio cert issuance + Rekor upload at the
// cosign-client boundary (the fakeKeylessBackend, with a real test cert
// from a test CA) and exercises the FULL on-disk path that a real export
// takes: sign → WriteBundle (manifest to disk) → ReadBundle → verify. That
// path — the deterministic digest, the manifest persistence of the keyless
// attestation, and the dispatch — is the production code under test; only
// the Fulcio/Rekor leaf is faked. This is the keyless analogue of the
// cosign-kms local-key integration test.
package oscal

import (
	"context"
	"testing"
)

func TestIntegration_KeylessMode_BundleOnDiskRoundTrip(t *testing.T) {
	cert := makeFulcioLikeCert(t)
	backend := newFakeKeylessBackend(cert)
	signer := newKeylessSignerForTest(t, backend, &fakeTokenSource{token: "header.payload.sig"})

	b := testBundle(t)
	sig, err := signer.SignBundle(context.Background(), b)
	if err != nil {
		t.Fatalf("keyless SignBundle: %v", err)
	}
	b.Signature = sig

	// Persist to disk and read back — the manifest must carry the keyless
	// attestation across the JSON round-trip (the drift surface).
	dir := t.TempDir()
	if _, err := b.WriteBundle(dir); err != nil {
		t.Fatalf("WriteBundle: %v", err)
	}
	read, err := ReadBundle(dir)
	if err != nil {
		t.Fatalf("ReadBundle: %v", err)
	}

	if read.Signature.Mode != ModeCosignKeyless {
		t.Errorf("read mode = %q, want cosign-keyless", read.Signature.Mode)
	}
	if read.Signature.Keyless == nil {
		t.Fatal("read manifest lost its keyless attestation")
	}
	if read.Signature.Keyless.RekorLogIndex != sig.Keyless.RekorLogIndex {
		t.Errorf("read rekor index = %d, want %d", read.Signature.Keyless.RekorLogIndex, sig.Keyless.RekorLogIndex)
	}
	if read.Signature.Keyless.CertificateIdentity != testKeylessIdentity {
		t.Errorf("read cert identity = %q, want %q", read.Signature.Keyless.CertificateIdentity, testKeylessIdentity)
	}

	// Verify the read-back bundle against the (fake) trust root.
	if err := VerifyBundleWithCosign(context.Background(), read, kvOnly{NewCosignKeylessAdapter(backend)}); err != nil {
		t.Fatalf("keyless verify of the read-back bundle: %v", err)
	}

	// Tamper a member on disk-read bundle → digest mismatch must reject
	// before cosign runs.
	read.Members[0] = newMember("ssp.json", "system-security-plan", []byte(`{"tampered":true}`))
	if err := VerifyBundleWithCosign(context.Background(), read, kvOnly{NewCosignKeylessAdapter(backend)}); err == nil {
		t.Fatal("keyless verify must fail on a tampered member")
	}
}
