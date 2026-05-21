// Package tokensign wraps the keystore + go-jose to produce and verify
// JWS-signed atlas JWTs. Slice 187 ships sign/verify primitives only —
// the OAuth grant flows that USE these primitives land in slices
// 188-192.
//
// All tokens are signed with ES256 (RFC 7518 §3.4). The signing key's
// KeyID is published as the JWS `kid` header so JWKS-based verifiers
// can select the right verification key.
//
// Anti-criterion P0-187-6: this file must NEVER log JWT payloads in
// the clear, and MUST never log private key material. Only KeyIDs and
// error categories may surface in logs.
package tokensign

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/json"
	"errors"
	"fmt"

	jose "github.com/go-jose/go-jose/v4"

	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/keystore"
)

// SignatureAlgorithm is the only signing algorithm slice 187 supports.
// Exported so peer packages (notably internal/api/oauth, which
// advertises this in the OIDC discovery doc) reference a single
// source of truth. Documented in ADR-0003 (decision D1).
const SignatureAlgorithm = jose.ES256

// SignatureAlgorithmString is the same value as a plain string,
// suitable for embedding in the OIDC discovery document's
// `id_token_signing_alg_values_supported` field and the JWK's `alg`
// value. Kept as a separate constant to avoid leaking the jose type
// across the package boundary.
const SignatureAlgorithmString = "ES256"

// AllowedAlgs is the closed set of signature algorithms ParseSigned
// will accept. Locking this at the parser level prevents algorithm
// confusion attacks (RFC 8725 §2.1 — "Perform Algorithm Verification").
// Exported as a package-level immutable for use by future verifiers
// (slice 190 R2 middleware will use the same allowlist).
var AllowedAlgs = []jose.SignatureAlgorithm{jose.ES256}

// Signer produces and verifies atlas JWTs against a backing keystore.
type Signer struct {
	store keystore.KeyStore
}

// New returns a Signer that asks store for the active signing key on
// every Sign call and the full verification set on every Verify call.
// Keys are not cached at this layer — keystore implementations cache
// internally and bear that responsibility.
func New(store keystore.KeyStore) *Signer {
	return &Signer{store: store}
}

// Sign marshals claims as JSON, wraps them in a JWS using ES256, and
// returns the compact-serialized token. The signing key's KeyID is
// stamped into the JWS `kid` header.
func (s *Signer) Sign(ctx context.Context, claims jwt.AtlasClaims) (string, error) {
	sk, _, err := s.store.Get(ctx)
	if err != nil {
		return "", fmt.Errorf("tokensign: keystore get: %w", err)
	}
	if sk.Key == nil {
		return "", errors.New("tokensign: nil signing key from keystore")
	}
	if !isES256Key(sk.Key) {
		return "", fmt.Errorf("tokensign: signing key is not ES256-compatible (curve %v)", sk.Key.Curve)
	}
	signer, err := jose.NewSigner(jose.SigningKey{
		Algorithm: SignatureAlgorithm,
		Key: jose.JSONWebKey{
			Key:   sk.Key,
			KeyID: sk.KeyID,
		},
	}, (&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		return "", fmt.Errorf("tokensign: new signer: %w", err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("tokensign: marshal claims: %w", err)
	}
	jws, err := signer.Sign(payload)
	if err != nil {
		return "", fmt.Errorf("tokensign: sign: %w", err)
	}
	out, err := jws.CompactSerialize()
	if err != nil {
		return "", fmt.Errorf("tokensign: serialize: %w", err)
	}
	return out, nil
}

// Verify parses tok as a JWS, finds the matching verification key by
// `kid`, validates the signature, and returns the decoded claims.
// Verify does NOT apply jwt.Validate temporal/identity checks — the
// caller is responsible for that step because they know the expected
// issuer + audience + clock.
func (s *Signer) Verify(ctx context.Context, tok string) (jwt.AtlasClaims, error) {
	_, vks, err := s.store.Get(ctx)
	if err != nil {
		return jwt.AtlasClaims{}, fmt.Errorf("tokensign: keystore get: %w", err)
	}
	parsed, err := jose.ParseSigned(tok, AllowedAlgs)
	if err != nil {
		return jwt.AtlasClaims{}, fmt.Errorf("tokensign: parse: %w", err)
	}
	if len(parsed.Signatures) != 1 {
		return jwt.AtlasClaims{}, fmt.Errorf("tokensign: expected exactly 1 signature, got %d", len(parsed.Signatures))
	}
	kid := parsed.Signatures[0].Header.KeyID
	pub, ok := findKey(vks, kid)
	if !ok {
		return jwt.AtlasClaims{}, fmt.Errorf("tokensign: no verification key for kid %q", kid)
	}
	payload, err := parsed.Verify(pub)
	if err != nil {
		return jwt.AtlasClaims{}, fmt.Errorf("tokensign: verify: %w", err)
	}
	var out jwt.AtlasClaims
	if err := json.Unmarshal(payload, &out); err != nil {
		return jwt.AtlasClaims{}, fmt.Errorf("tokensign: unmarshal claims: %w", err)
	}
	return out, nil
}

// PeekKeyID returns the `kid` header from a compact-serialized JWS
// without verifying the signature. Used by tests + diagnostic
// utilities; production code MUST always go through Verify.
func PeekKeyID(tok string) (string, error) {
	parsed, err := jose.ParseSigned(tok, AllowedAlgs)
	if err != nil {
		return "", fmt.Errorf("tokensign: parse: %w", err)
	}
	if len(parsed.Signatures) == 0 {
		return "", errors.New("tokensign: no signatures present")
	}
	return parsed.Signatures[0].Header.KeyID, nil
}

func findKey(vks []keystore.VerificationKey, kid string) (*ecdsa.PublicKey, bool) {
	for _, vk := range vks {
		if vk.KeyID == kid {
			return vk.Key, true
		}
	}
	return nil, false
}

func isES256Key(priv *ecdsa.PrivateKey) bool {
	return priv != nil && priv.Curve == elliptic.P256()
}
