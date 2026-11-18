package authz_test

// #2358 — GET /v1/admin/controls/held-back is tenant admin only. BuildInput
// derives read on admin/controls; admin.rego admits a tenant admin and its
// tenant_admin_only_resource carve-out keeps grc_engineer's blanket read
// off it, so OPA admits exactly the callers the handler's
// authz.IsTenantAdmin gate admits.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
)

func TestHeldBack2358_TenantAdminOnly(t *testing.T) {
	t.Parallel()
	e := engine(t)
	const tenant = "00000000-0000-4000-8000-000000002358"
	r := httptest.NewRequest(http.MethodGet, "/v1/admin/controls/held-back", nil)
	r = r.WithContext(authctx.WithCredential(r.Context(),
		credstore.Credential{ID: "jwt:2358", TenantID: tenant, UserID: "user-2358"}))
	in := authz.BuildInput(r, nil)
	if in.Action != "read" || in.Resource.Type != "admin" || in.Resource.ID != "controls" {
		t.Fatalf("held-back derives %s on %s/%s, want read on admin/controls", in.Action, in.Resource.Type, in.Resource.ID)
	}
	for _, c := range []struct {
		name  string
		roles []authz.Role
		want  bool
	}{
		{"admin", []authz.Role{authz.RoleAdmin}, true},
		{"grc_engineer", []authz.Role{authz.RoleGRCEngineer}, false},
		{"control_owner", []authz.Role{authz.RoleControlOwner}, false},
		{"auditor", []authz.Role{authz.RoleAuditor}, false},
		{"viewer", []authz.Role{authz.RoleViewer}, false},
		{"no role", []authz.Role{}, false},
	} {
		in.User.Roles = c.roles
		d, err := e.Decide(context.Background(), in)
		if err != nil {
			t.Fatalf("%s: Decide: %v", c.name, err)
		}
		if d.Allow != c.want {
			t.Errorf("%s: allow=%v, want %v (reason=%s)", c.name, d.Allow, c.want, d.Reason)
		}
	}
}

// TestHeldBack2358_GRCEngineerKeepsOtherAdminReads guards the carve-out: it
// narrows only the admin/controls segment, not grc_engineer's other reads.
func TestHeldBack2358_GRCEngineerKeepsOtherAdminReads(t *testing.T) {
	t.Parallel()
	e := engine(t)
	d, err := e.Decide(context.Background(), authz.Input{
		User:     authz.UserInput{ID: "user-grc", Roles: []authz.Role{authz.RoleGRCEngineer}},
		TenantID: "00000000-0000-4000-8000-000000002358",
		Action:   "read",
		Resource: authz.ResourceInput{Type: "admin", ID: "llm-routing"},
		Request:  authz.RequestInput{Method: http.MethodGet, Path: "/v1/admin/llm-routing"},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if !d.Allow {
		t.Errorf("grc_engineer read of /v1/admin/llm-routing denied (reason=%s); the carve-out must not widen", d.Reason)
	}
}
