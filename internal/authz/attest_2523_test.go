package authz_test

// #2523 — OPA admits POST /v1/controls/{uuid}/attestations for admin,
// grc_engineer (controls write) and control_owner (the narrow path rule in
// control_owner.rego), and denies auditor and viewer. Whether the caller
// holds the control's owner_role is the handler's check
// (authz.HasOwnerRole); this pins the role layer, and that the control_owner
// rule gives no other controls write.

import (
	"testing"

	"github.com/mgoodric/security-atlas/internal/authz"
)

const attest2523Path = "/v1/controls/00000000-0000-4000-8000-000000002523/attestations"

func TestDecide2523_AttestRoleMatrix(t *testing.T) {
	t.Parallel()
	e := engine(t)
	cases := []struct {
		role authz.Role
		want bool
	}{
		{authz.RoleAdmin, true},
		{authz.RoleGRCEngineer, true},
		{authz.RoleControlOwner, true},
		{authz.RoleAuditor, false},
		{authz.RoleViewer, false},
	}
	for _, tc := range cases {
		if got := decide2110(t, e, []authz.Role{tc.role}, "POST", attest2523Path); got != tc.want {
			t.Errorf("%s: allow = %v; want %v", tc.role, got, tc.want)
		}
	}
	if decide2110(t, e, nil, "POST", attest2523Path) {
		t.Error("no roles: allowed; want default-deny")
	}
}

func TestDecide2523_ControlOwnerAttestRuleIsNarrow(t *testing.T) {
	t.Parallel()
	e := engine(t)
	owner := []authz.Role{authz.RoleControlOwner}
	for _, c := range []struct{ method, path string }{
		{"POST", "/v1/controls/not-a-uuid/attestations"},
		{"POST", attest2523Path + "/x"},
		{"POST", "/v1/controls/00000000-0000-4000-8000-000000002523/attestations:bulk"},
		{"PUT", attest2523Path},
		{"DELETE", attest2523Path},
	} {
		if decide2110(t, e, owner, c.method, c.path) {
			t.Errorf("control_owner %s %s: allowed; want deny", c.method, c.path)
		}
	}
}
