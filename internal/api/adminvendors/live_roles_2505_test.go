package adminvendors

import (
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
)

// TestCallerAllowedExport2505 pins the export gate to live tenant roles: a
// JWT session whose role claim still names auditor or grc_engineer after a
// revoke is refused when its live role is narrower (#2505).
func TestCallerAllowedExport2505(t *testing.T) {
	t.Parallel()
	session := func(claim, live []string) credstore.Credential {
		return credstore.Credential{ID: "jwt:2505", UserID: "user:2505", TenantID: "t", OwnerRoles: claim, TenantRoles: live}
	}
	cases := []struct {
		name string
		cred credstore.Credential
		want bool
	}{
		{"stale auditor claim, live viewer", session([]string{"auditor"}, []string{"viewer"}), false},
		{"stale grc_engineer claim, live control_owner", session([]string{"grc_engineer"}, []string{"control_owner"}), false},
		{"stale claim, no live role", session([]string{"grc_engineer"}, nil), false},
		{"live auditor, empty claim", session(nil, []string{"auditor"}), true},
		{"live grc_engineer", session([]string{"grc_engineer"}, []string{"grc_engineer"}), true},
		{"admin flag", credstore.Credential{ID: "jwt:2505", IsAdmin: true}, true},
		{"unbound key, auditor label", credstore.Credential{ID: "key_2505", UserID: "key_2505", OwnerRoles: []string{"auditor"}}, true},
		{"bound key, stale auditor label, live viewer", credstore.Credential{ID: "key_2505", UserID: "11111111-1111-1111-1111-111111111111", OwnerRoles: []string{"auditor"}, TenantRoles: []string{"viewer"}}, false},
		{"bound key, live grc_engineer", credstore.Credential{ID: "key_2505", UserID: "11111111-1111-1111-1111-111111111111", TenantRoles: []string{"grc_engineer"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := callerAllowedExport(tc.cred); got != tc.want {
				t.Errorf("callerAllowedExport = %v; want %v", got, tc.want)
			}
		})
	}
}
