package authz_test

// #2110 — OPA admits POST /v1/controls/{uuid}/evaluate for admin,
// grc_engineer (controls write) and control_owner (the narrow path rule in
// control_owner.rego), and denies auditor and viewer. Which control the
// caller may evaluate is the handler's check (owner_user_id); this pins the
// role layer, and that the control_owner rule gives no other controls write.

import (
	"context"
	"testing"

	"github.com/mgoodric/security-atlas/internal/authz"
)

const evaluate2110Path = "/v1/controls/00000000-0000-4000-8000-0000000021c0/evaluate"

func decide2110(t *testing.T, e *authz.Engine, roles []authz.Role, method, path string) bool {
	t.Helper()
	d, err := e.Decide(context.Background(), authz.Input{
		User:     authz.UserInput{ID: "user-2110", Roles: roles},
		TenantID: "00000000-0000-0000-0000-000000002110",
		Action:   "write",
		Resource: authz.ResourceInput{Type: "controls", ID: "00000000-0000-4000-8000-0000000021c0"},
		Request:  authz.RequestInput{Method: method, Path: path},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	return d.Allow
}

func TestDecide2110_EvaluateRoleMatrix(t *testing.T) {
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
		if got := decide2110(t, e, []authz.Role{tc.role}, "POST", evaluate2110Path); got != tc.want {
			t.Errorf("%s: allow = %v; want %v", tc.role, got, tc.want)
		}
	}
	if decide2110(t, e, nil, "POST", evaluate2110Path) {
		t.Error("no roles: allowed; want default-deny")
	}
}

func TestDecide2110_ControlOwnerRuleIsNarrow(t *testing.T) {
	t.Parallel()
	e := engine(t)
	owner := []authz.Role{authz.RoleControlOwner}
	for _, c := range []struct{ method, path string }{
		{"POST", "/v1/controls/00000000-0000-4000-8000-0000000021c0/owner"},
		{"POST", "/v1/controls:upload-bundle"},
		{"POST", "/v1/controls/not-a-uuid/evaluate"},
		{"POST", evaluate2110Path + "/x"},
		{"DELETE", evaluate2110Path},
	} {
		if decide2110(t, e, owner, c.method, c.path) {
			t.Errorf("control_owner %s %s: allowed; want deny", c.method, c.path)
		}
	}
}
