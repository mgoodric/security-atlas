package authz_test

import (
	"slices"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
)

// TestLiveRoleLabels2505 pins which roles a handler gate counts per
// credential shape (#2505). A user's session and a bound key count their
// live roles alone; a credential that names no user counts its own label.
// AckRoleLabels also counts a bound key's label, the only place a user holds
// a free-text ack role.
func TestLiveRoleLabels2505(t *testing.T) {
	t.Parallel()
	const owner = "11111111-1111-1111-1111-111111111111"
	cases := []struct {
		name   string
		cred   credstore.Credential
		labels []string // TenantRoleLabels
		any    bool     // HasAnyTenantRole
		ack    []string // AckRoleLabels
	}{
		{
			name:   "session, stale admin claim, live viewer",
			cred:   credstore.Credential{ID: "jwt:a", UserID: "user:" + owner, OwnerRoles: []string{"admin"}, TenantRoles: []string{"viewer"}},
			labels: []string{"viewer"}, any: true, ack: []string{"viewer"},
		},
		{
			name: "session, stale claim, no live role",
			cred: credstore.Credential{ID: "jwt:a", UserID: "user:" + owner, OwnerRoles: []string{"control_owner"}},
		},
		{
			name:   "machine token, claim is its grant",
			cred:   credstore.Credential{ID: "jwt:m", UserID: "oauth_client:c1", OwnerRoles: []string{"auditor"}},
			labels: []string{"auditor"}, any: true, ack: []string{"auditor"},
		},
		{
			name:   "unbound key, label is its grant",
			cred:   credstore.Credential{ID: "key_k", UserID: "key_k", OwnerRoles: []string{"control_owner"}},
			labels: []string{"control_owner"}, any: true, ack: []string{"control_owner"},
		},
		{
			name:   "bound key, stale label, live viewer",
			cred:   credstore.Credential{ID: "key_k", UserID: owner, OwnerRoles: []string{"grc_engineer", "employee"}, TenantRoles: []string{"viewer"}},
			labels: []string{"viewer"}, any: true, ack: []string{"viewer", "grc_engineer", "employee"},
		},
		{
			name: "bound key, owner holds no live role",
			cred: credstore.Credential{ID: "key_k", UserID: owner, OwnerRoles: []string{"control_owner"}},
			ack:  []string{"control_owner"},
		},
		{
			name:   "in-memory credential, label counts",
			cred:   credstore.Credential{ID: "cred_1", OwnerRoles: []string{"control_owner"}},
			labels: []string{"control_owner"}, any: true, ack: []string{"control_owner"},
		},
		{
			name: "approver flag, no role",
			cred: credstore.Credential{ID: "jwt:a", UserID: "user:" + owner, IsApprover: true},
			any:  true,
		},
		{
			name: "bare connector credential",
			cred: credstore.Credential{ID: "cred_2"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := authz.TenantRoleLabels(tc.cred); !slices.Equal(got, tc.labels) && len(got)+len(tc.labels) > 0 {
				t.Errorf("TenantRoleLabels = %v; want %v", got, tc.labels)
			}
			if got := authz.HasAnyTenantRole(tc.cred); got != tc.any {
				t.Errorf("HasAnyTenantRole = %v; want %v", got, tc.any)
			}
			if got := authz.AckRoleLabels(tc.cred); !slices.Equal(got, tc.ack) && len(got)+len(tc.ack) > 0 {
				t.Errorf("AckRoleLabels = %v; want %v", got, tc.ack)
			}
		})
	}
}
