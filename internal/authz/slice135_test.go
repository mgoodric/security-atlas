package authz_test

// Slice 135 — OPA matrix tests for the `audit-log-export` resource type.
//
// Constitutional anchor: slice 135 P0-A9 — "OPA admit set matches the
// underlying read endpoint EXACTLY." The export endpoint is the SAME
// underlying query as slice 124's `audit-log-unified` read, with a
// format-encoder swap; admitting any narrower or wider role set would
// silently elevate or restrict access without surfacing the divergence
// in code review.
//
// This file enforces that contract at the rego layer. It mirrors the
// slice 124 test (TestSlice124_UnifiedAuditLogAccess) and additionally
// pins parity — every (role -> allow) pair on `audit-log-unified` MUST
// match the same pair on `audit-log-export`, for the canonical six
// roles in the platform.

import (
	"context"
	"testing"

	"github.com/mgoodric/security-atlas/internal/authz"
)

// rolePairs are the six canonical roles the platform exposes plus the
// "no-role" baseline that exercises the default-deny path. The slice
// 135 admit-set parity test asserts that for each entry here, the
// audit-log-unified decision == audit-log-export decision.
var slice135RolePairs = []struct {
	name  string
	roles []authz.Role
	want  bool
}{
	{"admin", []authz.Role{authz.RoleAdmin}, true},
	{"auditor", []authz.Role{authz.RoleAuditor}, true},
	{"grc_engineer", []authz.Role{authz.RoleGRCEngineer}, true},
	{"viewer", []authz.Role{authz.RoleViewer}, false},
	{"control_owner", []authz.Role{authz.RoleControlOwner}, false},
	{"no_roles", nil, false},
}

// TestSlice135_UnifiedAuditLogExportAccess pins the per-role admit
// matrix for the audit-log-export resource. The expected admit set is
// IDENTICAL to slice 124's audit-log-unified — that is the merge-
// blocking contract of slice 135 P0-A9.
func TestSlice135_UnifiedAuditLogExportAccess(t *testing.T) {
	t.Parallel()
	e := engine(t)

	for _, tc := range slice135RolePairs {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d, err := e.Decide(context.Background(), authz.Input{
				User: authz.UserInput{
					ID:    "user-" + tc.name,
					Roles: tc.roles,
				},
				TenantID: "00000000-0000-0000-0000-000000000135",
				Action:   "read",
				Resource: authz.ResourceInput{Type: "audit-log-export"},
				Request: authz.RequestInput{
					Method: "GET",
					Path:   "/v1/admin/audit-log/export",
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

// TestSlice135_AdmitSetParityWithUnifiedRead is the P0-A9 enforcement
// test. For every role in slice135RolePairs, the decision on
// audit-log-unified MUST match the decision on audit-log-export. If
// they ever diverge, slice 135 has silently elevated or restricted
// access relative to the underlying read endpoint — a constitutional
// regression that this test surfaces immediately.
func TestSlice135_AdmitSetParityWithUnifiedRead(t *testing.T) {
	t.Parallel()
	e := engine(t)

	for _, tc := range slice135RolePairs {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			input := func(resource string) authz.Input {
				return authz.Input{
					User: authz.UserInput{
						ID:    "user-parity-" + tc.name,
						Roles: tc.roles,
					},
					TenantID: "00000000-0000-0000-0000-000000000135",
					Action:   "read",
					Resource: authz.ResourceInput{Type: resource},
					Request: authz.RequestInput{
						Method: "GET",
						Path:   "/v1/admin/audit-log/" + resource,
					},
				}
			}
			readDec, err := e.Decide(context.Background(), input("audit-log-unified"))
			if err != nil {
				t.Fatalf("Decide(unified): %v", err)
			}
			exportDec, err := e.Decide(context.Background(), input("audit-log-export"))
			if err != nil {
				t.Fatalf("Decide(export): %v", err)
			}
			if readDec.Allow != exportDec.Allow {
				t.Errorf("PARITY VIOLATION: role=%v unified.allow=%v export.allow=%v "+
					"(slice 135 P0-A9 — export admit set MUST match underlying read endpoint EXACTLY)",
					tc.roles, readDec.Allow, exportDec.Allow)
			}
		})
	}
}

// TestSlice135_AuditorExportWriteDenied — defensive: no role gets a
// write surface on the export resource. The endpoint is read-only by
// design (export GENERATES a download from a read; it never mutates
// the audit log). Mirrors slice 124's analogous test on audit-log-unified.
func TestSlice135_AuditorExportWriteDenied(t *testing.T) {
	t.Parallel()
	e := engine(t)
	d, err := e.Decide(context.Background(), authz.Input{
		User: authz.UserInput{
			ID:    "user-write-attempt-export",
			Roles: []authz.Role{authz.RoleAuditor},
		},
		TenantID: "00000000-0000-0000-0000-000000000135",
		Action:   "write",
		Resource: authz.ResourceInput{Type: "audit-log-export"},
		Request: authz.RequestInput{
			Method: "POST",
			Path:   "/v1/admin/audit-log/export",
		},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d.Allow {
		t.Errorf("auditor.write on audit-log-export should be denied; got allow")
	}
}
