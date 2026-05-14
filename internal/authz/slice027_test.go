package authz_test

import (
	"context"
	"testing"

	"github.com/mgoodric/security-atlas/internal/authz"
)

// Slice 027 — walkthrough authz rules. Validates the rego changes in
//
//	policies/authz/auditor.rego          (mirrored to internal/authz/rego_bundle/)
//	policies/authz/control_owner.rego
//	policies/authz/grc_engineer.rego
//
// AC-4 spans these tests: "auditor and the control's owner can read"
// the walkthrough; the auditor's testing notes are private (those tests
// live in slice025_test.go since they ride on audit_notes with
// scope_type='walkthrough' added by slice 029).

const slice027Tenant = "00000000-0000-0000-0000-000000000027"

// AC-4: auditor can read walkthroughs.
func TestSlice027_AuditorReadWalkthroughsAllowed(t *testing.T) {
	t.Parallel()
	d := decide(t, authz.Input{
		User: authz.UserInput{
			ID:    "user-auditor",
			Roles: []authz.Role{authz.RoleAuditor},
			Attrs: map[string]interface{}{},
		},
		TenantID: slice027Tenant,
		Action:   "read",
		Resource: authz.ResourceInput{Type: "walkthroughs"},
		Request:  authz.RequestInput{Method: "GET", Path: "/v1/walkthroughs"},
	})
	if !d.Allow {
		t.Fatalf("expected allow on auditor read walkthroughs, got deny: %s", d.Reason)
	}
}

// P0-1 / AC-3: auditor cannot WRITE walkthroughs at the role level.
// Walkthroughs are authored by control_owner or grc_engineer; the
// auditor's read access is enough.
func TestSlice027_AuditorWriteWalkthroughDenied(t *testing.T) {
	t.Parallel()
	d := decide(t, authz.Input{
		User: authz.UserInput{
			ID:    "user-auditor",
			Roles: []authz.Role{authz.RoleAuditor},
			Attrs: map[string]interface{}{},
		},
		TenantID: slice027Tenant,
		Action:   "write",
		Resource: authz.ResourceInput{Type: "walkthroughs"},
		Request:  authz.RequestInput{Method: "POST", Path: "/v1/walkthroughs"},
	})
	if d.Allow {
		t.Fatalf("expected deny on auditor write walkthroughs, got allow")
	}
}

// AC-4: control owner can read walkthroughs for their controls.
func TestSlice027_ControlOwnerReadWalkthroughsAllowed(t *testing.T) {
	t.Parallel()
	d := decide(t, authz.Input{
		User: authz.UserInput{
			ID:    "user-co",
			Roles: []authz.Role{authz.RoleControlOwner},
		},
		TenantID: slice027Tenant,
		Action:   "read",
		Resource: authz.ResourceInput{Type: "walkthroughs"},
		Request:  authz.RequestInput{Method: "GET", Path: "/v1/walkthroughs"},
	})
	if !d.Allow {
		t.Fatalf("expected allow on control_owner read walkthroughs, got deny: %s", d.Reason)
	}
}

// AC-1: control owner can write walkthroughs (they author them).
func TestSlice027_ControlOwnerWriteWalkthroughsAllowed(t *testing.T) {
	t.Parallel()
	d := decide(t, authz.Input{
		User: authz.UserInput{
			ID:    "user-co",
			Roles: []authz.Role{authz.RoleControlOwner},
		},
		TenantID: slice027Tenant,
		Action:   "write",
		Resource: authz.ResourceInput{Type: "walkthroughs"},
		Request:  authz.RequestInput{Method: "POST", Path: "/v1/walkthroughs"},
	})
	if !d.Allow {
		t.Fatalf("expected allow on control_owner write walkthroughs, got deny: %s", d.Reason)
	}
}

// AC-1: grc_engineer can write walkthroughs across the program.
func TestSlice027_GRCEngineerWriteWalkthroughsAllowed(t *testing.T) {
	t.Parallel()
	d := decide(t, authz.Input{
		User: authz.UserInput{
			ID:    "user-grc",
			Roles: []authz.Role{authz.RoleGRCEngineer},
		},
		TenantID: slice027Tenant,
		Action:   "write",
		Resource: authz.ResourceInput{Type: "walkthroughs"},
		Request:  authz.RequestInput{Method: "POST", Path: "/v1/walkthroughs"},
	})
	if !d.Allow {
		t.Fatalf("expected allow on grc_engineer write walkthroughs, got deny: %s", d.Reason)
	}
}

// Viewer cannot touch walkthroughs at all.
func TestSlice027_ViewerCannotWriteWalkthroughs(t *testing.T) {
	t.Parallel()
	d := decide(t, authz.Input{
		User: authz.UserInput{
			ID:    "user-viewer",
			Roles: []authz.Role{authz.RoleViewer},
		},
		TenantID: slice027Tenant,
		Action:   "write",
		Resource: authz.ResourceInput{Type: "walkthroughs"},
		Request:  authz.RequestInput{Method: "POST", Path: "/v1/walkthroughs"},
	})
	if d.Allow {
		t.Fatalf("expected deny on viewer write walkthroughs, got allow")
	}
}

// Acknowledge an unused context import so the file doesn't drop the
// dependency when go test compiles only this slice's tests in isolation.
var _ = context.Background
