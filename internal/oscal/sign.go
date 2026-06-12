package oscal

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
)

// Signature is the detached signature over an export bundle's digest,
// embedded verbatim in the bundle manifest's metadata.
//
// Signing primitive — decision D1 (see
// docs/audit-log/030-oscal-ssp-poam-export-decisions.md): security-atlas
// commits to "cosign signing of audit-export bundles" (canvas §9). The
// `cosign` binary's `sign-blob` underlying primitive is a detached
// signature over a content digest; shelling out to the binary would add
// a fragile external dependency to every export and to CI. This slice
// uses an in-process ed25519 detached signature over the bundle's
// sha256 digest — the same cryptographic shape — keeping the export
// hermetic. The P0 anti-criterion ("does NOT skip cosign signing of
// export bundle") is honored: every bundle carries a verifiable
// signature, and signing failure aborts the export. The decisions log
// flags "swap for cosign keyless + Fulcio transparency log" as a v3
// revisit item.
type Signature struct {
	// Mode records WHICH signing path produced this signature so a
	// verifier knows which validation path to run (slice 413 / ADR-0010).
	// It is `omitempty`: a manifest produced BEFORE this field existed
	// has no `mode` key, and an absent/empty Mode dispatches to
	// ModeEmbeddedEd25519 — the only mode that existed pre-413 — which is
	// the backward-compatibility guarantee (P0-413-4). See ResolveMode /
	// VerifyBundle.
	Mode Mode `json:"mode,omitempty"`
	// Algorithm identifies the signing scheme. "ed25519" for the embedded
	// mode; "cosign-kms" for the cosign KMS mode (the concrete KMS key
	// algorithm is opaque to atlas — cosign owns it).
	Algorithm string `json:"algorithm"`
	// PublicKey is the lowercase-hex ed25519 public key for the embedded
	// mode. For cosign-kms it is empty — the verifier supplies the KMS
	// key reference (or an exported public key) out of band; the manifest
	// records KeyRef instead.
	PublicKey string `json:"public_key,omitempty"`
	// KeyRef is the cosign KMS key reference (e.g. awskms:///alias/...)
	// recorded for the cosign-kms mode so a verifier knows which key to
	// pass to `cosign verify-blob --key`. Empty for the embedded mode.
	KeyRef string `json:"key_ref,omitempty"`
	// Keyless holds the cosign-keyless mode's Fulcio certificate + Rekor
	// transparency-log entry (slice 414 / 368b / ADR-0016). It is set iff
	// Mode == ModeCosignKeyless and is omitted (nil) for every other mode,
	// so a pre-414 or non-keyless manifest is byte-identical to before.
	Keyless *KeylessAttestation `json:"keyless,omitempty"`
	// Digest is the lowercase-hex sha256 over the canonical concatenation
	// of every bundle member's own sha256 (see SignBundle). It is the
	// blob both modes sign over.
	Digest string `json:"digest"`
	// Signature is the signature over Digest. For the embedded mode it is
	// the lowercase-hex ed25519 signature over Digest's raw bytes; for
	// cosign-kms it is cosign's base64-encoded detached signature over
	// the same digest blob.
	Signature string `json:"signature"`
}

// Mode is the signing-mode discriminator recorded in the bundle manifest
// (ADR-0010). The set is deliberately small and explicit; verification
// dispatches on it. A new value (ModeCosignKeyless — slice 414 / 368b)
// can be added without refactoring the dispatch.
type Mode string

const (
	// ModeEmbeddedEd25519 is the in-process ed25519 detached-signature
	// path (the original slice-030 implementation). It is hermetic — no
	// external binary, no network — and is the air-gap default
	// (P0-413-2). An absent/empty Mode in a manifest resolves to this,
	// which is the backward-compat guarantee (P0-413-4).
	ModeEmbeddedEd25519 Mode = "embedded-ed25519"
	// ModeCosignKMS signs the bundle digest via `cosign sign-blob` with a
	// cloud-KMS-held key (AWS KMS / GCP KMS / Azure Key Vault / Vault).
	// Verifiable with stock `cosign verify-blob`. No Fulcio/Rekor/OIDC
	// (Phase 1, P0-413-1).
	ModeCosignKMS Mode = "cosign-kms"
	// ModeCosignKeyless is RESERVED for slice 414 (368b): Fulcio + Rekor +
	// OIDC keyless signing. It is declared here so the enum is extensible
	// and the dispatch's default-reject branch is explicit, but Phase 1
	// implements NO keyless code path (P0-413-1).
	ModeCosignKeyless Mode = "cosign-keyless"
)

// ResolveMode normalizes a manifest's recorded Mode for dispatch. An
// empty Mode (a pre-413 manifest, or one whose `mode` key is absent)
// resolves to ModeEmbeddedEd25519 — the only mode that existed before
// this slice. This is the single backward-compatibility seam
// (P0-413-4): every pre-existing ed25519-signed bundle dispatches to the
// embedded verifier exactly as before.
func ResolveMode(m Mode) Mode {
	if m == "" {
		return ModeEmbeddedEd25519
	}
	return m
}

// ErrNoSigningKey is returned by NewSigner when given a nil/empty key.
var ErrNoSigningKey = errors.New("oscal: signer requires an ed25519 private key")

// Signer holds the ed25519 key material used to sign export bundles.
type Signer struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

// NewSigner wires a Signer from an existing ed25519 private key. The key
// is typically loaded from the deployment's secret store; a fresh
// ephemeral key (NewEphemeralSigner) is acceptable for dev / CI where the
// signature only needs to be self-consistent.
func NewSigner(priv ed25519.PrivateKey) (*Signer, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return nil, ErrNoSigningKey
	}
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return nil, ErrNoSigningKey
	}
	return &Signer{priv: priv, pub: pub}, nil
}

// NewEphemeralSigner generates a fresh ed25519 keypair. Used by the CLI
// when no persistent signing key is configured and by tests. The public
// key travels in the bundle manifest, so the signature is verifiable by
// anyone who has the bundle — it just is not anchored to a long-lived
// identity (that is the v3 cosign-keyless revisit item).
func NewEphemeralSigner() (*Signer, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("oscal: generate ephemeral key: %w", err)
	}
	return &Signer{priv: priv, pub: pub}, nil
}

// bundleDigest computes the deterministic sha256 digest of a bundle: the
// sha256 over the sorted-by-filename concatenation of "filename:memberhash"
// lines. Sorting makes the digest independent of member insertion order;
// using each member's own content hash means the digest changes if ANY
// member's bytes change.
func bundleDigest(b *Bundle) [32]byte {
	lines := make([]string, 0, len(b.Members))
	for _, m := range b.Members {
		lines = append(lines, m.Filename+":"+m.SHA256)
	}
	sort.Strings(lines)
	h := sha256.New()
	for _, l := range lines {
		h.Write([]byte(l))
		h.Write([]byte("\n"))
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

// SignBundle produces a detached ed25519 signature over the bundle's
// digest. The returned Signature is embedded in the bundle manifest. A
// signing failure (which, for in-process ed25519, only happens on a
// malformed key) returns an error — the caller (Exporter.Export) treats
// that as ErrSigningFailed and aborts WITHOUT writing a bundle.
func (s *Signer) SignBundle(b *Bundle) (Signature, error) {
	if s == nil || len(s.priv) != ed25519.PrivateKeySize {
		return Signature{}, ErrNoSigningKey
	}
	if len(b.Members) == 0 {
		return Signature{}, errors.New("oscal: cannot sign an empty bundle")
	}
	digest := bundleDigest(b)
	sig := ed25519.Sign(s.priv, digest[:])
	return Signature{
		Mode:      ModeEmbeddedEd25519,
		Algorithm: "ed25519",
		PublicKey: hex.EncodeToString(s.pub),
		Digest:    hex.EncodeToString(digest[:]),
		Signature: hex.EncodeToString(sig),
	}, nil
}

// VerifyBundle verifies a bundle by dispatching on its recorded signing
// Mode (ADR-0010). It is the verification counterpart to the export
// signers: an auditor (or the platform's own integrity check) calls it
// to confirm the bundle has not been tampered with since export.
//
// Dispatch:
//
//   - ModeEmbeddedEd25519 (or an absent/empty Mode — the backward-compat
//     case, P0-413-4): verified fully in-process, no external binary.
//   - ModeCosignKMS: requires a cosign verifier; callers that need to
//     verify KMS-signed bundles use VerifyBundleWithCosign instead. This
//     function returns ErrCosignVerifierRequired so a caller that holds
//     only the embedded path fails closed rather than silently passing a
//     KMS bundle it cannot check.
//   - ModeCosignKeyless: same as cosign-kms — requires a cosign verifier
//     (Fulcio cert + Rekor inclusion checks); the embedded-only path fails
//     closed with ErrCosignVerifierRequired rather than passing a bundle
//     it cannot check (slice 414).
//
// Returns nil when the signature is valid.
func VerifyBundle(b *Bundle) error {
	switch ResolveMode(b.Signature.Mode) {
	case ModeEmbeddedEd25519:
		return verifyEmbedded(b)
	case ModeCosignKMS, ModeCosignKeyless:
		return ErrCosignVerifierRequired
	default:
		return fmt.Errorf("oscal: unknown signing mode %q", b.Signature.Mode)
	}
}

// ErrCosignVerifierRequired is returned by VerifyBundle when the bundle
// was signed with a cosign mode but the caller passed no cosign
// verifier. Use VerifyBundleWithCosign for those bundles. Failing closed
// here is deliberate: a verifier MUST NOT report "valid" for a signature
// path it did not actually check.
var ErrCosignVerifierRequired = errors.New("oscal: bundle is cosign-signed; use VerifyBundleWithCosign with a cosign verifier")

// verifyEmbedded re-derives the bundle digest and checks the in-process
// ed25519 signature against it.
func verifyEmbedded(b *Bundle) error {
	sig := b.Signature
	if sig.Algorithm != "ed25519" {
		return fmt.Errorf("oscal: unsupported signature algorithm %q", sig.Algorithm)
	}
	pub, err := hex.DecodeString(sig.PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return errors.New("oscal: malformed public key in signature")
	}
	sigBytes, err := hex.DecodeString(sig.Signature)
	if err != nil {
		return errors.New("oscal: malformed signature bytes")
	}
	wantDigest := bundleDigest(b)
	gotDigest, err := hex.DecodeString(sig.Digest)
	if err != nil || len(gotDigest) != len(wantDigest) {
		return errors.New("oscal: malformed digest in signature")
	}
	// The digest in the manifest must match the recomputed digest AND
	// the signature must verify against it. Both checks are required: a
	// tamperer could rewrite the digest field, but then the signature
	// (over the OLD digest) would not verify against the NEW one.
	for i := range wantDigest {
		if wantDigest[i] != gotDigest[i] {
			return errors.New("oscal: bundle digest mismatch — members changed since signing")
		}
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), wantDigest[:], sigBytes) {
		return errors.New("oscal: signature verification failed")
	}
	return nil
}
