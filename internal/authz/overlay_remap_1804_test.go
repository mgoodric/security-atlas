package authz_test

// #1804 — overlay-edge remap proposals are tenant-owned, so OPA admits the
// decisions for the tenant's writers (admin, grc_engineer) under the
// existing `mapping-overlays` resource, with no super_admin carve-out.
// Every other role is refused, the list included, exactly as for the
// overlay edges themselves. The handler checks the role again and refuses
// a machine credential (internal/api/mappingoverlays).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
)

func TestOverlayRemap1804_TenantWritersDecide(t *testing.T) {
	t.Parallel()
	e := engine(t)
	const tenant = "00000000-0000-4000-8000-000000001804"
	const id = "6f1c2a52-3b0e-4b8e-9a51-1d6a3f0c1804"
	routes := []struct{ method, path, action string }{
		{http.MethodGet, "/v1/mapping-overlays/remap-proposals", "read"},
		{http.MethodPost, "/v1/mapping-overlays/remap-proposals/" + id + "/approve", "approve"},
		{http.MethodPost, "/v1/mapping-overlays/remap-proposals/" + id + "/reject", "write"},
		{http.MethodPost, "/v1/mapping-overlays/remap-proposals/" + id + "/keep-legacy", "write"},
	}
	for _, rt := range routes {
		r := httptest.NewRequest(rt.method, rt.path, nil)
		r = r.WithContext(authctx.WithCredential(r.Context(),
			credstore.Credential{ID: "jwt:1804", TenantID: tenant, UserID: "user-1804"}))
		in := authz.BuildInput(r, nil)
		if in.Action != rt.action || in.Resource.Type != "mapping-overlays" {
			t.Fatalf("%s %s derives %s on %s, want %s on mapping-overlays",
				rt.method, rt.path, in.Action, in.Resource.Type, rt.action)
		}
		for _, c := range []struct {
			name  string
			roles []authz.Role
			want  bool
		}{
			{"tenant admin", []authz.Role{authz.RoleAdmin}, true},
			{"grc_engineer", []authz.Role{authz.RoleGRCEngineer}, true},
			{"viewer", []authz.Role{authz.RoleViewer}, false},
			{"auditor", []authz.Role{authz.RoleAuditor}, false},
			{"control_owner", []authz.Role{authz.RoleControlOwner}, false},
			{"no role", []authz.Role{}, false},
		} {
			in.User.Roles = c.roles
			in.User.Attrs = map[string]any{"is_super_admin": false}
			d, err := e.Decide(context.Background(), in)
			if err != nil {
				t.Fatalf("%s %s %s: Decide: %v", c.name, rt.method, rt.path, err)
			}
			if d.Allow != c.want {
				t.Errorf("%s %s %s: allow=%v, want %v (reason=%s)", c.name, rt.method, rt.path, d.Allow, c.want, d.Reason)
			}
		}
	}
}
