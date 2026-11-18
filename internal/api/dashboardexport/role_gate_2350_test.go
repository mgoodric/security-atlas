// #2350 — the dashboard export is gated by OPA on read of the dashboard
// resource. These tests drive ExportDashboard with a real engine (the
// embedded policies, roles from a fixed resolver), so no database is
// needed: the meta-audit write is skipped with a nil pool.

package dashboardexport

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
)

const gateTenant = "00000000-0000-0000-0000-000000000001"

type gateRoles []authz.Role

func (f gateRoles) RolesFor(context.Context, string, string) ([]authz.Role, error) {
	return f, nil
}

func gateEngine(t *testing.T, roles ...authz.Role) *authz.Engine {
	t.Helper()
	engine, err := authz.NewEngine(context.Background(), gateRoles(roles))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return engine
}

// session is a user's browser session as jwtmw builds it: a "jwt:"
// credential naming the user, with the live user_roles authzmw resolved.
func session(roles ...string) credstore.Credential {
	return credstore.Credential{
		ID:          "jwt:2350",
		TenantID:    gateTenant,
		UserID:      "33333333-3333-3333-3333-333333333333",
		TenantRoles: roles,
	}
}

func exportAs(t *testing.T, engine *authz.Engine, cred credstore.Credential) int {
	t.Helper()
	h := NewHandler(nil, &stubSource{snap: fixtureSnapshot()}).WithAuthz(engine)
	req := httptest.NewRequest("GET", "/v1/dashboard/export?format=json", nil)
	req = req.WithContext(authctx.WithCredential(req.Context(), cred))
	rec := httptest.NewRecorder()
	h.ExportDashboard(rec, req)
	return rec.Code
}

// TestExportGate2350_PerRole: a tenant admin, a super admin, a
// grc_engineer and an auditor export; a viewer and a control owner get
// 403, as OPA decides on the dashboard resource.
func TestExportGate2350_PerRole(t *testing.T) {
	superAdmin := session()
	superAdmin.IsAdmin = true // jwtmw sets it from the atlas:super_admin claim

	cases := []struct {
		name   string
		engine *authz.Engine
		cred   credstore.Credential
		want   int
	}{
		{"tenant_admin", gateEngine(t, authz.RoleAdmin), session("admin"), 200},
		{"super_admin", gateEngine(t), superAdmin, 200},
		{"grc_engineer", gateEngine(t, authz.RoleGRCEngineer), session("grc_engineer"), 200},
		{"auditor", gateEngine(t, authz.RoleAuditor), session("auditor"), 200},
		{"viewer", gateEngine(t, authz.RoleViewer), session("viewer"), 403},
		{"control_owner", gateEngine(t, authz.RoleControlOwner), session("control_owner"), 403},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := exportAs(t, tc.engine, tc.cred); got != tc.want {
				t.Fatalf("status = %d; want %d", got, tc.want)
			}
		})
	}
}

// TestExportGate2350_OPARefusalWins: the handler asks OPA, so a caller
// the twin admits is refused when the engine denies.
func TestExportGate2350_OPARefusalWins(t *testing.T) {
	if got := exportAs(t, gateEngine(t, authz.RoleViewer), session("admin")); got != 403 {
		t.Fatalf("status = %d; want 403", got)
	}
}

// TestExportGate2350_BarePushKeyRefused: OPA derives grc_engineer for a
// credential with no flags and no label, so the twin is what keeps a bare
// push key out.
func TestExportGate2350_BarePushKeyRefused(t *testing.T) {
	cred := credstore.Credential{ID: "key_push", TenantID: gateTenant}
	if got := exportAs(t, gateEngine(t), cred); got != 403 {
		t.Fatalf("status = %d; want 403", got)
	}
}
