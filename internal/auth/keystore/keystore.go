// Package keystore defines the abstraction for the OAuth Authorization
// Server's JWT signing keys.
//
// Slice 187 ships a single interface and one filesystem-backed
// implementation. Subsequent slices may add KMS / HSM backends without
// changing the interface or any caller.
//
// The keystore exposes ONE signing key (the current key — what new JWTs
// are signed with) and ONE OR MORE verification keys (the current key
// plus any keys retained for the rotation overlap window — what JWTs
// presented by clients are verified against). Day one ships with a
// single key in both slots; multi-key rotation is a follow-on slice.
//
// See docs/adr/0003-oauth-authorization-server.md for the architectural
// rationale.
package keystore

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"errors"
)

// Curve is the ECDSA curve that ES256 signing requires (NIST P-256).
// Centralised here so callers don't sprinkle elliptic.P256() around.
func Curve() elliptic.Curve { return elliptic.P256() }

// SigningKey is the current keypair used to mint new JWTs. The KeyID is
// the JWS `kid` header value and the JWKS `kid` field — clients use it
// to select the right verification key on receipt.
type SigningKey struct {
	KeyID string
	Key   *ecdsa.PrivateKey
}

// VerificationKey is a public key that the AS will accept as having
// signed a JWT. The current SigningKey's public half is always present
// in this list; during rotation, the prior key's public half is also
// present until its overlap window closes.
type VerificationKey struct {
	KeyID string
	Key   *ecdsa.PublicKey
}

// KeyStore is the abstract interface every backend implements. Slice
// 187 ships the filesystem-backed implementation in
// internal/auth/keystore/fsstore.
type KeyStore interface {
	// Get returns the active signing key and the full set of accepted
	// verification keys. Implementations MUST guarantee the signing
	// key's public half is present in the verification list.
	Get(ctx context.Context) (SigningKey, []VerificationKey, error)

	// Rotate generates a new signing key and moves the prior key into
	// the verification set for the overlap window. Slice 187 ships an
	// interface stub that returns ErrRotateUnsupported; the end-to-end
	// rotation flow lands in a follow-on slice (see ADR-0003 § Key
	// rotation strategy).
	Rotate(ctx context.Context) error
}

// ErrRotateUnsupported is returned by the v1 fsstore Rotate method.
// The interface declares Rotate so callers can adopt the rotation API
// today; implementations may return this until the end-to-end rotation
// flow lands.
var ErrRotateUnsupported = errors.New("keystore: end-to-end rotation not yet implemented (see ADR-0003 § Key rotation strategy)")
