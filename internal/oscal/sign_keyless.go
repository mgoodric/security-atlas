package oscal

import (
	"context"
	"crypto/x509"
	"encoding/asn1"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

// oidIssuerV2 is the Fulcio X.509v3 extension OID carrying the OIDC issuer
// claim (1.3.6.1.4.1.57264.1.8 — the Sigstore "Issuer (V2)" extension, a
// DER-encoded UTF8String). The legacy 1.3.6.1.4.1.57264.1.1 extension is a
// raw string. We read the SAN URI for the signing identity and either
// issuer extension for the OIDC issuer.
var (
	oidIssuerV2     = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 8}
	oidIssuerLegacy = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 1}
)

// certIdentityAndIssuer extracts the signing identity (the first URI SAN —
// atlas's `client:oscal-signer` subject) and the OIDC issuer (the Fulcio
// issuer extension) from a PEM-encoded Fulcio certificate. Best-effort: a
// cert that does not parse, or that lacks the fields, yields empty strings
// — the caller decides whether that is fatal (the signer requires a cert,
// but a verifier can fall back to the manifest-recorded identity).
func certIdentityAndIssuer(certPEM string) (identity, issuer string) {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return "", ""
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", ""
	}
	if len(cert.URIs) > 0 {
		identity = cert.URIs[0].String()
	} else if len(cert.EmailAddresses) > 0 {
		identity = cert.EmailAddresses[0]
	}
	for _, ext := range cert.Extensions {
		switch {
		case ext.Id.Equal(oidIssuerV2):
			var s string
			if _, err := asn1.Unmarshal(ext.Value, &s); err == nil && s != "" {
				issuer = s
			}
		case ext.Id.Equal(oidIssuerLegacy) && issuer == "":
			issuer = string(ext.Value)
		}
	}
	return identity, issuer
}

// KeylessAttestation is the cosign-keyless mode's manifest extension
// (slice 414 / 368b, per ADR-0016). It records everything a verifier
// needs to validate a Fulcio-issued short-lived certificate + the Rekor
// transparency-log entry against an OPERATOR-RUN PRIVATE Sigstore trust
// root — never public Fulcio (ADR-0016 rejects options a/d for the
// runtime OSCAL surface).
//
// The shape is deliberately self-describing: a bundle signed on one
// deployment must carry enough context for a verifier elsewhere (an
// auditor) to run `cosign verify-blob` against the SAME private trust
// root — so the Fulcio/Rekor URLs and the expected cert identity travel
// in the manifest alongside the cert itself.
type KeylessAttestation struct {
	// Certificate is the PEM-encoded short-lived signing certificate that
	// the operator's Fulcio issued for the `client:oscal-signer` identity.
	// A verifier checks the bundle signature against this cert and checks
	// the cert chains to the operator's controlled root.
	Certificate string `json:"certificate"`
	// CertificateIdentity is the SAN identity the cert binds (the atlas
	// `client:oscal-signer` subject). A verifier passes it to
	// `cosign verify-blob --certificate-identity` so a cert for a DIFFERENT
	// identity does not satisfy the check.
	CertificateIdentity string `json:"certificate_identity"`
	// CertificateOIDCIssuer is the issuer the cert was minted from (this
	// deployment's atlas AS issuer, per ADR-0016 / slice 188). A verifier
	// passes it to `cosign verify-blob --certificate-oidc-issuer`.
	CertificateOIDCIssuer string `json:"certificate_oidc_issuer"`
	// RekorLogIndex is the integer index of the Rekor transparency-log
	// entry created at sign time (the operator's PRIVATE Rekor — ADR-0016,
	// "no data leaves deployment"). It is the non-repudiation anchor; a
	// verifier can fetch the inclusion proof for this index from the same
	// private Rekor.
	RekorLogIndex int64 `json:"rekor_log_index"`
	// FulcioURL / RekorURL record the operator-run private Sigstore
	// endpoints used at sign time, so a verifier knows which private trust
	// root to validate against. These are NEVER public Fulcio/Rekor
	// (P0-414-3).
	FulcioURL string `json:"fulcio_url"`
	RekorURL  string `json:"rekor_url"`
}

// KeylessSigner produces ModeCosignKeyless signatures over an export
// bundle by (1) obtaining atlas's scoped `client:oscal-signer` OIDC token,
// (2) exchanging it at the operator's PRIVATE Fulcio for a short-lived
// signing certificate, (3) signing the bundle digest, and (4) uploading
// the signature + cert to the operator's PRIVATE Rekor transparency log.
// It is the cosign-keyless-mode counterpart to *KMSSigner.
//
// Per ADR-0016 this is an OPT-IN mode for the private-Sigstore deployment
// shape. It is unreachable on air-gap (no Sigstore) and is never a
// default — kms/embedded stay the defaults for every primary shape
// (P0-414-2 / P0-414-3).
//
// The blob signed is the SAME deterministic bundle digest the embedded
// and kms paths sign (bundleDigest) — the digest derivation is
// mode-independent, so the tamper-detection property is identical across
// modes.
type KeylessSigner struct {
	client      CosignKeylessSigner
	tokenSource IdentityTokenSource
	fulcioURL   string
	rekorURL    string
}

// CosignKeylessSigner is the minimal cosign-keyless blob-signing surface
// the export pipeline needs. internal/oscal/cosign.Client satisfies it.
// Declaring it here keeps the oscal package's cosign dependency
// injectable and unit-testable without the cosign binary (the same
// pattern as CosignSigner for the kms mode).
type CosignKeylessSigner interface {
	// SignBlobKeyless signs blob using a keyless flow against the operator's
	// PRIVATE Fulcio + Rekor. identityToken is the atlas OIDC token; the
	// returned result carries the issued cert + the Rekor log index.
	SignBlobKeyless(ctx context.Context, req KeylessSignRequest) (KeylessSignResult, error)
}

// CosignKeylessVerifier is the minimal cosign-keyless verification
// surface. internal/oscal/cosign.Client satisfies it.
type CosignKeylessVerifier interface {
	// VerifyBlobKeyless returns nil iff signature is a valid keyless cosign
	// signature over blob for the recorded cert identity + issuer, against
	// the operator's PRIVATE trust root.
	VerifyBlobKeyless(ctx context.Context, req KeylessVerifyRequest) error
}

// KeylessSignRequest is the input to a keyless sign. It is a struct (not a
// long argument list) because the keyless flow legitimately needs several
// operator-controlled inputs, and a struct keeps the cosign-wrapper API
// honest as the flow evolves.
type KeylessSignRequest struct {
	Blob          []byte
	IdentityToken string
	FulcioURL     string
	RekorURL      string
}

// KeylessSignResult is what a keyless sign produces: the detached
// signature plus the Fulcio cert and Rekor log index that anchor it.
type KeylessSignResult struct {
	Signature             []byte
	CertificatePEM        string
	RekorLogIndex         int64
	CertificateIdentity   string
	CertificateOIDCIssuer string
}

// KeylessVerifyRequest is the input to a keyless verify.
type KeylessVerifyRequest struct {
	Blob                  []byte
	Signature             []byte
	CertificatePEM        string
	CertificateIdentity   string
	CertificateOIDCIssuer string
	RekorURL              string
}

// IdentityTokenSource yields atlas's scoped `client:oscal-signer` OIDC
// token (slice 188 client_credentials grant). It is an interface so the
// oscal package does not import the oauth internals directly — the server
// wires a concrete source that mints/caches the token; tests inject a
// fake. Per ADR-0016 the token's subject is `client:oscal-signer` and its
// issuer is this deployment's atlas AS issuer.
type IdentityTokenSource interface {
	// IdentityToken returns a fresh (or cached-but-unexpired) OIDC token
	// for the oscal-signer identity, or an error if one cannot be minted.
	IdentityToken(ctx context.Context) (string, error)
}

// ErrKeylessNotConfigured is returned when a KeylessSigner is constructed
// without the operator-supplied private Fulcio/Rekor endpoints. Keyless is
// opt-in; absent configuration is not an error condition for the platform
// (it simply means the mode is unavailable) — but a code path that tries
// to BUILD a keyless signer without them is a misconfiguration and fails
// loudly here rather than silently producing an unusable signer.
var ErrKeylessNotConfigured = errors.New(
	"oscal: cosign-keyless signer requires operator-supplied private Fulcio and Rekor URLs " +
		"(keyless is opt-in for the private-Sigstore deployment shape; see ADR-0016)")

// NewKeylessSigner wires a KeylessSigner. It requires a cosign client, an
// identity-token source, and the operator's PRIVATE Fulcio + Rekor URLs.
// Any missing dependency is a misconfiguration (keyless is opt-in, but a
// caller that reached this constructor intends to use it).
func NewKeylessSigner(client CosignKeylessSigner, tokenSource IdentityTokenSource, fulcioURL, rekorURL string) (*KeylessSigner, error) {
	if client == nil {
		return nil, errors.New("oscal: cosign-keyless signer requires a cosign client")
	}
	if tokenSource == nil {
		return nil, errors.New("oscal: cosign-keyless signer requires an identity-token source")
	}
	fulcioURL = strings.TrimSpace(fulcioURL)
	rekorURL = strings.TrimSpace(rekorURL)
	if fulcioURL == "" || rekorURL == "" {
		return nil, ErrKeylessNotConfigured
	}
	return &KeylessSigner{
		client:      client,
		tokenSource: tokenSource,
		fulcioURL:   fulcioURL,
		rekorURL:    rekorURL,
	}, nil
}

// SignBundle signs the bundle digest via the cosign keyless flow and
// returns a ModeCosignKeyless Signature with its KeylessAttestation. The
// digest is computed identically to the embedded/kms paths; the manifest
// records the Fulcio cert + Rekor log index + the operator's Sigstore
// endpoints so a verifier can validate against the same private trust
// root.
func (s *KeylessSigner) SignBundle(ctx context.Context, b *Bundle) (Signature, error) {
	if s == nil || s.client == nil {
		return Signature{}, errors.New("oscal: nil cosign-keyless signer")
	}
	if len(b.Members) == 0 {
		return Signature{}, errors.New("oscal: cannot sign an empty bundle")
	}
	token, err := s.tokenSource.IdentityToken(ctx)
	if err != nil {
		return Signature{}, fmt.Errorf("oscal: cosign-keyless identity token: %w", err)
	}
	if strings.TrimSpace(token) == "" {
		return Signature{}, errors.New("oscal: cosign-keyless identity-token source returned an empty token")
	}
	digest := bundleDigest(b)
	res, err := s.client.SignBlobKeyless(ctx, KeylessSignRequest{
		Blob:          digest[:],
		IdentityToken: token,
		FulcioURL:     s.fulcioURL,
		RekorURL:      s.rekorURL,
	})
	if err != nil {
		return Signature{}, fmt.Errorf("oscal: cosign-keyless sign: %w", err)
	}
	if len(res.Signature) == 0 {
		return Signature{}, errors.New("oscal: cosign-keyless produced an empty signature")
	}
	if strings.TrimSpace(res.CertificatePEM) == "" {
		return Signature{}, errors.New("oscal: cosign-keyless produced no certificate")
	}
	return Signature{
		Mode:      ModeCosignKeyless,
		Algorithm: "cosign-keyless",
		Digest:    hex.EncodeToString(digest[:]),
		Signature: string(res.Signature),
		Keyless: &KeylessAttestation{
			Certificate:           res.CertificatePEM,
			CertificateIdentity:   res.CertificateIdentity,
			CertificateOIDCIssuer: res.CertificateOIDCIssuer,
			RekorLogIndex:         res.RekorLogIndex,
			FulcioURL:             s.fulcioURL,
			RekorURL:              s.rekorURL,
		},
	}, nil
}

// cosignKeylessBackend is the lower-level keyless surface that the
// cosign-wrapper subpackage's *Client implements directly (it uses the
// subpackage's own param/output types). The oscal package keeps its own
// KeylessSign{Request,Result} for a clean, subpackage-decoupled signer
// API; CosignKeylessAdapter bridges the two so cosign.Client can be used
// as a KeylessSigner/Verifier without the oscal package importing the
// subpackage's types into its public interface.
type cosignKeylessBackend interface {
	SignBlobKeyless(ctx context.Context, blob []byte, identityToken, fulcioURL, rekorURL string) (sig []byte, certPEM string, rekorLogIndex int64, err error)
	VerifyBlobKeyless(ctx context.Context, blob, signature []byte, certPEM, certIdentity, certOIDCIssuer, rekorURL string) error
}

// CosignKeylessAdapter adapts a cosignKeylessBackend (the cosign.Client
// via a tiny shim) to the oscal package's CosignKeylessSigner /
// CosignKeylessVerifier interfaces. Construct it with NewCosignKeylessAdapter.
type CosignKeylessAdapter struct{ backend cosignKeylessBackend }

// NewCosignKeylessAdapter wraps a backend (typically a shim over
// cosign.Client) so it satisfies the keyless signer/verifier interfaces.
func NewCosignKeylessAdapter(backend cosignKeylessBackend) *CosignKeylessAdapter {
	return &CosignKeylessAdapter{backend: backend}
}

// SignBlobKeyless implements CosignKeylessSigner.
func (a *CosignKeylessAdapter) SignBlobKeyless(ctx context.Context, req KeylessSignRequest) (KeylessSignResult, error) {
	sig, certPEM, idx, err := a.backend.SignBlobKeyless(ctx, req.Blob, req.IdentityToken, req.FulcioURL, req.RekorURL)
	if err != nil {
		return KeylessSignResult{}, err
	}
	identity, issuer := certIdentityAndIssuer(certPEM)
	return KeylessSignResult{
		Signature:             sig,
		CertificatePEM:        certPEM,
		RekorLogIndex:         idx,
		CertificateIdentity:   identity,
		CertificateOIDCIssuer: issuer,
	}, nil
}

// VerifyBlobKeyless implements CosignKeylessVerifier.
func (a *CosignKeylessAdapter) VerifyBlobKeyless(ctx context.Context, req KeylessVerifyRequest) error {
	return a.backend.VerifyBlobKeyless(ctx, req.Blob, req.Signature, req.CertificatePEM,
		req.CertificateIdentity, req.CertificateOIDCIssuer, req.RekorURL)
}

// verifyCosignKeyless verifies a ModeCosignKeyless bundle: it re-derives
// the deterministic digest (catching member-tamper before cosign runs),
// then asks the keyless verifier to validate the recorded signature
// against the recorded Fulcio cert + identity + issuer (the operator's
// PRIVATE trust root). The digest is recomputed from the members, not
// trusted from the manifest — the same belt-and-braces property the
// embedded and kms paths have.
func verifyCosignKeyless(ctx context.Context, b *Bundle, verifier CosignKeylessVerifier) error {
	if verifier == nil {
		return ErrCosignVerifierRequired
	}
	sig := b.Signature
	if sig.Algorithm != "cosign-keyless" {
		return fmt.Errorf("oscal: cosign-keyless mode with unexpected algorithm %q", sig.Algorithm)
	}
	if sig.Keyless == nil {
		return errors.New("oscal: cosign-keyless signature is missing its keyless attestation")
	}
	att := sig.Keyless
	if strings.TrimSpace(att.Certificate) == "" {
		return errors.New("oscal: cosign-keyless signature is missing its certificate")
	}
	if att.CertificateIdentity == "" || att.CertificateOIDCIssuer == "" {
		return errors.New("oscal: cosign-keyless signature is missing its certificate identity/issuer")
	}
	if sig.Signature == "" {
		return errors.New("oscal: cosign-keyless signature is empty")
	}
	// Recompute the digest from the actual members — do NOT trust the
	// manifest's recorded digest. A member-tamper changes this and the
	// recorded digest will not match, which we reject before invoking
	// cosign.
	want := bundleDigest(b)
	gotDigest, err := hex.DecodeString(sig.Digest)
	if err != nil || len(gotDigest) != len(want) {
		return errors.New("oscal: malformed digest in cosign-keyless signature")
	}
	for i := range want {
		if want[i] != gotDigest[i] {
			return errors.New("oscal: bundle digest mismatch — members changed since signing")
		}
	}
	if err := verifier.VerifyBlobKeyless(ctx, KeylessVerifyRequest{
		Blob:                  want[:],
		Signature:             []byte(sig.Signature),
		CertificatePEM:        att.Certificate,
		CertificateIdentity:   att.CertificateIdentity,
		CertificateOIDCIssuer: att.CertificateOIDCIssuer,
		RekorURL:              att.RekorURL,
	}); err != nil {
		return fmt.Errorf("oscal: cosign-keyless verification failed: %w", err)
	}
	return nil
}
