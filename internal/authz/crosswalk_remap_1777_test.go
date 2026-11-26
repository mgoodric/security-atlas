package authz_test

// #1777 — crosswalk-edge remap proposals change global catalog rows, so OPA
// admits them for a super_admin only. A tenant admin, who admin.rego admits
// everywhere else, is refused here; so is every other role. The handler
// checks the claim again (internal/api/admincrosswalkremap).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
)

func TestCrosswalkRemap1777_SuperAdminOnly(t *testing.T) {
	t.Parallel()
	e := engine(t)
	const tenant = "00000000-0000-4000-8000-000000001777"
	const id = "6f1c2a52-3b0e-4b8e-9a51-1d6a3f0c1777"
	routes := []struct{ method, path, action string }{
		{http.MethodGet, "/v1/admin/crosswalk-edge-remap-proposals", "read"},
		{http.MethodPost, "/v1/admin/crosswalk-edge-remap-proposals/approve-unchanged", "write"},
		{http.MethodPost, "/v1/admin/crosswalk-edge-remap-proposals/" + id + "/approve", "approve"},
		{http.MethodPost, "/v1/admin/crosswalk-edge-remap-proposals/" + id + "/reject", "write"},
		{http.MethodPost, "/v1/admin/crosswalk-edge-remap-proposals/" + id + "/keep-legacy", "write"},
	}
	for _, rt := range routes {
		r := httptest.NewRequest(rt.method, rt.path, nil)
		r = r.WithContext(authctx.WithCredential(r.Context(),
			credstore.Credential{ID: "jwt:1777", TenantID: tenant, UserID: "user-1777"}))
		in := authz.BuildInput(r, nil)
		if in.Action != rt.action || in.Resource.Type != "admin" || in.Resource.ID != "crosswalk-edge-remap-proposals" {
			t.Fatalf("%s %s derives %s on %s/%s, want %s on admin/crosswalk-edge-remap-proposals",
				rt.method, rt.path, in.Action, in.Resource.Type, in.Resource.ID, rt.action)
		}
		for _, c := range []struct {
			name  string
			roles []authz.Role
			super bool
			want  bool
		}{
			{"super_admin without a tenant role", nil, true, true},
			{"super_admin who is also tenant admin", []authz.Role{authz.RoleAdmin}, true, true},
			{"tenant admin", []authz.Role{authz.RoleAdmin}, false, false},
			{"grc_engineer", []authz.Role{authz.RoleGRCEngineer}, false, false},
			{"auditor", []authz.Role{authz.RoleAuditor}, false, false},
			{"viewer", []authz.Role{authz.RoleViewer}, false, false},
			{"no role", []authz.Role{}, false, false},
		} {
			in.User.Roles = c.roles
			in.User.Attrs = map[string]any{"is_super_admin": c.super}
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

// TestCrosswalkRemap1777_TenantAdminKeepsOtherAdminRoutes guards the
// admin.rego carve-out: it narrows only the super_admin-only segment.
func TestCrosswalkRemap1777_TenantAdminKeepsOtherAdminRoutes(t *testing.T) {
	t.Parallel()
	e := engine(t)
	d, err := e.Decide(context.Background(), authz.Input{
		User:     authz.UserInput{ID: "user-admin", Roles: []authz.Role{authz.RoleAdmin}, Attrs: map[string]any{"is_super_admin": false}},
		TenantID: "00000000-0000-4000-8000-000000001777",
		Action:   "write",
		Resource: authz.ResourceInput{Type: "admin", ID: "users"},
		Request:  authz.RequestInput{Method: http.MethodPost, Path: "/v1/admin/users/assign"},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if !d.Allow {
		t.Fatalf("tenant admin lost POST /v1/admin/users/assign: reason=%s", d.Reason)
	}
}
