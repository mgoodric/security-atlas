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

// #1763: the admit is pinned to the one path and needs a role. A viewer
// still cannot read other /v1/me endpoints, and a roleless credential is
// denied.
func TestTenancy1763_NarrowAdmit(t *testing.T) {
	t.Parallel()
	if d := decide(t, meReadInput([]authz.Role{authz.RoleViewer}, "/v1/me/tenants")); d.Allow {
		t.Error("viewer admitted to GET /v1/me/tenants; the #1763 rule must not widen other /v1/me paths")
	}
	if d := decide(t, meReadInput(nil, "/v1/me/tenancy")); d.Allow {
		t.Error("roleless credential admitted to GET /v1/me/tenancy")
	}
}
