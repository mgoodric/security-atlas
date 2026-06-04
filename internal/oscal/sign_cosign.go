package oscal

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
)

// CosignSigner is the minimal cosign-blob signing surface the export
// pipeline needs. internal/oscal/cosign.Client satisfies it. Declaring
// it as an interface here keeps the oscal package's cosign dependency
// injectable (and unit-testable without the cosign binary) and avoids a
// hard import cycle — the concrete client lives in the subpackage.
type CosignSigner interface {
	// SignBlob signs blob with the KMS key referenced by kmsRef and
	// returns the detached signature bytes.
	SignBlob(ctx context.Context, kmsRef string, blob []byte) ([]byte, error)
}

// CosignVerifier is the minimal cosign-blob verification surface.
// internal/oscal/cosign.Client satisfies it.
type CosignVerifier interface {
	// VerifyBlob returns nil iff signature is a valid cosign signature
	// over blob for the key referenced by keyRef.
	VerifyBlob(ctx context.Context, keyRef string, blob, signature []byte) error
}

// KMSSigner produces ModeCosignKMS signatures over an export bundle by
// shelling out (via the injected CosignSigner) to `cosign sign-blob`
// with a cloud-KMS key reference. It is the cosign-kms-mode counterpart
// to the in-process *Signer.
//
// The blob signed is the SAME deterministic bundle digest the embedded
// path signs (bundleDigest) — the digest derivation is mode-independent,
// so the tamper-detection property (digest changes if any member's bytes
// change) is identical across modes.
type KMSSigner struct {
	client CosignSigner
	kmsRef string
}

// ErrNoKMSRef is returned when a KMSSigner is constructed without a key
// reference.
var ErrNoKMSRef = errors.New("oscal: cosign-kms signer requires a non-empty KMS key reference")

// NewKMSSigner wires a KMSSigner from a cosign client and a KMS key
// reference (e.g. awskms:///alias/atlas-oscal). The reference's
// well-formedness is validated by the cosign client at sign time.
func NewKMSSigner(client CosignSigner, kmsRef string) (*KMSSigner, error) {
	if client == nil {
		return nil, errors.New("oscal: cosign-kms signer requires a cosign client")
	}
	if kmsRef == "" {
		return nil, ErrNoKMSRef
	}
	return &KMSSigner{client: client, kmsRef: kmsRef}, nil
}

// SignBundle signs the bundle digest via cosign + KMS and returns a
// ModeCosignKMS Signature. The digest is computed identically to the
// embedded path; the manifest records the KMS key reference so a
// verifier knows which key to pass to `cosign verify-blob --key`.
func (s *KMSSigner) SignBundle(ctx context.Context, b *Bundle) (Signature, error) {
	if s == nil || s.client == nil {
		return Signature{}, errors.New("oscal: nil cosign-kms signer")
	}
	if len(b.Members) == 0 {
		return Signature{}, errors.New("oscal: cannot sign an empty bundle")
	}
	digest := bundleDigest(b)
	sig, err := s.client.SignBlob(ctx, s.kmsRef, digest[:])
	if err != nil {
		return Signature{}, fmt.Errorf("oscal: cosign-kms sign: %w", err)
	}
	return Signature{
		Mode:      ModeCosignKMS,
		Algorithm: "cosign-kms",
		KeyRef:    s.kmsRef,
		Digest:    hex.EncodeToString(digest[:]),
		Signature: string(sig),
	}, nil
}

// VerifyBundleWithCosign verifies a bundle, dispatching on its recorded
// Mode and using the supplied CosignVerifier for cosign modes. Embedded
// bundles are verified in-process (the verifier argument is unused for
// them), so this is a safe superset of VerifyBundle that also handles
// ModeCosignKMS — making it the function an auditor-facing verifier
// (the CLI `verify`) calls.
//
// For ModeCosignKMS it re-derives the deterministic digest, then asks
// cosign to verify the recorded signature against that digest using the
// manifest's KeyRef. The digest is recomputed from the bundle members
// (not trusted from the manifest), so a member-tamper is caught before
// cosign even runs — the same belt-and-braces property the embedded path
// has.
func VerifyBundleWithCosign(ctx context.Context, b *Bundle, verifier CosignVerifier) error {
	switch ResolveMode(b.Signature.Mode) {
	case ModeEmbeddedEd25519:
		return verifyEmbedded(b)
	case ModeCosignKMS:
		return verifyCosignKMS(ctx, b, verifier)
	case ModeCosignKeyless:
		return fmt.Errorf("oscal: signing mode %q is not supported in this build (slice 414)", ModeCosignKeyless)
	default:
		return fmt.Errorf("oscal: unknown signing mode %q", b.Signature.Mode)
	}
}

func verifyCosignKMS(ctx context.Context, b *Bundle, verifier CosignVerifier) error {
	if verifier == nil {
		return ErrCosignVerifierRequired
	}
	sig := b.Signature
	if sig.Algorithm != "cosign-kms" {
		return fmt.Errorf("oscal: cosign-kms mode with unexpected algorithm %q", sig.Algorithm)
	}
	if sig.KeyRef == "" {
		return errors.New("oscal: cosign-kms signature is missing its key_ref")
	}
	if sig.Signature == "" {
		return errors.New("oscal: cosign-kms signature is empty")
	}
	// Recompute the digest from the actual members — do NOT trust the
	// manifest's recorded digest. A member-tamper changes this and the
	// recorded digest will not match, which we reject before invoking
	// cosign.
	want := bundleDigest(b)
	gotDigest, err := hex.DecodeString(sig.Digest)
	if err != nil || len(gotDigest) != len(want) {
		return errors.New("oscal: malformed digest in cosign-kms signature")
	}
	for i := range want {
		if want[i] != gotDigest[i] {
			return errors.New("oscal: bundle digest mismatch — members changed since signing")
		}
	}
	if err := verifier.VerifyBlob(ctx, sig.KeyRef, want[:], []byte(sig.Signature)); err != nil {
		return fmt.Errorf("oscal: cosign-kms verification failed: %w", err)
	}
	return nil
}
