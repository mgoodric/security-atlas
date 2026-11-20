package policyacks

import (
	"reflect"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
)

// TestAckCallerFromCred2505 pins the roles a policy ack matches to live
// tenant roles: a JWT session's role claim is dropped, so a revoked role no
// longer puts a role-targeted policy in the caller's pending list or lets
// them record its ack (#2505). A key keeps its owner_roles label, which is
// where the free-text ack roles live and what the ack roster counts.
func TestAckCallerFromCred2505(t *testing.T) {
	t.Parallel()
	const owner = "11111111-1111-1111-1111-111111111111"
	cases := []struct {
		name string
		cred credstore.Credential
		want []string
	}{
		{"stale grc_engineer claim, live viewer",
			credstore.Credential{ID: "jwt:2505", UserID: "user:" + owner, OwnerRoles: []string{"grc_engineer"}, TenantRoles: []string{"viewer"}},
			[]string{"viewer", "everyone"}},
		{"stale claim, no live role",
			credstore.Credential{ID: "jwt:2505", UserID: "user:" + owner, OwnerRoles: []string{"admin"}},
			nil},
		{"bound key, category label plus live viewer",
			credstore.Credential{ID: "key_2505", UserID: owner, OwnerRoles: []string{"employee"}, TenantRoles: []string{"viewer"}},
			[]string{"viewer", "employee", "everyone"}},
		// #2524: a bound key's free-text label is not a live role, so it
		// does not make its owner a member for an "everyone" policy.
		{"bound key, category label, no live role",
			credstore.Credential{ID: "key_2505", UserID: owner, OwnerRoles: []string{"employee"}},
			[]string{"employee"}},
		{"unbound key, category label",
			credstore.Credential{ID: "key_2505", UserID: "key_2505", OwnerRoles: []string{"employee"}},
			[]string{"employee", "everyone"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := CallerFromCred(tc.cred).OwnerRoles
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ack roles = %v; want %v", got, tc.want)
			}
		})
	}
}
