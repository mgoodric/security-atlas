package authz_test

// Slice 270 — OPA matrix tests for the `/v1/activity/unified` endpoint.
//
// The new slice-270 route mounts under the existing `"activity"` OPA
// resource type (slice 156 added it for `/v1/activity` — the dashboard
// activity-feed panel). All five tenant-member roles (admin / auditor /
// grc_engineer / viewer / control_owner) admit on `"activity"`:
//
//   - admin           — admin.rego wildcard
//   - grc_engineer    — grc_engineer.rego wildcard read
//   - auditor         — auditor.rego auditor_readable_resources["activity"]
//   - viewer          — viewer.rego viewer_readable_resources["activity"]
//   - control_owner   — control_owner.rego control_owner_readable_resources["activity"]
//
// Slice 270 adds NO new OPA resource-type symbol (D1 in the decisions
// log). This test pins:
//
//   - AC-10: every authenticated tenant member admits the new
//     /v1/activity/unified route (no 403);
//   - The unauthenticated baseline (no-roles) is denied;
//   - The read-only contract (slice 270 P0-A8 by analogy with slice 156):
//     no role gets a write surface on `/v1/activity/unified` (the handler
//     exposes no write methods either — defense in depth).

import (
	"context"
	"testing"

	"github.com/mgoodric/security-atlas/internal/authz"
)

// TestSlice270_ActivityUnifiedOPAAdmit asserts every signed-in role
// reaches the new slice-270 `/v1/activity/unified` endpoint via the
// existing `"activity"` admit.
//
// The slice 270 row-visibility predicate runs at the SQL layer AFTER
// the OPA admit; this test only pins the admit. Cross-actor and
// cross-tenant row-visibility correctness is verified by the
// integration tests in
// `internal/api/adminauditlog/activity_integration_test.go` (slice 270
// AC-6 + AC-7).
func TestSlice270_ActivityUnifiedOPAAdmit(t *testing.T) {
	t.Parallel()
	e := engine(t)

	cases := []struct {
		name      string
		roles     []authz.Role
		wantAllow bool
		reason    string
	}{
		{
			name:      "admin",
			roles:     []authz.Role{authz.RoleAdmin},
			wantAllow: true,
			reason:    "admin.rego wildcard allows every action on every resource within tenant",
		},
		{
			name:      "auditor",
			roles:     []authz.Role{authz.RoleAuditor},
			wantAllow: true,
			reason:    "slice 156: auditor.rego auditor_readable_resources['activity'] (route uses same resource type)",
		},
		{
			name:      "viewer",
			roles:     []authz.Role{authz.RoleViewer},
			wantAllow: true,
			reason:    "slice 156: viewer.rego viewer_readable_resources['activity'] (slice 270 P0-A1: non-admin admit is the whole point)",
		},
		{
			name:      "control_owner",
			roles:     []authz.Role{authz.RoleControlOwner},
			wantAllow: true,
			reason:    "slice 156: control_owner.rego control_owner_readable_resources['activity']",
		},
		{
			name:      "grc_engineer",
			roles:     []authz.Role{authz.RoleGRCEngineer},
			wantAllow: true,
			reason:    "grc_engineer.rego wildcard read across tenant-scoped resources",
		},
		{
			name:      "no-roles",
			roles:     nil,
			wantAllow: false,
			reason:    "default-deny baseline when no role rule fires",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d, err := e.Decide(context.Background(), authz.Input{
				User: authz.UserInput{
					ID:    "user-" + tc.name,
					Roles: tc.roles,
				},
				TenantID: "00000000-0000-0000-0000-000000000270",
				Action:   "read",
				Resource: authz.ResourceInput{
					Type: "activity",
					ID:   "unified",
				},
				Request: authz.RequestInput{Method: "GET", Path: "/v1/activity/unified"},
			})
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if d.Allow != tc.wantAllow {
				t.Errorf("allow = %v; want %v; (%s); reason: %s", d.Allow, tc.wantAllow, tc.reason, d.Reason)
			}
		})
	}
}

// TestSlice270_ActivityUnifiedWriteDenied pins the slice-270 read-only
// contract. The handler exposes no write surface on /v1/activity/unified
// (no POST/PUT/PATCH/DELETE methods registered); this test pins the
// OPA layer as the second leg of the gate — no role except admin (whose
// wildcard intentionally admits) is admitted on a hypothetical POST.
//
// The `"activity"` resource type's read admits all live behind
// `is_read` predicates in viewer.rego / control_owner.rego /
// auditor.rego, so a POST falls through to default-deny for the
// non-admin roles. grc_engineer is also denied because `"activity"` is
// NOT in `grc_writable_resources`. This pins that contract — a future
// maintainer adding `"activity"` to a writable set would surface here.
func TestSlice270_ActivityUnifiedWriteDenied(t *testing.T) {
	t.Parallel()
	e := engine(t)

	denied := []authz.Role{
		authz.RoleGRCEngineer,
		authz.RoleAuditor,
		authz.RoleViewer,
		authz.RoleControlOwner,
	}

	for _, role := range denied {
		role := role
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()
			d, err := e.Decide(context.Background(), authz.Input{
				User: authz.UserInput{
					ID:    "user-write-attempt",
					Roles: []authz.Role{role},
				},
				TenantID: "00000000-0000-0000-0000-000000000270",
				Action:   "write",
				Resource: authz.ResourceInput{Type: "activity", ID: "unified"},
				Request:  authz.RequestInput{Method: "POST", Path: "/v1/activity/unified"},
			})
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if d.Allow {
				t.Errorf("role %q should be denied on POST /v1/activity/unified; got allow; reason: %s", role, d.Reason)
			}
		})
	}
}
