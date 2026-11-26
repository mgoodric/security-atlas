package authz_test

// #2105 — the risk treatment and acceptance transitions derive write,
// approve and revoke on "risks". The roles that write risks (admin,
// grc_engineer) may take each one; every other role is denied.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
)

func TestRiskAcceptance2105_AdmitsTheRiskWriteRoles(t *testing.T) {
	t.Parallel()
	e := engine(t)
	const tenant = "00000000-0000-4000-8000-000000002105"
	const id = "6f1c2a52-3b0e-4b8e-9a51-1d6a3f0c2105"
	build := func(path string) authz.Input {
		r := httptest.NewRequest(http.MethodPatch, path, nil)
		r = r.WithContext(authctx.WithCredential(r.Context(),
			credstore.Credential{ID: "jwt:2105", TenantID: tenant, UserID: "user-2105"}))
		return authz.BuildInput(r, nil)
	}
	wantAction := map[string]string{
		"/v1/risks/" + id + "/treatment":          "write",
		"/v1/risks/" + id + "/acceptance/propose": "write",
		"/v1/risks/" + id + "/acceptance/approve": "approve",
		"/v1/risks/" + id + "/acceptance/reject":  "write",
		"/v1/risks/" + id + "/acceptance/revoke":  "revoke",
	}
	for path, action := range wantAction {
		in := build(path)
		if in.Action != action || in.Resource.Type != "risks" {
			t.Fatalf("%s derives %s on %s, want %s on risks", path, in.Action, in.Resource.Type, action)
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
