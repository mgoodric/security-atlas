package authz_test

// #2176 — GET /v1/controls/needs-reanchoring is a controls read. BuildInput
// derives the same action and resource as GET /v1/controls, so the rego
// bundle admits exactly the roles that may already read controls, and the
// dashboard warning's count is gated the same way as the control list.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
)

func TestNeedsReanchoring2176_SameAdmitSetAsControlList(t *testing.T) {
	t.Parallel()
	e := engine(t)
	const tenant = "00000000-0000-4000-8000-000000002176"
	build := func(path string) authz.Input {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r = r.WithContext(authctx.WithCredential(r.Context(),
			credstore.Credential{ID: "jwt:2176", TenantID: tenant, UserID: "user-2176"}))
		return authz.BuildInput(r, nil)
	}
	list := build("/v1/controls")
	flagged := build("/v1/controls/needs-reanchoring")
	if flagged.Action != list.Action || flagged.Resource.Type != list.Resource.Type {
		t.Fatalf("needs-reanchoring derives %s on %s; the control list derives %s on %s",
			flagged.Action, flagged.Resource.Type, list.Action, list.Resource.Type)
	}
	if flagged.Action != "read" || flagged.Resource.Type != "controls" {
		t.Fatalf("needs-reanchoring derives %s on %s, want read on controls", flagged.Action, flagged.Resource.Type)
	}

	for _, c := range []struct {
		name  string
		roles []authz.Role
	}{
		{"admin", []authz.Role{authz.RoleAdmin}},
		{"grc_engineer", []authz.Role{authz.RoleGRCEngineer}},
		{"auditor", []authz.Role{authz.RoleAuditor}},
		{"control_owner", []authz.Role{authz.RoleControlOwner}},
		{"viewer", []authz.Role{authz.RoleViewer}},
		{"no_role", []authz.Role{}},
	} {
		decide := func(in authz.Input) bool {
			in.User.Roles = c.roles
			d, err := e.Decide(context.Background(), in)
			if err != nil {
				t.Fatalf("%s: Decide: %v", c.name, err)
			}
			return d.Allow
		}
		if got, want := decide(flagged), decide(list); got != want {
			t.Errorf("%s: needs-reanchoring allow=%v, control list allow=%v; want the same", c.name, got, want)
		}
	}
	// The read roles named in the ticket review are admitted; no role is not.
	for _, c := range []struct {
		role authz.Role
		want bool
	}{{authz.RoleAuditor, true}, {authz.RoleViewer, true}} {
		in := flagged
		in.User.Roles = []authz.Role{c.role}
		d, err := e.Decide(context.Background(), in)
		if err != nil {
			t.Fatal(err)
		}
		if d.Allow != c.want {
			t.Errorf("%s: allow=%v, want %v (reason=%s)", c.role, d.Allow, c.want, d.Reason)
		}
	}
	none := flagged
	none.User.Roles = []authz.Role{}
	if d, _ := e.Decide(context.Background(), none); d.Allow {
		t.Error("no role: allowed, want denied")
	}
}
