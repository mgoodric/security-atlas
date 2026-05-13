package authz_test

import (
	"context"
	"testing"

	"github.com/mgoodric/security-atlas/internal/authz"
)

// decide is a thin shorthand for the slice-025 rego rule tests.
func decide(t *testing.T, in authz.Input) authz.Decision {
	t.Helper()
	e := engine(t)
	d, err := e.Decide(context.Background(), in)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	return d
}

const (
	periodA = "00000000-0000-0000-0000-0000000000aa"
	periodB = "00000000-0000-0000-0000-0000000000bb"
	tenant  = "00000000-0000-0000-0000-000000000010"
)

func auditorInput(action, resource string, periodAttr string) authz.Input {
	in := authz.Input{
		User: authz.UserInput{
			ID:    "user-auditor",
			Roles: []authz.Role{authz.RoleAuditor},
			Attrs: map[string]interface{}{
				"audit_period_ids": []interface{}{periodA},
			},
		},
		TenantID: tenant,
		Action:   action,
		Resource: authz.ResourceInput{Type: resource},
		Request:  authz.RequestInput{Method: "GET", Path: "/v1/" + resource},
	}
	if periodAttr != "" {
		in.Resource.Attrs = map[string]interface{}{
			"audit_period_id": periodAttr,
		}
	}
	return in
}

// AC-1: the auditor role is recognized by the policy bundle.
func TestSlice025_AuditorReadAuditNotesAllowed(t *testing.T) {
	t.Parallel()
	d := decide(t, auditorInput("read", "audit-notes", ""))
	if !d.Allow {
		t.Fatalf("expected allow on auditor read audit-notes, got deny: %s", d.Reason)
	}
}

// AC-4: auditor can write a note INTO their assigned period.
func TestSlice025_AuditorWriteAuditNoteInPeriodAllowed(t *testing.T) {
	t.Parallel()
	in := auditorInput("write", "audit-notes", periodA)
	in.Request = authz.RequestInput{Method: "POST", Path: "/v1/audit-notes"}
	d := decide(t, in)
	if !d.Allow {
		t.Fatalf("expected allow on auditor write within period, got deny: %s", d.Reason)
	}
}

// P0-3 / AC-4: cross-period writes are denied (auditor assigned to
// periodA cannot write a note targeting periodB).
func TestSlice025_AuditorWriteAuditNoteCrossPeriodDenied(t *testing.T) {
	t.Parallel()
	in := auditorInput("write", "audit-notes", periodB)
	in.Request = authz.RequestInput{Method: "POST", Path: "/v1/audit-notes"}
	d := decide(t, in)
	if d.Allow {
		t.Fatalf("expected deny on cross-period write, got allow")
	}
}

// AC-5: auditor can read /v1/me (the resource type derives to "me"
// from the path).
func TestSlice025_AuditorReadMeAllowed(t *testing.T) {
	t.Parallel()
	in := authz.Input{
		User: authz.UserInput{
			ID:    "user-auditor",
			Roles: []authz.Role{authz.RoleAuditor},
			Attrs: map[string]interface{}{},
		},
		TenantID: tenant,
		Action:   "read",
		Resource: authz.ResourceInput{Type: "me"},
		Request:  authz.RequestInput{Method: "GET", Path: "/v1/me/audit-period"},
	}
	d := decide(t, in)
	if !d.Allow {
		t.Fatalf("expected allow on auditor /v1/me, got deny: %s", d.Reason)
	}
}

// AC-2: auditor can read /v1/audit-periods (handled by the
// unconditional auditor_readable_resources allow rule).
func TestSlice025_AuditorReadAuditPeriodsAllowed(t *testing.T) {
	t.Parallel()
	in := authz.Input{
		User: authz.UserInput{
			ID:    "user-auditor",
			Roles: []authz.Role{authz.RoleAuditor},
			Attrs: map[string]interface{}{},
		},
		TenantID: tenant,
		Action:   "read",
		Resource: authz.ResourceInput{Type: "audit-periods"},
		Request:  authz.RequestInput{Method: "GET", Path: "/v1/audit-periods"},
	}
	d := decide(t, in)
	if !d.Allow {
		t.Fatalf("expected allow on auditor read audit-periods, got deny: %s", d.Reason)
	}
}

// P0-1 / AC-3: auditor cannot mutate non-audit-notes resources.
// Table-driven across a representative slice of mutating endpoints.
func TestSlice025_AuditorMutationsDenied(t *testing.T) {
	t.Parallel()
	cases := []struct {
		method   string
		path     string
		resource string
		action   string
	}{
		{"POST", "/v1/risks", "risks", "write"},
		{"POST", "/v1/policies", "policies", "write"},
		{"POST", "/v1/exceptions", "exceptions", "write"},
		{"POST", "/v1/controls:upload-bundle", "controls", "upload-bundle"},
		{"POST", "/v1/vendors", "vendors", "write"},
		{"POST", "/v1/audit-periods", "audit-periods", "write"},
		{"PATCH", "/v1/policies/abc/submit", "policies", "submit"},
		{"PATCH", "/v1/exceptions/abc/approve", "exceptions", "approve"},
		{"POST", "/v1/audit-periods/abc/freeze", "audit-periods", "write"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			t.Parallel()
			in := authz.Input{
				User: authz.UserInput{
					ID:    "user-auditor",
					Roles: []authz.Role{authz.RoleAuditor},
					Attrs: map[string]interface{}{
						"audit_period_ids": []interface{}{periodA},
					},
				},
				TenantID: tenant,
				Action:   tc.action,
				Resource: authz.ResourceInput{Type: tc.resource},
				Request:  authz.RequestInput{Method: tc.method, Path: tc.path},
			}
			d := decide(t, in)
			if d.Allow {
				t.Fatalf("expected DENY on auditor %s %s, got allow", tc.method, tc.path)
			}
		})
	}
}

// P0-2 (visibility): the rego layer allows grc_engineer to hit the
// /v1/audit-notes read endpoint (grc_engineer has tenant-wide read in
// the slice-035 baseline policy), but the query layer enforces
// `author_user_id = caller.UserID` so the auditee sees an empty list.
// This test confirms the rego layer's permissive read; the empty-list
// guarantee is exercised by the integration test (notes/integration_test.go).
func TestSlice025_GRCEngineerReadAuditNotesAllowedButFiltered(t *testing.T) {
	t.Parallel()
	in := authz.Input{
		User: authz.UserInput{
			ID:    "user-grc",
			Roles: []authz.Role{authz.RoleGRCEngineer},
			Attrs: map[string]interface{}{},
		},
		TenantID: tenant,
		Action:   "read",
		Resource: authz.ResourceInput{Type: "audit-notes"},
		Request:  authz.RequestInput{Method: "GET", Path: "/v1/audit-notes"},
	}
	d := decide(t, in)
	if !d.Allow {
		t.Fatalf("expected allow on grc_engineer read (visibility enforced at query layer), got deny")
	}
}

// P0-2: grc_engineer cannot WRITE audit-notes. The grc_writable_resources
// set does not include "audit-notes"; default-deny on write.
func TestSlice025_GRCEngineerWriteAuditNotesDenied(t *testing.T) {
	t.Parallel()
	in := authz.Input{
		User: authz.UserInput{
			ID:    "user-grc",
			Roles: []authz.Role{authz.RoleGRCEngineer},
			Attrs: map[string]interface{}{},
		},
		TenantID: tenant,
		Action:   "write",
		Resource: authz.ResourceInput{
			Type:  "audit-notes",
			Attrs: map[string]interface{}{"audit_period_id": periodA},
		},
		Request: authz.RequestInput{Method: "POST", Path: "/v1/audit-notes"},
	}
	d := decide(t, in)
	if d.Allow {
		t.Fatalf("expected deny on grc_engineer write audit-notes, got allow")
	}
}

// P0-2: a viewer cannot read audit-notes either.
func TestSlice025_ViewerCannotReadAuditNotes(t *testing.T) {
	t.Parallel()
	in := authz.Input{
		User: authz.UserInput{
			ID:    "user-viewer",
			Roles: []authz.Role{authz.RoleViewer},
			Attrs: map[string]interface{}{},
		},
		TenantID: tenant,
		Action:   "read",
		Resource: authz.ResourceInput{Type: "audit-notes"},
		Request:  authz.RequestInput{Method: "GET", Path: "/v1/audit-notes"},
	}
	d := decide(t, in)
	if d.Allow {
		t.Fatalf("expected deny on viewer read audit-notes (P0-2), got allow")
	}
}

// P0-3: auditor with empty audit_period_ids cannot write audit-notes
// (the auditor_period_matches rule needs at least one assignment).
func TestSlice025_AuditorWithoutAssignmentDeniedWrite(t *testing.T) {
	t.Parallel()
	in := authz.Input{
		User: authz.UserInput{
			ID:    "user-auditor-unassigned",
			Roles: []authz.Role{authz.RoleAuditor},
			Attrs: map[string]interface{}{
				"audit_period_ids": []interface{}{},
			},
		},
		TenantID: tenant,
		Action:   "write",
		Resource: authz.ResourceInput{
			Type:  "audit-notes",
			Attrs: map[string]interface{}{"audit_period_id": periodA},
		},
		Request: authz.RequestInput{Method: "POST", Path: "/v1/audit-notes"},
	}
	d := decide(t, in)
	if d.Allow {
		t.Fatalf("expected deny on unassigned-auditor write (P0-3), got allow")
	}
}
