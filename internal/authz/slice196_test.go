package authz_test

import (
	"context"
	"testing"

	"github.com/mgoodric/security-atlas/internal/authz"
)

// Slice 196 — bootstrap OAuth migration. The atlas-bootstrap one-shot
// container uses an OAuth client_credentials JWT (slice 188) to drive
// `atlas-cli controls upload`. The OAuth-issued JWT has no per-tenant
// role binding (Roles = {}, SuperAdmin = false per slice 188's
// handleClientCredentials), so a plain authz path would land at
// default-deny. Slice 196 extends the slice-035 system.rego machine-
// actor carve-outs to cover `upload-bundle` on `controls`, symmetric
// with the existing evidence-push carve-out.
//
// These tests pin both the positive case (machine actor allowed) and
// two negative regression guards (human actors NOT shortcut, non-
// controls resources NOT shortcut).

// TestSlice196_MachineActorUploadBundleAllowed is the positive case:
// a machine actor with no roles can upload-bundle to controls.
func TestSlice196_MachineActorUploadBundleAllowed(t *testing.T) {
	t.Parallel()
	e := engine(t)
	d, err := e.Decide(context.Background(), authz.Input{
		User: authz.UserInput{
			ID:    "oauth_client:0e3b4a7e-1111-2222-3333-444455556666",
			Roles: nil, // OAuth client_credentials has no per-tenant roles
			Attrs: map[string]interface{}{
				"is_machine_actor": true,
			},
		},
		TenantID: "00000000-0000-4000-8000-000000000001",
		Action:   "upload-bundle",
		Resource: authz.ResourceInput{Type: "controls"},
		Request: authz.RequestInput{
			Method: "POST",
			Path:   "/v1/controls:upload-bundle",
		},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if !d.Allow {
		t.Fatalf("expected allow on machine-actor upload-bundle controls, got deny: %s", d.Reason)
	}
}

// TestSlice196_HumanActorUploadBundleStillRoleGated is the regression
// guard: a HUMAN actor (is_machine_actor=false) with no roles must NOT
// get the machine-actor carve-out — they still hit default-deny. If
// this test passes when system.rego is over-broad (e.g., dropping the
// is_machine_actor predicate), the carve-out has accidentally opened
// upload-bundle to every authenticated human.
func TestSlice196_HumanActorUploadBundleStillRoleGated(t *testing.T) {
	t.Parallel()
	e := engine(t)
	d, err := e.Decide(context.Background(), authz.Input{
		User: authz.UserInput{
			ID:    "11111111-2222-3333-4444-555555555555",
			Roles: nil,
			Attrs: map[string]interface{}{
				"is_machine_actor": false,
			},
		},
		TenantID: "00000000-0000-4000-8000-000000000001",
		Action:   "upload-bundle",
		Resource: authz.ResourceInput{Type: "controls"},
		Request: authz.RequestInput{
			Method: "POST",
			Path:   "/v1/controls:upload-bundle",
		},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d.Allow {
		t.Fatalf("expected deny on human-actor upload-bundle without role, got allow")
	}
}

// TestSlice196_MachineActorUploadBundleDeniedOnOtherResource is the
// other regression guard: a machine actor cannot use upload-bundle to
// write to ANY resource type — the carve-out is scoped to controls.
// If this test passes when system.rego widens the resource match
// (e.g., dropping the resource.type predicate), every resource type
// would suddenly be machine-uploadable.
func TestSlice196_MachineActorUploadBundleDeniedOnOtherResource(t *testing.T) {
	t.Parallel()
	e := engine(t)
	d, err := e.Decide(context.Background(), authz.Input{
		User: authz.UserInput{
			ID:    "oauth_client:0e3b4a7e-1111-2222-3333-444455556666",
			Roles: nil,
			Attrs: map[string]interface{}{
				"is_machine_actor": true,
			},
		},
		TenantID: "00000000-4000-4000-8000-000000000001",
		Action:   "upload-bundle",
		Resource: authz.ResourceInput{Type: "policies"},
		Request: authz.RequestInput{
			Method: "POST",
			Path:   "/v1/policies:upload-bundle",
		},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d.Allow {
		t.Fatalf("expected deny on machine-actor upload-bundle policies, got allow — carve-out leaked to non-controls resource")
	}
}
