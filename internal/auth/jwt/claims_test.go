package jwt_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	atlasjwt "github.com/mgoodric/security-atlas/internal/auth/jwt"
)

func newValidClaims(t *testing.T) (atlasjwt.AtlasClaims, time.Time) {
	t.Helper()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	tenantA := uuid.New()
	tenantB := uuid.New()
	c := atlasjwt.AtlasClaims{
		RegisteredClaims: atlasjwt.RegisteredClaims{
			Issuer:    "https://atlas.example.test",
			Subject:   "user:alice",
			Audience:  []string{"https://atlas.example.test/api"},
			ExpiresAt: now.Add(1 * time.Hour).Unix(),
			IssuedAt:  now.Unix(),
			NotBefore: now.Unix(),
			ID:        "jti-1",
		},
		IDPIssuer:        "https://idp.example.test",
		CurrentTenantID:  tenantA,
		AvailableTenants: []uuid.UUID{tenantA, tenantB},
		Roles:            map[uuid.UUID][]string{tenantA: {"admin"}, tenantB: {"reader"}},
		SuperAdmin:       false,
	}
	return c, now
}

// ISC-11 + AC-6: Validate rejects expired tokens.
func TestValidateRejectsExpired(t *testing.T) {
	c, now := newValidClaims(t)
	c.ExpiresAt = now.Add(-1 * time.Minute).Unix()
	err := atlasjwt.Validate(c, atlasjwt.ValidationParams{
		ExpectedIssuer:   c.Issuer,
		ExpectedAudience: "https://atlas.example.test/api",
		Now:              now,
	})
	if err == nil {
		t.Fatal("expected error for expired token")
	}
}

// ISC-12 + AC-6: audience mismatch rejected.
func TestValidateRejectsAudienceMismatch(t *testing.T) {
	c, now := newValidClaims(t)
	err := atlasjwt.Validate(c, atlasjwt.ValidationParams{
		ExpectedIssuer:   c.Issuer,
		ExpectedAudience: "https://other.example.test/api",
		Now:              now,
	})
	if err == nil {
		t.Fatal("expected error for audience mismatch")
	}
}

// ISC-13 + AC-6: issuer mismatch rejected.
func TestValidateRejectsIssuerMismatch(t *testing.T) {
	c, now := newValidClaims(t)
	err := atlasjwt.Validate(c, atlasjwt.ValidationParams{
		ExpectedIssuer:   "https://wrong-issuer.example.test",
		ExpectedAudience: "https://atlas.example.test/api",
		Now:              now,
	})
	if err == nil {
		t.Fatal("expected error for issuer mismatch")
	}
}

// ISC-14 + AC-6: current_tenant_id must appear in available_tenants. This
// is the load-bearing tenant-isolation check slice 190's middleware will
// rely on.
func TestValidateRejectsCurrentTenantNotInAvailable(t *testing.T) {
	c, now := newValidClaims(t)
	c.CurrentTenantID = uuid.New() // not in AvailableTenants
	err := atlasjwt.Validate(c, atlasjwt.ValidationParams{
		ExpectedIssuer:   c.Issuer,
		ExpectedAudience: "https://atlas.example.test/api",
		Now:              now,
	})
	if err == nil {
		t.Fatal("expected error: current_tenant_id not in available_tenants")
	}
}

// Valid claim set accepted (negative-of-negative).
func TestValidateAcceptsValid(t *testing.T) {
	c, now := newValidClaims(t)
	err := atlasjwt.Validate(c, atlasjwt.ValidationParams{
		ExpectedIssuer:   c.Issuer,
		ExpectedAudience: "https://atlas.example.test/api",
		Now:              now,
	})
	if err != nil {
		t.Fatalf("expected valid claims to pass, got %v", err)
	}
}

// nbf (not-before) honored.
func TestValidateRejectsNotYetValid(t *testing.T) {
	c, now := newValidClaims(t)
	c.NotBefore = now.Add(5 * time.Minute).Unix()
	err := atlasjwt.Validate(c, atlasjwt.ValidationParams{
		ExpectedIssuer:   c.Issuer,
		ExpectedAudience: "https://atlas.example.test/api",
		Now:              now,
	})
	if err == nil {
		t.Fatal("expected error for not-yet-valid token")
	}
}
