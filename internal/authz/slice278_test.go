package authz_test

// Slice 278 — OPA matrix test for the demo-seed UI button.
//
// AC-16: pins the admit set for POST /v1/admin/demo/seed and
// POST /v1/admin/demo/teardown. Admin admits; every other canonical
// role denies. The env-var gate runs in the handler (an admit-set
// test cannot exercise it because OPA has no access to process env).
//
// Constitutional anchor: P0-278-7 — admin is the ONLY authz role.
// Super_admin alone does NOT admit (super_admin.rego scopes its
// resource set to {super-admins, tenants}; admin_demo is not in
// that set). Tenant admins admit through admin.rego's blanket
// allow.
//
// Resource shape: BuildInput resolves /v1/admin/demo/{seed,teardown}
// to resource.type="admin", resource.id="demo". The admin.rego
// rule (`allow if has_role("admin")`) admits regardless of resource
// id; this test confirms the role gate is the sole admit lever.

import (
	"context"
	"testing"

	"github.com/mgoodric/security-atlas/internal/authz"
)

// slice278DemoPaths enumerates the two MUTATING demo routes that
// need a strict role admit matrix. Status is intentionally excluded
// — the wider platform admits non-admin reads broadly (grc_engineer
// has a wildcard read allow), and {enabled: boolean} is benign.
// The MUTATING actions are where admin-only matters.
var slice278DemoPaths = []struct {
	name   string
	action string
	method string
	path   string
}{
	{"seed", "write", "POST", "/v1/admin/demo/seed"},
	{"teardown", "write", "POST", "/v1/admin/demo/teardown"},
}

// slice278RolePairs is the role × want-allow matrix. Admin is the
// only canonical role that admits. Auditor / grc_engineer /
// control_owner / viewer / (no role) all deny.
var slice278RolePairs = []struct {
	name  string
	roles []authz.Role
	want  bool
}{
	{"admin", []authz.Role{authz.RoleAdmin}, true},
	{"auditor", []authz.Role{authz.RoleAuditor}, false},
	{"grc_engineer", []authz.Role{authz.RoleGRCEngineer}, false},
	{"control_owner", []authz.Role{authz.RoleControlOwner}, false},
	{"viewer", []authz.Role{authz.RoleViewer}, false},
	{"no_role", []authz.Role{}, false},
}

// TestSlice278_DemoSeedAdmitMatrix pins the per-role admit matrix
// for the slice-278 demo routes. Admin admits all three; every
// other role denies all three.
func TestSlice278_DemoSeedAdmitMatrix(t *testing.T) {
	t.Parallel()
	e := engine(t)
	for _, path := range slice278DemoPaths {
		path := path
		for _, role := range slice278RolePairs {
			role := role
			t.Run(path.name+"_"+role.name, func(t *testing.T) {
				t.Parallel()
				d, err := e.Decide(context.Background(), authz.Input{
					User: authz.UserInput{
						ID:    "user-" + role.name + "-" + path.name,
						Roles: role.roles,
					},
					TenantID: "00000000-0000-0000-0000-000000000278",
					Action:   path.action,
					Resource: authz.ResourceInput{Type: "admin", ID: "demo"},
					Request: authz.RequestInput{
						Method: path.method,
						Path:   path.path,
					},
				})
				if err != nil {
					t.Fatalf("Decide: %v", err)
				}
				if d.Allow != role.want {
					t.Errorf("role=%s path=%s allow=%v; want %v; reason=%s",
						role.name, path.path, d.Allow, role.want, d.Reason)
				}
			})
		}
	}
}

// TestSlice278_SuperAdminAloneDenies — defense in depth for
// P0-278-7. A super_admin without an admin role grant does NOT
// admit the demo-seed endpoints. The super_admin.rego rule scopes
// its resource set to {super-admins, tenants}; admin_demo is not
// in that set. This test pins the no-bypass posture.
func TestSlice278_SuperAdminAloneDenies(t *testing.T) {
	t.Parallel()
	e := engine(t)
	for _, path := range slice278DemoPaths {
		path := path
		t.Run(path.name, func(t *testing.T) {
			t.Parallel()
			d, err := e.Decide(context.Background(), authz.Input{
				User: authz.UserInput{
					ID:    "super-admin-no-role-" + path.name,
					Roles: []authz.Role{},
					Attrs: map[string]interface{}{
						"is_super_admin": true,
					},
				},
				TenantID: "00000000-0000-0000-0000-000000000278",
				Action:   path.action,
				Resource: authz.ResourceInput{Type: "admin", ID: "demo"},
				Request: authz.RequestInput{
					Method: path.method,
					Path:   path.path,
				},
			})
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if d.Allow {
				t.Errorf("super_admin-alone admitted %s; want deny (P0-278-7)", path.path)
			}
		})
	}
}
