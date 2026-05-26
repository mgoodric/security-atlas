// Slice 285 — branch-coverage fill-ins for the remaining uncovered
// error paths in the OSCAL export pipeline. These were the residual
// statements left after the proto-conversion + bridge-wrapper test
// additions; this file targets the load-bearing rejection branches:
//
//   - VerifyBundle: wrong algorithm / malformed public key / malformed
//     signature bytes / malformed digest / digest-length mismatch.
//   - NewSigner: happy path with a valid ed25519 private key.
//   - WriteBundle: refuses a bundle whose Signature has Algorithm set
//     but Signature field empty (and the inverse) — the AND in the
//     guard must reject EITHER zero.
//   - uuidFromPg: zero-valued pgtype.UUID yields uuid.Nil (the
//     `if !p.Valid` branch).
//   - Export: rejects an ExportInput whose audit period id is uuid.Nil
//     BEFORE touching the pool (the function returns early; verifies
//     the validation order — invariant 10 is enforced in Aggregate,
//     but the uuid.Nil sentinel is rejected first to give a clearer
//     error).
package oscal

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestVerifyBundle_RejectsUnsupportedAlgorithm(t *testing.T) {
	b := testBundle(t)
	b.Signature = Signature{Algorithm: "rsa-pss"}
	err := VerifyBundle(b)
	if err == nil || !strings.Contains(err.Error(), "unsupported signature algorithm") {
		t.Errorf("VerifyBundle on unsupported algorithm = %v, want unsupported-algorithm error", err)
	}
}

func TestVerifyBundle_RejectsMalformedPublicKey(t *testing.T) {
	b := testBundle(t)
	// Wrong length even though the hex parses.
	b.Signature = Signature{
		Algorithm: "ed25519",
		PublicKey: "deadbeef",
		Signature: "00",
		Digest:    "00",
	}
	if err := VerifyBundle(b); err == nil || !strings.Contains(err.Error(), "malformed public key") {
		t.Errorf("VerifyBundle on short public key = %v, want malformed-public-key error", err)
	}

	// Non-hex public key.
	b.Signature.PublicKey = "zzzz"
	if err := VerifyBundle(b); err == nil || !strings.Contains(err.Error(), "malformed public key") {
		t.Errorf("VerifyBundle on non-hex public key = %v, want malformed-public-key error", err)
	}
}

func TestVerifyBundle_RejectsMalformedSignatureBytes(t *testing.T) {
	// Need a real-shape public key first (correct length) so we reach
	// the signature-bytes decode step.
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	b := testBundle(t)
	b.Signature = Signature{
		Algorithm: "ed25519",
		PublicKey: hexEncode(pub),
		Signature: "zzzz",
		Digest:    "00",
	}
	if err := VerifyBundle(b); err == nil || !strings.Contains(err.Error(), "malformed signature bytes") {
		t.Errorf("VerifyBundle on non-hex signature = %v, want malformed-signature-bytes error", err)
	}
}

func TestVerifyBundle_RejectsMalformedDigest(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	b := testBundle(t)

	// Wrong length but hex-parseable -> "malformed digest".
	b.Signature = Signature{
		Algorithm: "ed25519",
		PublicKey: hexEncode(pub),
		Signature: "00",
		Digest:    "deadbeef", // only 4 bytes, not 32.
	}
	if err := VerifyBundle(b); err == nil || !strings.Contains(err.Error(), "malformed digest") {
		t.Errorf("VerifyBundle on short digest = %v, want malformed-digest error", err)
	}

	// Non-hex digest.
	b.Signature.Digest = "not-hex-at-all"
	if err := VerifyBundle(b); err == nil || !strings.Contains(err.Error(), "malformed digest") {
		t.Errorf("VerifyBundle on non-hex digest = %v, want malformed-digest error", err)
	}
}

func TestNewSigner_AcceptsValidKey(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	signer, err := NewSigner(priv)
	if err != nil {
		t.Fatalf("NewSigner with a valid key = %v, want nil", err)
	}
	if signer == nil {
		t.Fatal("NewSigner returned nil signer for a valid key")
	}
	// And the signer must actually sign — exercise the happy path through
	// SignBundle so the round-trip is end-to-end via NewSigner (not just
	// the test ephemeral signer).
	b := testBundle(t)
	sig, err := signer.SignBundle(b)
	if err != nil {
		t.Fatalf("SignBundle via NewSigner-built signer: %v", err)
	}
	if sig.Algorithm != "ed25519" {
		t.Errorf("Signature algorithm = %q, want ed25519", sig.Algorithm)
	}
}

func TestWriteBundle_RejectsPartiallyZeroSignature(t *testing.T) {
	// Algorithm-only set, Signature field empty -> still refuses (the
	// guard uses ||, so EITHER zero rejects).
	b := signedTestBundle(t)
	b.Signature = Signature{Algorithm: "ed25519"}
	if _, err := b.WriteBundle(t.TempDir()); err == nil {
		t.Fatal("WriteBundle must refuse a bundle whose Signature field is empty")
	}

	// Signature-only set, Algorithm empty -> still refuses.
	b.Signature = Signature{Signature: "deadbeef"}
	if _, err := b.WriteBundle(t.TempDir()); err == nil {
		t.Fatal("WriteBundle must refuse a bundle whose Signature.Algorithm is empty")
	}
}

func TestUUIDFromPg_InvalidYieldsNil(t *testing.T) {
	// !Valid -> uuid.Nil branch.
	got := uuidFromPg(pgtype.UUID{Valid: false})
	if got != uuid.Nil {
		t.Errorf("uuidFromPg(invalid) = %v, want uuid.Nil", got)
	}
	// Valid -> the wrapped value.
	want := uuid.New()
	got = uuidFromPg(pgtype.UUID{Bytes: want, Valid: true})
	if got != want {
		t.Errorf("uuidFromPg(valid) = %v, want %v", got, want)
	}
}

func TestExport_RejectsNilAuditPeriodIDBeforeTouchingPool(t *testing.T) {
	// A nil pool would crash Aggregate, but Export must short-circuit
	// on the uuid.Nil sentinel BEFORE calling Aggregate. We pass
	// (nil pool, nil bridge, zero-value signer): if Export reaches
	// Aggregate, it will nil-panic. The test passes if it returns an
	// error cleanly.
	e := NewExporter(nil, nil, &Signer{})
	_, err := e.Export(context.Background(), ExportInput{AuditPeriodID: uuid.Nil})
	if err == nil {
		t.Fatal("Export must reject AuditPeriodID == uuid.Nil")
	}
	// And it must NOT be one of the deeper-layer errors — confirms the
	// short-circuit.
	if errors.Is(err, ErrPeriodNotFrozen) || errors.Is(err, ErrPeriodNotFound) {
		t.Errorf("Export returned a deeper-layer error %v, want the uuid.Nil short-circuit message", err)
	}
}

// hexEncode is the same encoding the sign.go file uses; pulled into the
// test file so the assertions read naturally without an extra import.
func hexEncode(b []byte) string {
	const hexchars = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, x := range b {
		out[i*2] = hexchars[x>>4]
		out[i*2+1] = hexchars[x&0xf]
	}
	return string(out)
}
