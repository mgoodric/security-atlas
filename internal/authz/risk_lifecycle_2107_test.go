package authz_test

// #2107 — the risk lifecycle. Close and reopen derive write on "risks",
// which the risk writers (admin, grc_engineer) may take. Hard delete,
// DELETE /v1/risks/{id}, is admin-only: grc_engineer is refused, while
// the deletes under a risk and of a category stay grc_engineer's.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
)

const (
	lifecycle2107Tenant = "00000000-0000-4000-8000-000000002107"
	lifecycle2107Risk   = "6f1c2a52-3b0e-4b8e-9a51-1d6a3f0c2107"
)

func lifecycle2107Input(method, path string) authz.Input {
	r := httptest.NewRequest(method, path, nil)
	r = r.WithContext(authctx.WithCredential(r.Context(),
		credstore.Credential{ID: "jwt:2107", TenantID: lifecycle2107Tenant, UserID: "user-2107"}))
	return authz.BuildInput(r, nil)
}

type lifecycle2107Case struct {
	name  string
	roles []authz.Role
	want  bool
}

func decide2107(t *testing.T, e *authz.Engine, in authz.Input, cases []lifecycle2107Case) {
	t.Helper()
	for _, c := range cases {
		in.User.Roles = c.roles
		d, err := e.Decide(context.Background(), in)
		if err != nil {
			t.Fatalf("%s %s %s: Decide: %v", c.name, in.Request.Method, in.Request.Path, err)
		}
		if d.Allow != c.want {
			t.Errorf("%s %s %s: allow=%v, want %v (reason=%s)",
				c.name, in.Request.Method, in.Request.Path, d.Allow, c.want, d.Reason)
		}
	}
}

func TestRiskLifecycle2107_CloseAndReopenAdmitTheRiskWriters(t *testing.T) {
	t.Parallel()
	e := engine(t)
	for _, suffix := range []string{"/close", "/reopen"} {
		in := lifecycle2107Input(http.MethodPatch, "/v1/risks/"+lifecycle2107Risk+suffix)
		if in.Action != "write" || in.Resource.Type != "risks" {
			t.Fatalf("%s derives %s on %s, want write on risks", suffix, in.Action, in.Resource.Type)
		}
		decide2107(t, e, in, []lifecycle2107Case{
			{"admin", []authz.Role{authz.RoleAdmin}, true},
			{"grc_engineer", []authz.Role{authz.RoleGRCEngineer}, true},
			{"auditor", []authz.Role{authz.RoleAuditor}, false},
			{"control_owner", []authz.Role{authz.RoleControlOwner}, false},
			{"viewer", []authz.Role{authz.RoleViewer}, false},
			{"no_role", []authz.Role{}, false},
		})
	}
}

func TestRiskLifecycle2107_HardDeleteIsAdminOnly(t *testing.T) {
	t.Parallel()
	e := engine(t)
	in := lifecycle2107Input(http.MethodDelete, "/v1/risks/"+lifecycle2107Risk)
	decide2107(t, e, in, []lifecycle2107Case{
		{"admin", []authz.Role{authz.RoleAdmin}, true},
		{"grc_engineer", []authz.Role{authz.RoleGRCEngineer}, false},
		{"auditor", []authz.Role{authz.RoleAuditor}, false},
		{"control_owner", []authz.Role{authz.RoleControlOwner}, false},
		{"viewer", []authz.Role{authz.RoleViewer}, false},
		{"no_role", []authz.Role{}, false},
	})
}

func TestRiskLifecycle2107_DeletesUnderARiskStayGRCEngineers(t *testing.T) {
	t.Parallel()
	e := engine(t)
	for _, path := range []string{
		"/v1/risks/" + lifecycle2107Risk + "/children/" + lifecycle2107Risk,
		"/v1/risks/" + lifecycle2107Risk + "/themes/cloud",
		"/v1/risks/categories/operational",
	} {
		decide2107(t, e, lifecycle2107Input(http.MethodDelete, path), []lifecycle2107Case{
			{"grc_engineer", []authz.Role{authz.RoleGRCEngineer}, true},
		})
	}
}
