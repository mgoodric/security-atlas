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

	// Each role gets the same answer as the control list, and the explicit
	// answer below: every role that reads controls (auditor and viewer
	// included) is admitted; no role is denied.
	for _, c := range []struct {
		name  string
		roles []authz.Role
		want  bool
	}{
		{"admin", []authz.Role{authz.RoleAdmin}, true},
		{"grc_engineer", []authz.Role{authz.RoleGRCEngineer}, true},
		{"auditor", []authz.Role{authz.RoleAuditor}, true},
		{"control_owner", []authz.Role{authz.RoleControlOwner}, true},
		{"viewer", []authz.Role{authz.RoleViewer}, true},
		{"no_role", []authz.Role{}, false},
	} {
		decide := func(in authz.Input) authz.Decision {
			in.User.Roles = c.roles
			d, err := e.Decide(context.Background(), in)
			if err != nil {
				t.Fatalf("%s: Decide: %v", c.name, err)
			}
			return d
		}
		got, listed := decide(flagged), decide(list)
		if got.Allow != listed.Allow {
			t.Errorf("%s: needs-reanchoring allow=%v, control list allow=%v; want the same", c.name, got.Allow, listed.Allow)
		}
		if got.Allow != c.want {
			t.Errorf("%s: allow=%v, want %v (reason=%s)", c.name, got.Allow, c.want, got.Reason)
		}
	}
}
