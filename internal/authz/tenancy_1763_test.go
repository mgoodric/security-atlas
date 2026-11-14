package authz_test

import (
	"testing"

	"github.com/mgoodric/security-atlas/internal/authz"
)

func meReadInput(roles []authz.Role, path string) authz.Input {
	return authz.Input{
		User:     authz.UserInput{ID: "user-1763", Roles: roles, Attrs: map[string]interface{}{}},
		TenantID: tenant,
		Action:   "read",
		Resource: authz.ResourceInput{Type: "me"},
		Request:  authz.RequestInput{Method: "GET", Path: path},
	}
}

// #1763: every signed-in role reads GET /v1/me/tenancy, the tenant-count
// signal the web app uses for tenant chrome.
func TestTenancy1763_EveryRoleReads(t *testing.T) {
	t.Parallel()
	for _, r := range []authz.Role{
		authz.RoleViewer, authz.RoleControlOwner, authz.RoleAuditor,
		authz.RoleGRCEngineer, authz.RoleAdmin,
	} {
		if d := decide(t, meReadInput([]authz.Role{r}, "/v1/me/tenancy")); !d.Allow {
			t.Errorf("%s denied GET /v1/me/tenancy: %s", r, d.Reason)
		}
	}
}

// #1763: the admit needs a role. #2015 widened the path list; its tests
// pin which /v1/me paths stay denied.
func TestTenancy1763_NarrowAdmit(t *testing.T) {
	t.Parallel()
	if d := decide(t, meReadInput(nil, "/v1/me/tenancy")); d.Allow {
		t.Error("roleless credential admitted to GET /v1/me/tenancy")
	}
}
