package authz_test

// Slice 269 — OPA matrix tests for the dashboard snapshot export
// endpoint.
//
// Slice 269 ships `GET /v1/dashboard/export?format=json|csv|xlsx`,
// registered in `internal/api/httpserver.go` (the dashboardexport
// mount block). The `BuildInput` path-to-resource resolver derives
// `resource.type = "dashboard"` from the leading `/v1/dashboard/`
// segment (see `internal/authz/input.go::resourceFromPath`); the
// admit set is therefore keyed on `"dashboard"`.
//
// Admit set (slice 269 D3 — narrower than the slice 066 in-app
// dashboard reads):
//
//   admin         → admit (admin.rego wildcard)
//   grc_engineer  → admit (grc_engineer.rego wildcard read; covers
//                          the IsApprover credential bridge)
//   auditor       → admit (auditor.rego auditor_readable_resources
//                          entry for "dashboard")
//   control_owner → DENY  (bulk-handoff variant intentionally
//                          narrower than the in-app reads slice 156
//                          granted; control-owner credentials
//                          should NOT bulk-export the whole
//                          dashboard)
//   viewer        → DENY  (same rationale as control_owner)
//   no-roles      → DENY  (default-deny baseline)
//
// The handler-level `hasDashboardExportAccess` predicate in
// `internal/api/dashboardexport/` is the defense-in-depth twin
// (admin + approver only — IsApprover maps to grc_engineer at the
// derivedRolesFor boundary, so the handler gate aligns with the
// OPA admit minus the auditor branch). The auditor branch goes
// through OPA only; a test-server with no OPA engine wired (the
// unit-test path) would 401/403 a bare-auditor credential at the
// handler gate, but production paths admit it via this rego rule.
//
// This test pins the admit set so a future maintainer who narrows
// or widens it trips a unit-test failure here.

import (
	"context"
	"testing"

	"github.com/mgoodric/security-atlas/internal/authz"
)

func TestSlice269_DashboardExportAdmitMatrix(t *testing.T) {
	t.Parallel()
	e := engine(t)

	roleCases := []struct {
		name   string
		roles  []authz.Role
		reason string
	}{
		{
			name:   "admin",
			roles:  []authz.Role{authz.RoleAdmin},
			reason: "admin.rego wildcard allows every action on every resource within tenant",
		},
		{
			name:   "grc_engineer",
			roles:  []authz.Role{authz.RoleGRCEngineer},
			reason: "grc_engineer.rego wildcard read across tenant-scoped resources — covers dashboard export (IsApprover credential bridge)",
		},
		{
			name:   "auditor",
			roles:  []authz.Role{authz.RoleAuditor},
			reason: "slice 269: auditor.rego auditor_readable_resources entry for 'dashboard' (parity with slice 156 activity/upcoming admits)",
		},
		{
			name:   "control_owner",
			roles:  []authz.Role{authz.RoleControlOwner},
			reason: "slice 269 D3: bulk-export admit narrower than in-app reads — control_owner intentionally denied",
		},
		{
			name:   "viewer",
			roles:  []authz.Role{authz.RoleViewer},
			reason: "slice 269 D3: viewer intentionally denied bulk-export",
		},
		{
			name:   "no-roles",
			roles:  nil,
			reason: "default-deny baseline",
		},
	}

	// wantAllow is positional, parallel to roleCases above:
	// [admin, grc_engineer, auditor, control_owner, viewer, no-roles].
	wantAllow := [6]bool{true, true, true, false, false, false}

	for i, rc := range roleCases {
		rc := rc
		want := wantAllow[i]
		t.Run("dashboard-export/"+rc.name, func(t *testing.T) {
			t.Parallel()
			d, err := e.Decide(context.Background(), authz.Input{
				User: authz.UserInput{
					ID:    "user-dashexp-" + rc.name,
					Roles: rc.roles,
				},
				TenantID: "00000000-0000-0000-0000-000000000269",
				Action:   "read",
				Resource: authz.ResourceInput{
					Type: "dashboard",
					ID:   "export",
				},
				Request: authz.RequestInput{Method: "GET", Path: "/v1/dashboard/export"},
			})
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if d.Allow != want {
				t.Errorf("allow = %v; want %v; (%s); reason: %s",
					d.Allow, want, rc.reason, d.Reason)
			}
		})
	}
}
