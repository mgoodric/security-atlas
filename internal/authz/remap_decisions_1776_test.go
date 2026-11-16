package authz_test

// #1776 — deciding a remap proposal re-anchors or keeps a control, so it
// needs the existing control-edit admit: /approve derives action "approve"
// and the other decisions "write", all on resource "controls". admin and
// grc_engineer admit; auditor, control_owner, viewer and no role deny.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
)

func TestRemapDecisions1776_ControlEditAdmitSet(t *testing.T) {
	t.Parallel()
	e := engine(t)
	const tenant = "00000000-0000-4000-8000-000000001776"
	const id = "6f1c2a52-3b0e-4b8e-9a51-1d6a3f0c1776"
	build := func(path string) authz.Input {
		r := httptest.NewRequest(http.MethodPost, path, nil)
		r = r.WithContext(authctx.WithCredential(r.Context(),
			credstore.Credential{ID: "jwt:1776", TenantID: tenant, UserID: "user-1776"}))
		return authz.BuildInput(r, nil)
	}
	routes := map[string]string{
		"/v1/controls/remap-proposals/" + id + "/approve":     "approve",
		"/v1/controls/remap-proposals/" + id + "/reject":      "write",
		"/v1/controls/remap-proposals/" + id + "/keep-legacy": "write",
		"/v1/controls/remap-proposals/approve-unchanged":      "write",
	}
	for path, action := range routes {
		in := build(path)
		if in.Action != action || in.Resource.Type != "controls" {
			t.Fatalf("%s derives %s on %s, want %s on controls", path, in.Action, in.Resource.Type, action)
		}
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
			in.User.Roles = c.roles
			d, err := e.Decide(context.Background(), in)
			if err != nil {
				t.Fatalf("%s %s: Decide: %v", c.name, path, err)
			}
			if d.Allow != c.want {
				t.Errorf("%s %s: allow=%v, want %v (reason=%s)", c.name, path, d.Allow, c.want, d.Reason)
			}
		}
	}
}
