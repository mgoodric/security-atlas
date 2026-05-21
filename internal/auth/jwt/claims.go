// Package jwt defines the atlas JWT claim shape and the validation
// primitives that consumers (notably the slice-190 R2 middleware) will
// use to gate authorisation.
//
// Slice 187 ships claim types + validation only. Signing and
// verification of the JWS envelope live in
// internal/auth/tokensign — claim validation here is shape + temporal
// + relational checks against the claim values themselves, NOT against
// the cryptographic signature.
//
// Custom claims (locked at canvas OQ #21 Reading D, 2026-05-20):
//
//	atlas:idp_issuer         — the upstream OIDC issuer that authenticated the human
//	atlas:current_tenant_id  — the tenant scope this token is currently bound to
//	atlas:available_tenants  — the full list of tenants the subject can switch among
//	atlas:roles              — per-tenant role list (map tenant_id → [role])
//	atlas:super_admin        — global escalation flag (single-tenant deployments use false)
package jwt

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// RegisteredClaims is the RFC 7519 standard claim set. Field tags match
// the IANA-registered names so a JSON marshal round-trips to the wire
// format expected by every off-the-shelf JWT validator.
type RegisteredClaims struct {
	Issuer    string   `json:"iss,omitempty"`
	Subject   string   `json:"sub,omitempty"`
	Audience  []string `json:"aud,omitempty"`
	ExpiresAt int64    `json:"exp,omitempty"`
	NotBefore int64    `json:"nbf,omitempty"`
	IssuedAt  int64    `json:"iat,omitempty"`
	ID        string   `json:"jti,omitempty"`
}

// AtlasClaims extends RegisteredClaims with the locked atlas:* custom
// claims. JSON tags use the canonical `atlas:` prefix exactly as
// committed in the canvas — clients off the platform are expected to
// see those tag names on the wire.
type AtlasClaims struct {
	RegisteredClaims
	IDPIssuer        string                 `json:"atlas:idp_issuer,omitempty"`
	CurrentTenantID  uuid.UUID              `json:"atlas:current_tenant_id,omitempty"`
	AvailableTenants []uuid.UUID            `json:"atlas:available_tenants,omitempty"`
	Roles            map[uuid.UUID][]string `json:"atlas:roles,omitempty"`
	SuperAdmin       bool                   `json:"atlas:super_admin,omitempty"`
}

// ValidationParams supplies the values Validate compares claims
// against. The caller knows the expected issuer + audience from
// configuration; Now is injectable so tests can pin time.
type ValidationParams struct {
	ExpectedIssuer   string
	ExpectedAudience string
	Now              time.Time
}

// Validate performs RFC 7519 + RFC 9068 temporal/identity checks plus
// the atlas-specific tenant-scope invariant. It does NOT verify the
// JWS signature — that belongs in tokensign.Verify, which calls this
// after signature validation succeeds.
//
// Returned errors are typed for the small set of distinct failure
// modes the slice-190 middleware will need to discriminate.
var (
	ErrIssuerMismatch    = errors.New("jwt: issuer mismatch")
	ErrAudienceMismatch  = errors.New("jwt: audience mismatch")
	ErrExpired           = errors.New("jwt: token expired")
	ErrNotYetValid       = errors.New("jwt: token not yet valid (nbf in the future)")
	ErrMissingExpiration = errors.New("jwt: missing exp claim")
	ErrTenantOutOfScope  = errors.New("jwt: current_tenant_id not in available_tenants")
)

// Validate returns nil when every claim passes. The first failing
// check determines the returned error; checks run in this order:
// issuer → audience → exp → nbf → tenant-scope.
func Validate(c AtlasClaims, p ValidationParams) error {
	if c.Issuer == "" || c.Issuer != p.ExpectedIssuer {
		return fmt.Errorf("%w: got %q want %q", ErrIssuerMismatch, c.Issuer, p.ExpectedIssuer)
	}
	if !audienceContains(c.Audience, p.ExpectedAudience) {
		return fmt.Errorf("%w: %v missing %q", ErrAudienceMismatch, c.Audience, p.ExpectedAudience)
	}
	if c.ExpiresAt == 0 {
		return ErrMissingExpiration
	}
	now := p.Now.Unix()
	if now >= c.ExpiresAt {
		return ErrExpired
	}
	if c.NotBefore > 0 && now < c.NotBefore {
		return ErrNotYetValid
	}
	if c.CurrentTenantID != uuid.Nil && !tenantInList(c.AvailableTenants, c.CurrentTenantID) {
		return ErrTenantOutOfScope
	}
	return nil
}

func audienceContains(audience []string, expected string) bool {
	for _, a := range audience {
		if a == expected {
			return true
		}
	}
	return false
}

func tenantInList(list []uuid.UUID, target uuid.UUID) bool {
	for _, t := range list {
		if t == target {
			return true
		}
	}
	return false
}
