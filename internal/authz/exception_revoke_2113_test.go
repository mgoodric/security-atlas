package authz_test

// #2113 — PATCH /v1/exceptions/{id}/revoke derives action "revoke" on
// "exceptions". The roles that approve and activate an exception (admin,
// grc_engineer) may revoke one; every other role is denied. The admit is
// bound to exceptions: grc_engineer still cannot revoke on another
// resource, where revoke stays admin's.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
)

func TestExceptionRevoke2113_AdmitsTheActivateRoles(t *testing.T) {
	t.Parallel()
	e := engine(t)
	const tenant = "00000000-0000-4000-8000-000000002113"
	build := func(method, path string) authz.Input {
		r := httptest.NewRequest(method, path, nil)
		r = r.WithContext(authctx.WithCredential(r.Context(),
			credstore.Credential{ID: "jwt:2113", TenantID: tenant, UserID: "user-2113"}))
		return authz.BuildInput(r, nil)
	}
	const id = "6f1c2a52-3b0e-4b8e-9a51-1d6a3f0c2113"
	revoke := build(http.MethodPatch, "/v1/exceptions/"+id+"/revoke")
	if revoke.Action != "revoke" || revoke.Resource.Type != "exceptions" {
		t.Fatalf("revoke derives %s on %s, want revoke on exceptions", revoke.Action, revoke.Resource.Type)
	}
	activate := build(http.MethodPatch, "/v1/exceptions/"+id+"/activate")

	for _, c := range []struct {
		name  string
		roles []authz.Role
		want  bool
	}{
		{"admin", []authz.Role{authz.RoleAdmin}, true},
		{"grc_engineer", []authz.Role{authz.RoleGRCEngineer}, true},
		{"auditor", []authz.Role{authz.RoleAuditor}, false},
		{"control_owner", []authz.Role{authz.RoleControlOwner}, false},
		{"viewer", []authz.Role{authz.RoleViewer}, false},
		{"no_role", []authz.Role{}, false},
	} {
		for name, in := range map[string]authz.Input{"activate": activate, "revoke": revoke} {
			in.User.Roles = c.roles
			d, err := e.Decide(context.Background(), in)
			if err != nil {
				t.Fatalf("%s/%s: Decide: %v", c.name, name, err)
			}
			if d.Allow != c.want {
				t.Errorf("%s/%s: allow=%v, want %v (reason=%s)", c.name, name, d.Allow, c.want, d.Reason)
			}
		}
	}
}

func TestExceptionRevoke2113_GRCRevokeStaysOnExceptions(t *testing.T) {
	t.Parallel()
	e := engine(t)
	in := authz.Input{
		User:     authz.UserInput{ID: "user-2113", Roles: []authz.Role{authz.RoleGRCEngineer}},
		TenantID: "00000000-0000-4000-8000-000000002113",
		Action:   "revoke",
		Resource: authz.ResourceInput{Type: "api-keys"},
	}
	d, err := e.Decide(context.Background(), in)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d.Allow {
		t.Fatalf("grc_engineer revoke on api-keys allowed; the #2113 admit must stay on exceptions")
	}
}
