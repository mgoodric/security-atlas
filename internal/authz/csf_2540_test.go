package authz_test

// #2540 — OPA admits the /v1/csf/* routes: every role with a live tenant
// role reads the Tier, the gap view and a profile; grc_engineer and admin
// write. Whether the caller holds grc_engineer is checked again by the
// handler (authz.HasOwnerRole, #2530); this pins the role layer, and that
// the csf rules open nothing else.

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/openapi"
	"github.com/mgoodric/security-atlas/internal/authz"
)

const csf2540Req = "00000000-0000-4000-8000-000000002540"

// decideCSF2540 builds the input the middleware builds (authz.BuildInput
// derives action and resource from the request) with the given roles.
func decideCSF2540(t *testing.T, e *authz.Engine, roles []authz.Role, method, path string) bool {
	t.Helper()
	in := authz.BuildInput(httptest.NewRequest(method, path, nil), nil)
	in.User.ID = "user-2540"
	in.User.Roles = roles
	in.TenantID = "00000000-0000-0000-0000-000000002540"
	d, err := e.Decide(context.Background(), in)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	return d.Allow
}

// csfConcretePath fills a route template's parameters.
func csfConcretePath(tmpl string) string {
	return strings.NewReplacer("{kind}", "target", "{requirement_id}", csf2540Req).Replace(tmpl)
}

func TestDecide2540_CSFRoleMatrix(t *testing.T) {
	t.Parallel()
	e := engine(t)
	readers := map[authz.Role]bool{
		authz.RoleAdmin: true, authz.RoleGRCEngineer: true, authz.RoleControlOwner: true,
		authz.RoleAuditor: true, authz.RoleViewer: true,
	}
	writers := map[authz.Role]bool{authz.RoleAdmin: true, authz.RoleGRCEngineer: true}

	var n int
	for _, r := range openapi.RouteSpecs {
		if !strings.HasPrefix(r.Path, "/v1/csf/") {
			continue
		}
		n++
		path := csfConcretePath(r.Path)
		want := writers
		if r.Method == "GET" {
			want = readers
		}
		for _, role := range []authz.Role{
			authz.RoleAdmin, authz.RoleGRCEngineer, authz.RoleControlOwner,
			authz.RoleAuditor, authz.RoleViewer,
		} {
			if got := decideCSF2540(t, e, []authz.Role{role}, r.Method, path); got != want[role] {
				t.Errorf("%s %s as %s: allow = %v; want %v", r.Method, path, role, got, want[role])
			}
		}
		if decideCSF2540(t, e, nil, r.Method, path) {
			t.Errorf("%s %s with no role: allowed; want default-deny", r.Method, path)
		}
	}
	if n != 7 {
		t.Fatalf("found %d /v1/csf/ routes in openapi.RouteSpecs; want 7 (update this matrix with the route)", n)
	}
}

// The csf rules admit only the seven routes. Every other method, sub-path or
// profile kind is denied to each role they serve; admin is left out, since
// admin.rego admits it everywhere in its tenant.
func TestDecide2540_CSFRulesAreNarrow(t *testing.T) {
	t.Parallel()
	e := engine(t)
	sel := "/v1/csf/profiles/target/selections"
	for _, c := range []struct{ method, path string }{
		{"GET", "/v1/csf"},
		{"GET", "/v1/csf/"},
		{"GET", "/v1/csf/tier/x"},
		{"GET", "/v1/csf/gap/x"},
		{"GET", "/v1/csf/profiles"},
		{"GET", "/v1/csf/profiles/baseline"},
		{"GET", sel},
		{"GET", sel + "/" + csf2540Req},
		{"GET", "/v1/csfx/tier"},
		{"POST", "/v1/csf/tier"},
		{"DELETE", "/v1/csf/tier"},
		{"PATCH", "/v1/csf/tier"},
		{"PUT", "/v1/csf/gap"},
		{"PUT", "/v1/csf/profiles/baseline"},
		{"DELETE", "/v1/csf/profiles/target"},
		{"POST", sel},
		{"DELETE", sel},
		{"DELETE", sel + "/"},
		{"PUT", sel + "/" + csf2540Req},
		{"DELETE", sel + "/" + csf2540Req + "/x"},
		{"DELETE", "/v1/csf/profiles/baseline/selections/" + csf2540Req},
		{"POST", "/v1/csf/tier:approve"},
	} {
		for _, role := range []authz.Role{
			authz.RoleGRCEngineer, authz.RoleControlOwner, authz.RoleAuditor, authz.RoleViewer,
		} {
			if role == authz.RoleGRCEngineer && c.method == "GET" {
				continue // grc_engineer reads every tenant resource (grc_engineer.rego)
			}
			if decideCSF2540(t, e, []authz.Role{role}, c.method, c.path) {
				t.Errorf("%s %s as %s: allowed; want deny", c.method, c.path, role)
			}
		}
	}
}
