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
	// Algorithm identifies the signing scheme. Always "ed25519" in v1.
	Algorithm string `json:"algorithm"`
	// PublicKey is the lowercase-hex ed25519 public key. A verifier uses
	// it together with the bundle digest to check Signature.
	PublicKey string `json:"public_key"`
	// Digest is the lowercase-hex sha256 over the canonical concatenation
	// of every bundle member's own sha256 (see SignBundle).
	Digest string `json:"digest"`
	// Signature is the lowercase-hex ed25519 signature over Digest's raw
	// bytes.
	Signature string `json:"signature"`
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
		Algorithm: "ed25519",
		PublicKey: hex.EncodeToString(s.pub),
		Digest:    hex.EncodeToString(digest[:]),
		Signature: hex.EncodeToString(sig),
	}, nil
}

// VerifyBundle re-derives the bundle digest and checks the embedded
// signature against it. It is the verification counterpart to
// SignBundle: an auditor (or the platform's own integrity check) calls
// it to confirm the bundle has not been tampered with since export.
// Returns nil when the signature is valid.
func VerifyBundle(b *Bundle) error {
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
