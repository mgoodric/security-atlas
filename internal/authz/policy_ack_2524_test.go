package authz_test

// #2524 — OPA admits POST /v1/policies/{uuid}/acknowledge for every role, not
// only admin and grc_engineer, and denies a roleless credential. Whether the
// policy is aimed at the caller, and that the ack is the caller's own, is the
// handler's check; this pins the role layer, and that the rule gives no other
// policies write.

import (
	"context"
	"testing"

	"github.com/mgoodric/security-atlas/internal/authz"
)

// decide2524 asks OPA as authzmw does for a policies route: type
// "policies" and action "write" for every non-GET method.
func decide2524(t *testing.T, e *authz.Engine, roles []authz.Role, method, path string) bool {
	t.Helper()
	d, err := e.Decide(context.Background(), authz.Input{
		User:     authz.UserInput{ID: "user-2524", Roles: roles},
		TenantID: "00000000-0000-0000-0000-000000002524",
		Action:   "write",
		Resource: authz.ResourceInput{Type: "policies", ID: "00000000-0000-4000-8000-000000002524"},
		Request:  authz.RequestInput{Method: method, Path: path},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	return d.Allow
}

const ack2524Path = "/v1/policies/00000000-0000-4000-8000-000000002524/acknowledge"

func TestDecide2524_AcknowledgeRoleMatrix(t *testing.T) {
	t.Parallel()
	e := engine(t)
	for _, role := range authz.CanonicalRoles() {
		if !decide2524(t, e, []authz.Role{role}, "POST", ack2524Path) {
			t.Errorf("%s: acknowledge denied; want allow", role)
		}
	}
	if decide2524(t, e, nil, "POST", ack2524Path) {
		t.Error("no roles: allowed; want default-deny")
	}
}

func TestDecide2524_AcknowledgeRuleIsNarrow(t *testing.T) {
	t.Parallel()
	e := engine(t)
	for _, role := range []authz.Role{authz.RoleViewer, authz.RoleAuditor, authz.RoleControlOwner} {
		for _, c := range []struct{ method, path string }{
			{"POST", "/v1/policies/not-a-uuid/acknowledge"},
			{"POST", ack2524Path + "/x"},
			{"PUT", ack2524Path},
			{"DELETE", ack2524Path},
			{"POST", "/v1/policies"},
			{"PATCH", "/v1/policies/00000000-0000-4000-8000-000000002524"},
			{"DELETE", "/v1/policies/00000000-0000-4000-8000-000000002524"},
		} {
			if decide2524(t, e, []authz.Role{role}, c.method, c.path) {
				t.Errorf("%s %s %s: allowed; want deny", role, c.method, c.path)
			}
		}
	}
}
