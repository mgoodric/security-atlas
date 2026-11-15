package authz_test

// #1775 — GET /v1/controls/remap-proposals is a controls read. BuildInput
// derives the same action and resource as GET /v1/controls, so the rego
// bundle admits exactly the roles that may already read controls.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
)

func TestRemapProposals1775_SameAdmitSetAsControlList(t *testing.T) {
	t.Parallel()
	e := engine(t)
	const tenant = "00000000-0000-4000-8000-000000001775"
	build := func(path string) authz.Input {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r = r.WithContext(authctx.WithCredential(r.Context(),
			credstore.Credential{ID: "jwt:1775", TenantID: tenant, UserID: "user-1775"}))
		return authz.BuildInput(r, nil)
	}
	list := build("/v1/controls")
	queue := build("/v1/controls/remap-proposals?class=split&status=open")
	if queue.Action != "read" || queue.Resource.Type != "controls" {
		t.Fatalf("remap-proposals derives %s on %s, want read on controls", queue.Action, queue.Resource.Type)
	}
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
		got, listed := decide(queue), decide(list)
		if got.Allow != listed.Allow || got.Allow != c.want {
			t.Errorf("%s: remap-proposals allow=%v, control list allow=%v, want %v (reason=%s)", c.name, got.Allow, listed.Allow, c.want, got.Reason)
		}
	}
}
