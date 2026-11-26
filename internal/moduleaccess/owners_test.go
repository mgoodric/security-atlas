package moduleaccess

import (
	"testing"

	"github.com/mgoodric/security-atlas/internal/authz"
)

// #2501: a person with no live role lacks every optional module unless a
// super-admin; admin and auditor are exempt from narrowing; any other
// live role follows the stored access, where nil is "all".
func TestMemberLacks(t *testing.T) {
	t.Parallel()
	grc := []authz.Role{authz.RoleGRCEngineer}
	cases := []struct {
		name    string
		super   bool
		roles   []authz.Role
		modules []string
		want    bool
	}{
		{"no live role lacks", false, nil, nil, true},
		{"no live role, stored all, still lacks", false, []authz.Role{}, nil, true},
		{"no live role, super-admin has", true, nil, nil, false},
		{"live role on all has", false, grc, nil, false},
		{"live role narrowed in has", false, grc, []string{"risk"}, false},
		{"live role narrowed out lacks", false, grc, []string{"policies"}, true},
		{"super-admin narrowed out has", true, grc, []string{}, false},
		{"admin narrowed out has", false, []authz.Role{authz.RoleAdmin}, []string{}, false},
		{"auditor narrowed out has", false, []authz.Role{authz.RoleAuditor}, []string{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := memberLacks(c.super, c.roles, c.modules, "risk"); got != c.want {
				t.Fatalf("memberLacks(%v, %v, %v, risk) = %v; want %v", c.super, c.roles, c.modules, got, c.want)
			}
		})
	}
}
