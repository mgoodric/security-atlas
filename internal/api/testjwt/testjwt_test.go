// Package testjwt unit tests. These verify the mint helpers produce
// JWTs that round-trip through tokensign.Signer.Verify and validate
// against jwt.ValidationParams with the matching issuer + audience.
//
// Slice 197 — these tests are the load-bearing verification that the
// migration-target JWT shape is interoperable with the slice 187 +
// slice 190 production code path.
package testjwt_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/keystore/fsstore"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
)

// TestMustMint_RoundTrip is the slice 197 ISC-7 anchor. A JWT minted
// via the helper must verify against the same signer and validate
// successfully against ValidationParams whose issuer + audience match
// the values the helper stamped onto the token.
func TestMustMint_RoundTrip(t *testing.T) {
	ks, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	signer := tokensign.New(ks)

	tenant := uuid.New()
	claims := testjwt.AdminFor(tenant)

	const issuer = "https://atlas.test"
	const audience = "https://atlas.test"
	tok := testjwt.MustMint(t, signer, issuer, audience, claims)
	if tok == "" {
		t.Fatal("MustMint returned empty token")
	}

	out, err := signer.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("signer.Verify: %v", err)
	}
	if out.CurrentTenantID != tenant {
		t.Fatalf("CurrentTenantID round-trip: got %v want %v", out.CurrentTenantID, tenant)
	}
	if !out.SuperAdmin {
		t.Fatal("expected SuperAdmin=true on AdminFor claims")
	}
	if out.Issuer != issuer {
		t.Fatalf("Issuer round-trip: got %q want %q", out.Issuer, issuer)
	}
}

// TestAdminFor_ShapeMatchesJWTMiddlewareExpectations verifies the
// AdminFor helper produces claims the jwtmw.Middleware can synthesize
// a working admin credstore.Credential from. The middleware reads
// SuperAdmin into both IsAdmin and IsApprover; AdminFor MUST set
// SuperAdmin so admin-gated tests see IsAdmin=true.
func TestAdminFor_ShapeMatchesJWTMiddlewareExpectations(t *testing.T) {
	tenant := uuid.New()
	c := testjwt.AdminFor(tenant)
	if !c.SuperAdmin {
		t.Error("AdminFor must set SuperAdmin")
	}
	if c.CurrentTenantID != tenant {
		t.Errorf("CurrentTenantID = %v, want %v", c.CurrentTenantID, tenant)
	}
	if len(c.AvailableTenants) != 1 || c.AvailableTenants[0] != tenant {
		t.Errorf("AvailableTenants = %v, want [%v]", c.AvailableTenants, tenant)
	}
}

// TestOwnerFor_RolesPropagateToJWTClaim verifies the OwnerFor helper
// stamps the supplied roles onto Roles[tenantID]. The jwtmw middleware
// reads exactly that path (jwtmw line 205) to synthesize OwnerRoles
// on the credstore.Credential — the slice-011 manual attestation gate.
func TestOwnerFor_RolesPropagateToJWTClaim(t *testing.T) {
	tenant := uuid.New()
	c := testjwt.OwnerFor(tenant, []string{"control_owner", "policy_owner"})
	got := c.Roles[tenant]
	if len(got) != 2 || got[0] != "control_owner" || got[1] != "policy_owner" {
		t.Errorf("Roles[tenant] = %v, want [control_owner policy_owner]", got)
	}
	if c.SuperAdmin {
		t.Error("OwnerFor must NOT set SuperAdmin")
	}
}

// TestApproverFor_MimicsLegacyIssueApprover verifies ApproverFor's
// shape gives the jwtmw bridge an IsApprover-flagged credential. Since
// the bridge maps SuperAdmin → IsApprover (jwtmw line 204), the helper
// uses SuperAdmin to flag approver authority for the integration-test
// surface. (V1 approver semantics in legacy credstore: IsApprover bit
// without IsAdmin. JWT bridge collapses both into SuperAdmin for tests
// — sufficient because tests gate only on the resulting IsApprover.)
func TestApproverFor_MimicsLegacyIssueApprover(t *testing.T) {
	tenant := uuid.New()
	c := testjwt.ApproverFor(tenant)
	if !c.SuperAdmin {
		t.Error("ApproverFor must set SuperAdmin so jwtmw synthesizes IsApprover=true")
	}
}

// TestViewerFor_NoElevation verifies the baseline viewer credential
// has no SuperAdmin / no OwnerRoles — equivalent to the legacy
// IssueBootstrapCredential default.
func TestViewerFor_NoElevation(t *testing.T) {
	tenant := uuid.New()
	c := testjwt.ViewerFor(tenant)
	if c.SuperAdmin {
		t.Error("ViewerFor must NOT set SuperAdmin")
	}
	if len(c.Roles[tenant]) != 0 {
		t.Errorf("ViewerFor Roles[tenant] = %v, want empty", c.Roles[tenant])
	}
}

// TestClaimsValidateAgainstStandardParams confirms the helper-produced
// claims pass jwt.Validate when paired with matching ValidationParams.
// Catches regressions where the helper forgets to stamp ExpiresAt or
// IssuedAt.
func TestClaimsValidateAgainstStandardParams(t *testing.T) {
	ks, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	signer := tokensign.New(ks)
	tenant := uuid.New()
	claims := testjwt.AdminFor(tenant)
	const issuer = "https://atlas.test"
	const audience = "https://atlas.test"
	tok := testjwt.MustMint(t, signer, issuer, audience, claims)
	verified, err := signer.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("signer.Verify: %v", err)
	}
	if err := jwt.Validate(verified, jwt.ValidationParams{
		ExpectedIssuer:   issuer,
		ExpectedAudience: audience,
		Now:              time.Now(),
	}); err != nil {
		t.Fatalf("jwt.Validate: %v", err)
	}
}
