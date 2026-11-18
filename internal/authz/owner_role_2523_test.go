package authz_test

import (
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
)

// TestHasOwnerRole2523 pins who may attest a control with a given owner_role
// per credential shape (#2523). A user's session counts its live roles, never
// its role claim, which outlives a revoke. A key, bound or not, and a machine
// token count their label, as AckRoleLabels does, except that a bound key's
// label naming a canonical role counts only while the owner holds it live
// (#2541). The admin flag is a
// wildcard, and an empty owner_role admits only it.
func TestHasOwnerRole2523(t *testing.T) {
	t.Parallel()
	const owner = "11111111-1111-1111-1111-111111111111"
	session := func(claim, live []string) credstore.Credential {
		return credstore.Credential{ID: "jwt:a", UserID: "user:" + owner, OwnerRoles: claim, TenantRoles: live}
	}
	co := []string{"control_owner"}
	cases := []struct {
		name string
		cred credstore.Credential
		role string
		want bool
	}{
		{"session, claim and live role match", session(co, co), "control_owner", true},
		{"session, role revoked, claim still holds it", session(co, nil), "control_owner", false},
		{"session, role revoked, other live role", session(co, []string{"grc_engineer"}), "control_owner", false},
		{"session, live role granted after mint", session(nil, co), "control_owner", true},
		{"session, empty owner_role", session(co, co), "", false},
		{"super-admin session is a wildcard", credstore.Credential{ID: "jwt:a", UserID: "user:" + owner, IsAdmin: true}, "control_owner", true},
		{"admin flag admits an empty owner_role", credstore.Credential{IsAdmin: true}, "", true},
		{"machine token, claim is its grant", credstore.Credential{ID: "jwt:m", UserID: "oauth_client:c1", OwnerRoles: co}, "control_owner", true},
		{"unbound key, label matches", credstore.Credential{ID: "key_k", UserID: "key_k", OwnerRoles: co}, "control_owner", true},
		{"unbound key, label differs", credstore.Credential{ID: "key_k", UserID: "key_k", OwnerRoles: []string{"other_role"}}, "control_owner", false},
		{"bound key, free-text label matches", credstore.Credential{ID: "key_k", UserID: owner, OwnerRoles: []string{"security-engineering"}}, "security-engineering", true},
		{"bound key, canonical label, owner's role revoked", credstore.Credential{ID: "key_k", UserID: owner, OwnerRoles: co, TenantRoles: []string{"grc_engineer"}}, "control_owner", false},
		{"bound key, canonical label, owner holds it live", credstore.Credential{ID: "key_k", UserID: owner, OwnerRoles: co, TenantRoles: co}, "control_owner", true},
		{"bound key, no label, live role matches", credstore.Credential{ID: "key_k", UserID: owner, TenantRoles: co}, "control_owner", true},
		{"bare credential", credstore.Credential{ID: "key_p", UserID: "key_p"}, "control_owner", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := authz.HasOwnerRole(tc.cred, tc.role); got != tc.want {
				t.Errorf("HasOwnerRole(%q) = %v; want %v", tc.role, got, tc.want)
			}
		})
	}
}
