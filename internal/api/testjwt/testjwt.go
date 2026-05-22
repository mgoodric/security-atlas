// Package testjwt provides JWT mint helpers for integration tests that
// previously authenticated via slice 034's `credstore.Issue()` opaque
// bearer tokens. Slice 197 retires the legacy bearer middleware mount;
// every integration test fixture that needs to authenticate against
// the running HTTP server gets its bearer from this package instead.
//
// The helpers mint claims with the shape the slice 190 `jwtmw`
// middleware synthesizes into a `credstore.Credential`:
//
//   - `AdminFor(tenant)`    — SuperAdmin=true, so jwtmw sets
//     IsAdmin + IsApprover on the synthesized credential.
//   - `ApproverFor(tenant)` — alias for `AdminFor` at the JWT layer;
//     the jwtmw bridge collapses both into SuperAdmin → IsApprover.
//     Kept distinct from AdminFor so call sites read self-documenting.
//   - `OwnerFor(tenant, roles)` — Roles[tenant] = roles, so jwtmw sets
//     OwnerRoles to that list on the credential (slice 011 attestation
//     gate).
//   - `ViewerFor(tenant)`   — minimal viewer claims; equivalent to the
//     legacy `IssueBootstrapCredential` default.
//
// Tests then sign the claims via `MustMint`, which builds the JWS
// envelope through `tokensign.Signer` and returns the compact bearer.
// The companion `*api.Server.IssueTestJWT(t, claims)` method wraps the
// MustMint + lazy-keystore wiring so the typical test does one call.
//
// This package is test-fixture infrastructure ONLY. The package builds
// without the `integration` tag (so `go build ./...` covers it), but
// every export takes `*testing.T` to keep production code from
// importing it accidentally.
package testjwt

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
)

// DefaultExpiry is the lifetime stamped onto every helper-minted JWT.
// 1 hour is far longer than any single integration test run and short
// enough that test bearers cannot leak into long-lived logs without
// natural expiry.
const DefaultExpiry = time.Hour

// MustMint signs the supplied claims via signer and returns the
// compact-serialized JWT. The helper stamps Issuer + Audience +
// IssuedAt + ExpiresAt onto the claims if the caller left them zero
// — every helper-built claim shape passes through here, so the
// per-helper builders can stay focused on Atlas-specific fields.
//
// On any signing failure the helper calls t.Fatal — tests using this
// MUST NOT see a partial bearer.
//
// The signature accepts `testing.TB` (not `*testing.T`) so the same
// helper works in benchmarks (`*testing.B`).
func MustMint(t testing.TB, signer *tokensign.Signer, issuer, audience string, claims jwt.AtlasClaims) string {
	t.Helper()
	now := time.Now().UTC()
	if claims.Issuer == "" {
		claims.Issuer = issuer
	}
	if len(claims.Audience) == 0 {
		claims.Audience = []string{audience}
	}
	if claims.IssuedAt == 0 {
		claims.IssuedAt = now.Unix()
	}
	if claims.ExpiresAt == 0 {
		claims.ExpiresAt = now.Add(DefaultExpiry).Unix()
	}
	tok, err := signer.Sign(context.Background(), claims)
	if err != nil {
		t.Fatalf("testjwt.MustMint: signer.Sign: %v", err)
	}
	return tok
}

// AdminFor returns AtlasClaims modeling a tenant admin — the JWT
// equivalent of the legacy `credstore.IssueAdmin(tenant)`. The jwtmw
// bridge reads SuperAdmin into both IsAdmin and IsApprover on the
// synthesized credstore.Credential, so admin-gated tests see
// IsAdmin=true and approver-gated tests see IsApprover=true.
func AdminFor(tenant uuid.UUID) jwt.AtlasClaims {
	return jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: "test-admin:" + tenant.String(),
		},
		CurrentTenantID:  tenant,
		AvailableTenants: []uuid.UUID{tenant},
		Roles:            map[uuid.UUID][]string{tenant: {"admin"}},
		SuperAdmin:       true,
	}
}

// ApproverFor returns AtlasClaims modeling a tenant approver — the
// JWT equivalent of the legacy `credstore.IssueApprover(tenant)`.
// At the JWT layer this is alpha-equivalent to AdminFor because the
// jwtmw bridge collapses SuperAdmin into both IsAdmin + IsApprover;
// kept as a distinct helper so call sites read self-documenting.
func ApproverFor(tenant uuid.UUID) jwt.AtlasClaims {
	c := AdminFor(tenant)
	c.Subject = "test-approver:" + tenant.String()
	return c
}

// OwnerFor returns AtlasClaims modeling a credential carrying the
// supplied owner roles — the JWT equivalent of the legacy
// `credstore.IssueOwner(tenant, roles)`. The jwtmw bridge reads
// Roles[CurrentTenantID] verbatim into the synthesized credential's
// OwnerRoles, so the slice 011 attestation gate continues to fire.
//
// SuperAdmin is intentionally left false — owner credentials are NOT
// admins.
func OwnerFor(tenant uuid.UUID, roles []string) jwt.AtlasClaims {
	rolesCopy := append([]string(nil), roles...)
	return jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: "test-owner:" + tenant.String(),
		},
		CurrentTenantID:  tenant,
		AvailableTenants: []uuid.UUID{tenant},
		Roles:            map[uuid.UUID][]string{tenant: rolesCopy},
		SuperAdmin:       false,
	}
}

// ViewerFor returns AtlasClaims modeling the baseline viewer
// credential — the JWT equivalent of the legacy
// `credstore.IssueBootstrapCredential(tenant)`. No SuperAdmin, no
// OwnerRoles; the synthesized credstore.Credential is the minimal
// shape that authenticates but does not unlock admin / approver /
// owner gates.
func ViewerFor(tenant uuid.UUID) jwt.AtlasClaims {
	return jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: "test-viewer:" + tenant.String(),
		},
		CurrentTenantID:  tenant,
		AvailableTenants: []uuid.UUID{tenant},
		// Roles map intentionally nil — the jwtmw bridge reads
		// Roles[CurrentTenantID] which yields nil → empty OwnerRoles.
	}
}
