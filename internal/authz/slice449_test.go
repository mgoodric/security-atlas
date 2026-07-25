// Slice 449 — OPA 1.17 regression gate (STRIDE-S, fail-closed).
//
// The OPA 1.4 -> 1.17 bump landed via dependabot #953 auto-merge BEFORE
// the deliberate regression pass slice 449 called for. These assertions
// backfill that gate for the dominant threat: a Rego-evaluation change
// across 13 minors that alters how `input` is coerced or how an undefined
// reference resolves, such that an unauthenticated / malformed `input`
// document evaluates permissively under 1.17.
//
// The existing decision_test.go::TestDecide_DefaultDenyEmptyRoles covers
// the empty-roles case. These tests close the two cases that file did NOT
// pin: (1) a fully empty/absent subject (zero-value UserInput — no id, no
// roles, no attrs) and (2) a malformed identity claim (non-canonical /
// garbage role strings that must not resolve to any allow rule). Both
// MUST deny under 1.17 — fail-closed is the constitutional invariant #6
// guarantee and anti-criterion P0-449-3.
package authz_test

import (
	"context"
	"testing"

	"github.com/mgoodric/security-atlas/internal/authz"
)

// TestSlice449_FailClosed_AbsentSubjectDenies asserts that a decision
// input carrying a fully zero-value subject (no user id, no roles, no
// attrs) DENIES under OPA 1.17. This is the canonical fail-closed case:
// an unauthenticated request that somehow reaches Decide with an empty
// input.user must never resolve permissively, regardless of action or
// resource. P0-449-3 / STRIDE-S.
func TestSlice449_FailClosed_AbsentSubjectDenies(t *testing.T) {
	t.Parallel()
	e := engine(t)

	cases := []struct {
		name     string
		action   string
		resource string
		method   string
		path     string
	}{
		{"write-risks", "write", "risks", "POST", "/v1/risks"},
		{"write-controls", "write", "controls", "POST", "/v1/controls"},
		{"approve-policies", "approve", "policies", "POST", "/v1/policies/p1/approve"},
		{"read-samples", "read", "samples", "GET", "/v1/samples/s1"},
		{"upload-bundle", "upload-bundle", "controls", "POST", "/v1/controls:upload-bundle"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d, err := e.Decide(context.Background(), authz.Input{
				// Zero-value subject: no ID, no roles, no attrs. This is
				// the shape an absent/forged identity collapses to.
				User:     authz.UserInput{},
				TenantID: "00000000-0000-0000-0000-000000000449",
				Action:   tc.action,
				Resource: authz.ResourceInput{Type: tc.resource},
				Request:  authz.RequestInput{Method: tc.method, Path: tc.path},
			})
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if d.Allow {
				t.Fatalf("FAIL-CLOSED VIOLATION: absent subject allowed %s %s under 1.17 (reason=%q)",
					tc.action, tc.resource, d.Reason)
			}
		})
	}
}

// TestSlice449_FailClosed_MalformedRoleClaimDenies asserts that a
// decision input carrying malformed / non-canonical identity role claims
// DENIES under OPA 1.17. A coercion or set-membership semantics change in
// 1.5..1.17 must not let a garbage role string match an allow rule. The
// roles below are deliberately NOT in the canonical set (admin /
// grc_engineer / control_owner / auditor / viewer) — a permissive result
// would mean an attacker-controlled JWT role claim could forge access.
// P0-449-3 / STRIDE-S.
func TestSlice449_FailClosed_MalformedRoleClaimDenies(t *testing.T) {
	t.Parallel()
	e := engine(t)

	malformed := [][]authz.Role{
		{authz.Role("")},                      // empty role string
		{authz.Role("ADMIN")},                 // wrong case — must not alias to admin
		{authz.Role("superadmin")},            // not a canonical role
		{authz.Role("admin; DROP")},           // injection-shaped garbage
		{authz.Role("../admin")},              // path-traversal-shaped garbage
		{authz.Role("admin\nviewer")},         // newline-embedded garbage
		{authz.Role("root"), authz.Role("*")}, // multiple non-canonical
	}
	for i, roles := range malformed {
		roles := roles
		t.Run("write-with-malformed-roles", func(t *testing.T) {
			t.Parallel()
			d, err := e.Decide(context.Background(), authz.Input{
				User: authz.UserInput{
					ID:    "malformed-subject",
					Roles: roles,
				},
				TenantID: "00000000-0000-0000-0000-000000000449",
				Action:   "write",
				Resource: authz.ResourceInput{Type: "controls"},
				Request:  authz.RequestInput{Method: "POST", Path: "/v1/controls"},
			})
			if err != nil {
				t.Fatalf("case %d Decide: %v", i, err)
			}
			if d.Allow {
				t.Fatalf("FAIL-CLOSED VIOLATION: malformed roles %v allowed write under 1.17 (reason=%q)",
					roles, d.Reason)
			}
		})
	}
}

// TestSlice449_FailClosed_NilAttrsAuditorDenies asserts an auditor whose
// ABAC attrs map is absent (nil audit_period_ids) cannot read a scoped
// sample under 1.17. The slice-025 auditor carve-out is the most
// attribute-dependent allow rule; an `input.user.attrs` coercion change
// (nil vs empty-object) is exactly the kind of 1.x semantics drift that
// could flip this deny to allow. P0-449-3 / STRIDE-S, composes with the
// audit-period freezing invariant.
func TestSlice449_FailClosed_NilAttrsAuditorDenies(t *testing.T) {
	t.Parallel()
	e := engine(t)
	d, err := e.Decide(context.Background(), authz.Input{
		User: authz.UserInput{
			ID:    "auditor-no-attrs",
			Roles: []authz.Role{authz.RoleAuditor},
			Attrs: nil, // no audit_period_ids hydrated
		},
		TenantID: "00000000-0000-0000-0000-000000000449",
		Action:   "read",
		Resource: authz.ResourceInput{
			Type:  "samples",
			ID:    "sample-449",
			Attrs: map[string]interface{}{"audit_period_id": "period-449"},
		},
		Request: authz.RequestInput{Method: "GET", Path: "/v1/samples/sample-449"},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d.Allow {
		t.Fatalf("FAIL-CLOSED VIOLATION: auditor with nil attrs read a scoped sample under 1.17 (reason=%q)", d.Reason)
	}
}
