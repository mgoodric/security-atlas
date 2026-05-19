package authz_test

// Slice 156 — OPA matrix tests for the slice-066 dashboard read endpoints.
//
// Slice 066 shipped three dashboard read endpoints registered at
// internal/api/httpserver.go:603-605:
//
//   GET /v1/frameworks/posture  → resource.type = "frameworks"
//   GET /v1/activity            → resource.type = "activity"
//   GET /v1/upcoming            → resource.type = "upcoming"
//
// (See internal/authz/input.go:resourceFromPath — the second URL segment
// is the resource type; the third segment is the resource id.)
//
// Slice 066 AC-9 / AC-10 / AC-11 implied "every signed-in user sees the
// dashboard" but never updated the per-role OPA policies. Slice 148's
// audit-log decision D8 surfaced the gap during the calendar OPA fix.
//
// Two of the three resource types map cleanly:
//
//   - "frameworks" is already in defaults.rego.catalog_resources, so
//     every authenticated role admits GET /v1/frameworks/posture today.
//     The slice-156 admit on "frameworks" is a no-op at the rego layer
//     but the test pins the contract so a future maintainer who narrows
//     catalog_resources doesn't silently break the dashboard's posture
//     panel.
//   - "activity" and "upcoming" are NOT in catalog_resources and are
//     NOT enumerated in any per-role readable set. Without this slice's
//     admit, viewer / control_owner / auditor hit /v1/activity or
//     /v1/upcoming and OPA returns allow=false. The React Query on the
//     dashboard page enters the isError branch and surfaces "Failed to
//     load."
//
// This test pins the slice-066-intended admit set at the rego layer so
// a future maintainer accidentally removing one of the new admits trips
// a unit-test failure.

import (
	"context"
	"testing"

	"github.com/mgoodric/security-atlas/internal/authz"
)

// TestSlice156_DashboardReadAdmitMatrix asserts every signed-in role
// admits the three slice-066 dashboard endpoints. Slice 156 AC-2 + AC-3.
//
// The matrix is endpoint-major (3) × role-major (6 including no-roles
// baseline) = 18 cases. The per-endpoint wantAllow vector encodes the
// design: "activity" and "upcoming" are tenant-scoped reads enumerated
// per-role (slice-148 pattern), so the no-roles case correctly denies.
// "frameworks" is enumerated in defaults.rego.catalog_resources as a
// tenant-agnostic catalog read (slice 035), so it admits for every
// authenticated request, including the no-roles baseline — that match
// is the existing contract slice 066 inherited and slice 156 does NOT
// narrow it.
//
// The "frameworks" cases ALSO double as regression coverage for
// defaults.rego.catalog_resources — if a future maintainer removes
// "frameworks" from catalog_resources without adding it to each role's
// enumerated set, the viewer / control_owner / auditor cases trip and
// surface the silent dashboard regression at the unit-test layer.
func TestSlice156_DashboardReadAdmitMatrix(t *testing.T) {
	t.Parallel()
	e := engine(t)

	// roleCases is the role half of the matrix, role-order-stable so
	// the per-endpoint allow vector is readable inline.
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
			reason: "grc_engineer.rego wildcard read across tenant-scoped resources — covers dashboard reads without enumeration",
		},
		{
			name:   "auditor",
			roles:  []authz.Role{authz.RoleAuditor},
			reason: "slice 156: auditor.rego auditor_readable_resources entry for activity / upcoming (frameworks via defaults.catalog_resources)",
		},
		{
			name:   "viewer",
			roles:  []authz.Role{authz.RoleViewer},
			reason: "slice 156: viewer.rego viewer_readable_resources entry for activity / upcoming (frameworks via defaults.catalog_resources)",
		},
		{
			name:   "control_owner",
			roles:  []authz.Role{authz.RoleControlOwner},
			reason: "slice 156: control_owner.rego control_owner_readable_resources entry for activity / upcoming (frameworks via defaults.catalog_resources)",
		},
		{
			name:   "no-roles",
			roles:  nil,
			reason: "default-deny baseline when no role rule fires (except catalog_resources which admits any authenticated request)",
		},
	}

	endpoints := []struct {
		name         string
		resourceType string
		resourceID   string
		path         string
		// wantAllow is positional, parallel to roleCases above:
		// [admin, grc_engineer, auditor, viewer, control_owner, no-roles].
		wantAllow [6]bool
	}{
		{
			name:         "frameworks-posture",
			resourceType: "frameworks",
			resourceID:   "posture",
			path:         "/v1/frameworks/posture",
			// no-roles admits because "frameworks" is in
			// defaults.rego.catalog_resources (slice 035).
			wantAllow: [6]bool{true, true, true, true, true, true},
		},
		{
			name:         "activity",
			resourceType: "activity",
			resourceID:   "",
			path:         "/v1/activity",
			wantAllow:    [6]bool{true, true, true, true, true, false},
		},
		{
			name:         "upcoming",
			resourceType: "upcoming",
			resourceID:   "",
			path:         "/v1/upcoming",
			wantAllow:    [6]bool{true, true, true, true, true, false},
		},
	}

	for _, ep := range endpoints {
		ep := ep
		for i, rc := range roleCases {
			rc := rc
			want := ep.wantAllow[i]
			t.Run(ep.name+"/"+rc.name, func(t *testing.T) {
				t.Parallel()
				d, err := e.Decide(context.Background(), authz.Input{
					User: authz.UserInput{
						ID:    "user-" + ep.name + "-" + rc.name,
						Roles: rc.roles,
					},
					TenantID: "00000000-0000-0000-0000-000000000156",
					Action:   "read",
					Resource: authz.ResourceInput{
						Type: ep.resourceType,
						ID:   ep.resourceID,
					},
					Request: authz.RequestInput{Method: "GET", Path: ep.path},
				})
				if err != nil {
					t.Fatalf("Decide: %v", err)
				}
				if d.Allow != want {
					t.Errorf("allow = %v; want %v; (%s); reason: %s", d.Allow, want, rc.reason, d.Reason)
				}
			})
		}
	}
}

// TestSlice156_DashboardWriteDenied pins the slice-066 read-only
// contract at the rego layer: the dashboard handler exposes no write
// methods on activity / upcoming, and the FrameworkPosture handler is
// likewise read-only over existing tables (constitutional invariant #2
// — slice 066 P0-A3). No role except admin (whose wildcard intentionally
// admits) should be admitted on a hypothetical POST /v1/activity or
// POST /v1/upcoming write.
//
// The new slice-156 admits live on the per-role *_readable_resources
// sets — those rules predicate on is_read, so a POST falls through to
// default-deny for viewer / control_owner / auditor. grc_engineer is
// also denied because activity / upcoming are NOT in
// grc_writable_resources. This test pins that contract.
func TestSlice156_DashboardWriteDenied(t *testing.T) {
	t.Parallel()
	e := engine(t)

	denied := []authz.Role{
		authz.RoleGRCEngineer,
		authz.RoleAuditor,
		authz.RoleViewer,
		authz.RoleControlOwner,
	}

	writeTargets := []struct {
		resourceType string
		path         string
	}{
		{resourceType: "activity", path: "/v1/activity"},
		{resourceType: "upcoming", path: "/v1/upcoming"},
	}

	for _, target := range writeTargets {
		target := target
		for _, role := range denied {
			role := role
			t.Run(target.resourceType+"/"+string(role), func(t *testing.T) {
				t.Parallel()
				d, err := e.Decide(context.Background(), authz.Input{
					User: authz.UserInput{
						ID:    "user-write-attempt",
						Roles: []authz.Role{role},
					},
					TenantID: "00000000-0000-0000-0000-000000000156",
					Action:   "write",
					Resource: authz.ResourceInput{Type: target.resourceType},
					Request:  authz.RequestInput{Method: "POST", Path: target.path},
				})
				if err != nil {
					t.Fatalf("Decide: %v", err)
				}
				if d.Allow {
					t.Errorf("role %q should be denied on POST %s; got allow; reason: %s", role, target.path, d.Reason)
				}
			})
		}
	}
}
