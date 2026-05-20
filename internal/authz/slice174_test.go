package authz_test

// Slice 174 — OPA matrix tests for the `anchors` resource type when
// hit via the export endpoint.
//
// Constitutional anchor: slice 174 D4 + slice 135 P0-A9 — "OPA admit
// set matches the underlying read endpoint EXACTLY." The export
// endpoint at `/v1/anchors/export` serializes the same catalog the
// `/v1/anchors` read endpoint exposes; admitting any narrower or
// wider role set would silently restrict or elevate access without
// surfacing the divergence in code review.
//
// Slice 174's special property is that the underlying read endpoint
// is a PUBLIC CATALOG read (admitted for any authenticated user via
// `defaults.rego`'s `catalog_resources` allow rule). The parity
// contract therefore reduces to: every role that can read the
// catalog can also export it; no role can export without first being
// able to read.

import (
	"context"
	"testing"

	"github.com/mgoodric/security-atlas/internal/authz"
)

// slice174RolePairs are the canonical roles plus the no-role baseline.
// The catalog read is admitted for ANY authenticated user with a
// role, so every named role in the platform admits — and the no-role
// baseline (no roles at all) also admits IF the request bears a
// recognised authenticated identity. (Per the slice 035 middleware,
// a missing credential is a 401 before authz runs; we only test the
// "has credential, has these roles" cases here.)
var slice174RolePairs = []struct {
	name  string
	roles []authz.Role
	want  bool
}{
	{"admin", []authz.Role{authz.RoleAdmin}, true},
	{"auditor", []authz.Role{authz.RoleAuditor}, true},
	{"grc_engineer", []authz.Role{authz.RoleGRCEngineer}, true},
	{"viewer", []authz.Role{authz.RoleViewer}, true},
	{"control_owner", []authz.Role{authz.RoleControlOwner}, true},
}

// TestSlice174_AnchorsExportAdmitSet pins the per-role admit matrix
// for the slice 174 export endpoint. Every named role MUST be
// admitted (parity with the existing /v1/anchors read endpoint).
func TestSlice174_AnchorsExportAdmitSet(t *testing.T) {
	t.Parallel()
	e := engine(t)

	for _, tc := range slice174RolePairs {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d, err := e.Decide(context.Background(), authz.Input{
				User: authz.UserInput{
					ID:    "user-" + tc.name,
					Roles: tc.roles,
				},
				TenantID: "00000000-0000-0000-0000-000000000174",
				Action:   "read",
				Resource: authz.ResourceInput{Type: "anchors"},
				Request: authz.RequestInput{
					Method: "GET",
					Path:   "/v1/anchors/export",
				},
			})
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if d.Allow != tc.want {
				t.Errorf("allow = %v; want %v; reason: %s",
					d.Allow, tc.want, d.Reason)
			}
		})
	}
}

// TestSlice174_AdmitSetParityWithAnchorsRead is the slice 135 P0-A9
// enforcement test for slice 174. For every role above, the decision
// on /v1/anchors (the underlying read) MUST match the decision on
// /v1/anchors/export. The export is a serialization of the read; if
// the two diverge, the export has silently re-gated access relative
// to the underlying read — a constitutional regression.
//
// Slice 174 D4 + slice 135 P0-A9 inheritance.
func TestSlice174_AdmitSetParityWithAnchorsRead(t *testing.T) {
	t.Parallel()
	e := engine(t)

	for _, tc := range slice174RolePairs {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			input := func(path string) authz.Input {
				return authz.Input{
					User: authz.UserInput{
						ID:    "user-parity-" + tc.name,
						Roles: tc.roles,
					},
					TenantID: "00000000-0000-0000-0000-000000000174",
					Action:   "read",
					Resource: authz.ResourceInput{Type: "anchors"},
					Request: authz.RequestInput{
						Method: "GET",
						Path:   path,
					},
				}
			}
			readDec, err := e.Decide(context.Background(), input("/v1/anchors"))
			if err != nil {
				t.Fatalf("Decide(read): %v", err)
			}
			exportDec, err := e.Decide(context.Background(), input("/v1/anchors/export"))
			if err != nil {
				t.Fatalf("Decide(export): %v", err)
			}
			if readDec.Allow != exportDec.Allow {
				t.Errorf("PARITY VIOLATION: role=%v read.allow=%v export.allow=%v "+
					"(slice 174 D4 + slice 135 P0-A9 — export admit set MUST match underlying read endpoint EXACTLY)",
					tc.roles, readDec.Allow, exportDec.Allow)
			}
		})
	}
}

// TestSlice174_AnchorsExportWriteDeniedForViewer — defensive: the
// viewer / auditor / control-owner / grc_engineer roles all read
// the catalog but MUST NOT have write access via the export
// endpoint. The endpoint is read-only by design; the public-catalog
// read path doesn't open a write surface. Admin is the platform's
// most-permissive role (allow everything within the tenant) so its
// "write on anchors" allow is expected; we don't test that here.
func TestSlice174_AnchorsExportWriteDeniedForViewer(t *testing.T) {
	t.Parallel()
	e := engine(t)
	for _, role := range []authz.Role{
		authz.RoleViewer,
		authz.RoleAuditor,
		authz.RoleControlOwner,
		authz.RoleGRCEngineer,
	} {
		role := role
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()
			d, err := e.Decide(context.Background(), authz.Input{
				User: authz.UserInput{
					ID:    "user-write-attempt-anchors-export-" + string(role),
					Roles: []authz.Role{role},
				},
				TenantID: "00000000-0000-0000-0000-000000000174",
				Action:   "write",
				Resource: authz.ResourceInput{Type: "anchors"},
				Request: authz.RequestInput{
					Method: "POST",
					Path:   "/v1/anchors/export",
				},
			})
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if d.Allow {
				t.Errorf("%s.write on anchors should be denied; got allow", role)
			}
		})
	}
}
