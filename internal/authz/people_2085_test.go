package authz_test

import (
	"testing"

	"github.com/mgoodric/security-atlas/internal/authz"
)

func peopleInput(roles []authz.Role, method, path string) authz.Input {
	action := "read"
	if method != "GET" {
		action = "write"
	}
	return authz.Input{
		User:     authz.UserInput{ID: "user-2085", Roles: roles, Attrs: map[string]interface{}{}},
		TenantID: tenant,
		Action:   action,
		Resource: authz.ResourceInput{Type: "people"},
		Request:  authz.RequestInput{Method: method, Path: path},
	}
}

// #2085: every signed-in role reads GET /v1/people.
func TestPeople2085_EveryRoleReads(t *testing.T) {
	t.Parallel()
	for _, r := range []authz.Role{
		authz.RoleViewer, authz.RoleControlOwner, authz.RoleAuditor,
		authz.RoleGRCEngineer, authz.RoleAdmin,
	} {
		if d := decide(t, peopleInput([]authz.Role{r}, "GET", "/v1/people")); !d.Allow {
			t.Errorf("%s denied GET /v1/people: %s", r, d.Reason)
		}
	}
}

// #2085: the admit is the exact read. A roleless credential, a sub-path
// and a write stay denied.
func TestPeople2085_DeniesTheRest(t *testing.T) {
	t.Parallel()
	if d := decide(t, peopleInput(nil, "GET", "/v1/people")); d.Allow {
		t.Error("roleless credential admitted to GET /v1/people")
	}
	for _, r := range []authz.Role{authz.RoleViewer, authz.RoleControlOwner, authz.RoleAuditor} {
		if d := decide(t, peopleInput([]authz.Role{r}, "GET", "/v1/people/extra")); d.Allow {
			t.Errorf("%s admitted to GET /v1/people/extra", r)
		}
		if d := decide(t, peopleInput([]authz.Role{r}, "POST", "/v1/people")); d.Allow {
			t.Errorf("%s admitted to POST /v1/people", r)
		}
	}
}
