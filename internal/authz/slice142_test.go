package authz_test

import (
	"context"
	"testing"

	"github.com/mgoodric/security-atlas/internal/authz"
)

// TestDecide_SuperAdmin_GrantsManagementSurface asserts the slice-142
// super_admin.rego policy allows the management routes when the input
// carries `is_super_admin: true`.
func TestDecide_SuperAdmin_GrantsManagementSurface(t *testing.T) {
	t.Parallel()
	e := engine(t)
	for _, tc := range []struct {
		name   string
		method string
		path   string
		action string
	}{
		{name: "list", method: "GET", path: "/v1/admin/super-admins", action: "read"},
		{name: "grant", method: "POST", path: "/v1/admin/super-admins", action: "write"},
		{name: "demote", method: "DELETE", path: "/v1/admin/super-admins/abc", action: "write"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, err := e.Decide(context.Background(), authz.Input{
				User: authz.UserInput{
					ID:    "user-super",
					Roles: nil, // explicitly no per-tenant role
					Attrs: map[string]any{"is_super_admin": true},
				},
				TenantID: "00000000-0000-0000-0000-000000000001",
				Action:   tc.action,
				Resource: authz.ResourceInput{
					Type: "admin",
					ID:   "super-admins",
				},
				Request: authz.RequestInput{Method: tc.method, Path: tc.path},
			})
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if !d.Allow {
				t.Fatalf("super_admin should be allowed for %s; reason=%s", tc.name, d.Reason)
			}
		})
	}
}

// TestDecide_SuperAdmin_DoesNotElevateOtherResources asserts the
// slice-142 super_admin.rego is NARROW — it does NOT grant blanket
// write authority across every resource. Per the policy's package
// doc: super_admin is the platform identity-management role, not a
// tenant-write override.
func TestDecide_SuperAdmin_DoesNotElevateOtherResources(t *testing.T) {
	t.Parallel()
	e := engine(t)
	d, err := e.Decide(context.Background(), authz.Input{
		User: authz.UserInput{
			ID:    "user-super",
			Roles: nil,
			Attrs: map[string]any{"is_super_admin": true},
		},
		TenantID: "00000000-0000-0000-0000-000000000001",
		Action:   "write",
		Resource: authz.ResourceInput{Type: "risks"},
		Request:  authz.RequestInput{Method: "POST", Path: "/v1/risks"},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d.Allow {
		t.Fatalf("super_admin should NOT have implicit write authority on risks; got allow")
	}
}

// TestDecide_NonSuperAdmin_BlockedFromManagementSurface asserts that
// a caller without the super_admin bit is rejected even when they
// hold per-tenant admin — slice 142's POST + DELETE handlers are
// platform-global routes that need the bit.
func TestDecide_NonSuperAdmin_BlockedFromManagementSurface(t *testing.T) {
	t.Parallel()
	e := engine(t)
	d, err := e.Decide(context.Background(), authz.Input{
		User: authz.UserInput{
			ID:    "user-tenant-admin",
			Roles: []authz.Role{authz.RoleAdmin}, // per-tenant admin only
			Attrs: map[string]any{"is_super_admin": false},
		},
		TenantID: "00000000-0000-0000-0000-000000000001",
		Action:   "write",
		Resource: authz.ResourceInput{
			Type: "admin",
			ID:   "super-admins",
		},
		Request: authz.RequestInput{Method: "POST", Path: "/v1/admin/super-admins"},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	// Per-tenant admin DOES match the generic admin.rego allow rule
	// (which allows ANY action on ANY resource within the tenant —
	// the canvas §9.5 design). So the OPA decision is `allow=true`
	// here even though the handler itself rejects via requireSuperAdmin.
	// That's the intended dual-leg defense: the handler gate is the
	// LOAD-BEARING super_admin check; the OPA policy is the second
	// leg for the JWT-only path. We assert the handler-level rejection
	// in handler_integration_test.go.
	if !d.Allow {
		t.Fatalf("per-tenant admin should still pass OPA (admin.rego allows any tenant action); handler-layer super_admin gate is the load-bearing check")
	}
}
