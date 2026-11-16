package authz_test

// #1987 — DELETE /v1/policies/{id} (discard a draft) needs the same role as
// editing a policy draft. BuildInput derives the same action and resource
// for the discard as for POST /v1/policies (create), so the rego bundle
// admits exactly the roles that may write policies: admin and grc_engineer
// admit; auditor, control_owner, viewer and no role deny.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
)

func TestPolicyDiscard1987_SameAdmitSetAsCreate(t *testing.T) {
	t.Parallel()
	e := engine(t)
	const tenant = "00000000-0000-4000-8000-000000001987"
	build := func(method, path string) authz.Input {
		r := httptest.NewRequest(method, path, nil)
		r = r.WithContext(authctx.WithCredential(r.Context(),
			credstore.Credential{ID: "jwt:1987", TenantID: tenant, UserID: "user-1987"}))
		return authz.BuildInput(r, nil)
	}
	create := build(http.MethodPost, "/v1/policies")
	discard := build(http.MethodDelete, "/v1/policies/6f1c2a52-3b0e-4b8e-9a51-1d6a3f0c1987")
	if discard.Action != create.Action || discard.Resource.Type != create.Resource.Type {
		t.Fatalf("discard derives %s on %s; create derives %s on %s",
			discard.Action, discard.Resource.Type, create.Action, create.Resource.Type)
	}
	if discard.Action != "write" || discard.Resource.Type != "policies" {
		t.Fatalf("discard derives %s on %s, want write on policies", discard.Action, discard.Resource.Type)
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
		for name, in := range map[string]authz.Input{"create": create, "discard": discard} {
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
