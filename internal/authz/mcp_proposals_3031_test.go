package authz_test

// #3031 (decided on #3022): grc_engineer files MCP write proposals and
// decides none of them. grc_engineer.rego admits POST
// /v1/mcp/write-proposals only; confirm and reject derive the same "write"
// action on "mcp" and stay admin's. viewer is refused all three, and admin
// is unchanged.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
)

func TestMCPProposals3031_GRCEngineerFilesOnly(t *testing.T) {
	t.Parallel()
	e := engine(t)
	const tenant = "00000000-0000-4000-8000-000000003031"
	const id = "6f1c2a52-3b0e-4b8e-9a51-1d6a3f0c3031"
	const (
		create  = "/v1/mcp/write-proposals"
		confirm = "/v1/mcp/write-proposals/" + id + "/confirm"
		reject  = "/v1/mcp/write-proposals/" + id + "/reject"
	)
	cases := []struct {
		role authz.Role
		path string
		want bool
	}{
		{authz.RoleGRCEngineer, create, true},
		{authz.RoleGRCEngineer, confirm, false},
		{authz.RoleGRCEngineer, reject, false},
		{authz.RoleViewer, create, false},
		{authz.RoleViewer, confirm, false},
		{authz.RoleViewer, reject, false},
		{authz.RoleAdmin, create, true},
		{authz.RoleAdmin, confirm, true},
		{authz.RoleAdmin, reject, true},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodPost, c.path, nil)
		r = r.WithContext(authctx.WithCredential(r.Context(),
			credstore.Credential{ID: "jwt:3031", TenantID: tenant, UserID: "user-3031"}))
		in := authz.BuildInput(r, nil)
		if in.Resource.Type != "mcp" || in.Action != "write" {
			t.Fatalf("POST %s derives (%q, %q), want (mcp, write)", c.path, in.Resource.Type, in.Action)
		}
		in.User.Roles = []authz.Role{c.role}
		in.User.Attrs = map[string]any{"is_super_admin": false}
		d, err := e.Decide(context.Background(), in)
		if err != nil {
			t.Fatalf("%s POST %s: Decide: %v", c.role, c.path, err)
		}
		if d.Allow != c.want {
			t.Errorf("%s POST %s: allow=%v, want %v (reason=%s)", c.role, c.path, d.Allow, c.want, d.Reason)
		}
	}
}
