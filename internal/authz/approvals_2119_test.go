package authz_test

import (
	"testing"

	"github.com/mgoodric/security-atlas/internal/authz"
)

func approvalsInput(roles []authz.Role, method, path string) authz.Input {
	action := "read"
	if method != "GET" {
		action = "write"
	}
	return authz.Input{
		User:     authz.UserInput{ID: "user-2119", Roles: roles, Attrs: map[string]interface{}{}},
		TenantID: tenant,
		Action:   action,
		Resource: authz.ResourceInput{Type: "approvals"},
		Request:  authz.RequestInput{Method: method, Path: path},
	}
}

// #2119: every signed-in role reads GET /v1/approvals; the handler lists
// only what the caller could approve.
func TestApprovals2119_EveryRoleReads(t *testing.T) {
	t.Parallel()
	for _, r := range []authz.Role{
		authz.RoleViewer, authz.RoleControlOwner, authz.RoleAuditor,
		authz.RoleGRCEngineer, authz.RoleAdmin,
	} {
		if d := decide(t, approvalsInput([]authz.Role{r}, "GET", "/v1/approvals")); !d.Allow {
			t.Errorf("%s denied GET /v1/approvals: %s", r, d.Reason)
		}
	}
}

// #2119: the admit is the exact read. A roleless credential, a sub-path
// and a write stay denied.
func TestApprovals2119_DeniesTheRest(t *testing.T) {
	t.Parallel()
	if d := decide(t, approvalsInput(nil, "GET", "/v1/approvals")); d.Allow {
		t.Error("roleless credential admitted to GET /v1/approvals")
	}
	for _, r := range []authz.Role{authz.RoleViewer, authz.RoleControlOwner, authz.RoleAuditor} {
		if d := decide(t, approvalsInput([]authz.Role{r}, "GET", "/v1/approvals/extra")); d.Allow {
			t.Errorf("%s admitted to GET /v1/approvals/extra", r)
		}
		if d := decide(t, approvalsInput([]authz.Role{r}, "POST", "/v1/approvals")); d.Allow {
			t.Errorf("%s admitted to POST /v1/approvals", r)
		}
	}
}
