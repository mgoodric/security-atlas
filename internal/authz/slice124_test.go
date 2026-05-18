package authz_test

// Slice 124 — OPA matrix tests for the `audit-log-unified` resource type.
//
// The slice ships read-only access to a tenant-scoped UNION ALL across nine
// per-domain audit-log tables; admins and auditors get read access, every
// other role is default-deny. These tests pin that contract at the rego layer
// so a future edit to admin.rego / auditor.rego / control_owner.rego /
// grc_engineer.rego / viewer.rego that accidentally widens or narrows the
// access surface trips a unit-test failure.

import (
	"context"
	"testing"

	"github.com/mgoodric/security-atlas/internal/authz"
)

func TestSlice124_UnifiedAuditLogAccess(t *testing.T) {
	t.Parallel()
	e := engine(t)

	cases := []struct {
		name      string
		roles     []authz.Role
		wantAllow bool
		reason    string
	}{
		{
			name:      "admin-allow-read",
			roles:     []authz.Role{authz.RoleAdmin},
			wantAllow: true,
			reason:    "admin.rego wildcard allows every action on every resource within tenant",
		},
		{
			name:      "auditor-allow-read",
			roles:     []authz.Role{authz.RoleAuditor},
			wantAllow: true,
			reason:    "auditor.rego auditor_readable_resources['audit-log-unified'] match",
		},
		{
			name:      "viewer-deny-read",
			roles:     []authz.Role{authz.RoleViewer},
			wantAllow: false,
			reason:    "viewer.rego does not enumerate audit-log-unified — default-deny",
		},
		{
			name:      "control_owner-deny-read",
			roles:     []authz.Role{authz.RoleControlOwner},
			wantAllow: false,
			reason:    "control_owner.rego is scoped to controls / evidence / attestations — default-deny",
		},
		{
			name:      "grc_engineer-allow-read",
			roles:     []authz.Role{authz.RoleGRCEngineer},
			wantAllow: true,
			reason:    "grc_engineer.rego allows wildcard read across tenant-scoped resources — the v1 primary user (security-leader-of-one) needs audit visibility into their own program",
		},
		{
			name:      "no-roles-deny-read",
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
				TenantID: "00000000-0000-0000-0000-000000000124",
				Action:   "read",
				Resource: authz.ResourceInput{Type: "audit-log-unified"},
				Request:  authz.RequestInput{Method: "GET", Path: "/v1/admin/audit-log/unified"},
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

func TestSlice124_AuditorWriteDenied(t *testing.T) {
	t.Parallel()
	// Read-only contract: no role gets a write surface on the unified
	// aggregator — the aggregator package has no exported writer
	// (slice-124 anti-criterion P0-A8). This test pins that even an
	// auditor (the most-permissive non-admin read role here) cannot
	// hit a write action.
	e := engine(t)
	d, err := e.Decide(context.Background(), authz.Input{
		User: authz.UserInput{
			ID:    "user-write-attempt",
			Roles: []authz.Role{authz.RoleAuditor},
		},
		TenantID: "00000000-0000-0000-0000-000000000124",
		Action:   "write",
		Resource: authz.ResourceInput{Type: "audit-log-unified"},
		Request:  authz.RequestInput{Method: "POST", Path: "/v1/admin/audit-log/unified"},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d.Allow {
		t.Errorf("auditor.write on audit-log-unified should be denied; got allow")
	}
}
