package authz_test

// #2674: the tenant gates read a credential's role label through one
// predicate. Before #2596, IsTenantAuditReader and IsTenantRiskReader
// counted a client_credentials machine token's auditor or viewer label
// (TenantRoleLabels) while IsTenantApprover refused its grc_engineer label,
// which a separate owner-label rule read. This table pins the agreement:
// for every credential whose label is its grant, each gate admits a label
// exactly as it admits the same role held live; for a credential whose
// label is a copy (a user's session, a key bound to a user), no label
// admits anything.

import (
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
)

func TestLabelGrant2674_GatesAgreeAcrossShapes(t *testing.T) {
	t.Parallel()
	gates := []gate2428{tenantAdminGate, tenantApproverGate, auditReaderGate, riskReaderGate, anyRoleGate, riskImportDeciderGate,
		{"IsTenantControlOwner", authz.IsTenantControlOwner}}
	shapes := []struct {
		name    string
		cred    func(label string) credstore.Credential
		isGrant bool
	}{
		{"machine token", func(l string) credstore.Credential {
			return credstore.Credential{ID: "jwt:2674", UserID: "oauth_client:c2674", OwnerRoles: []string{l}}
		}, true},
		{"unbound key", func(l string) credstore.Credential {
			return credstore.Credential{ID: "key_2674", UserID: "key_2674", OwnerRoles: []string{l}}
		}, true},
		{"key naming no user", func(l string) credstore.Credential {
			return credstore.Credential{ID: "key_2674e", OwnerRoles: []string{l}}
		}, true},
		{"legacy credential", func(l string) credstore.Credential {
			return credstore.Credential{ID: "cred_2674", OwnerRoles: []string{l}}
		}, true},
		{"user session", func(l string) credstore.Credential {
			return credstore.Credential{ID: "jwt:2674u", UserID: "u2674", OwnerRoles: []string{l}}
		}, false},
		{"bound key", func(l string) credstore.Credential {
			return credstore.Credential{ID: "key_2674b", UserID: "u2674", OwnerRoles: []string{l}}
		}, false},
	}
	for _, role := range tenantRoles2428 {
		live := credstore.Credential{ID: "jwt:2674l", UserID: "u2674l", TenantRoles: []string{string(role)}}
		for _, s := range shapes {
			cred := s.cred(string(role))
			for _, g := range gates {
				want := s.isGrant && g.fn(live)
				if got := g.fn(cred); got != want {
					t.Errorf("%s with %s label: %s = %v, want %v (live %s: %v)",
						s.name, role, g.name, got, want, role, g.fn(live))
				}
			}
		}
	}
}
