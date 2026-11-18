package authz_test

// Slice 135 — OPA matrix tests for GET /v1/admin/audit-log/export.
//
// Constitutional anchor: slice 135 P0-A9 — "OPA admit set matches the
// underlying read endpoint EXACTLY." The export endpoint is the SAME
// underlying query as slice 124's /v1/admin/audit-log/unified read, with a
// format-encoder swap; admitting any narrower or wider role set would
// silently elevate or restrict access without surfacing the divergence
// in code review.
//
// Both routes resolve to resource type "admin" (id "audit-log"), so the
// inputs are built from the real paths (unifiedAuditLogInput, slice124_test.go)
// and parity is checked on what a caller actually hits (#2378).

import (
	"context"
	"net/http"
	"testing"

	"github.com/mgoodric/security-atlas/internal/authz"
)

// slice135RolePairs are the canonical roles plus the "no-role" baseline
// that exercises the default-deny path.
//
// #2503: "admin" is an admin credential (cred.IsAdmin); a tenant admin by
// role alone is refused, as the handler's callerAllowedUnified refuses it.
var slice135RolePairs = []struct {
	name      string
	roles     []authz.Role
	adminCred bool
	want      bool
}{
	{"admin", []authz.Role{authz.RoleAdmin}, true, true},
	{"tenant_admin_by_role", []authz.Role{authz.RoleAdmin}, false, false},
	{"auditor", []authz.Role{authz.RoleAuditor}, false, false},
	{"grc_engineer", []authz.Role{authz.RoleGRCEngineer}, false, true},
	{"viewer", []authz.Role{authz.RoleViewer}, false, false},
	{"control_owner", []authz.Role{authz.RoleControlOwner}, false, false},
	{"no_roles", nil, false, false},
}

// TestSlice135_UnifiedAuditLogExportAccess pins the per-role admit
// matrix for the export route.
func TestSlice135_UnifiedAuditLogExportAccess(t *testing.T) {
	t.Parallel()
	e := engine(t)

	for _, tc := range slice135RolePairs {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := unifiedAuditLogInput(t, http.MethodGet, "/v1/admin/audit-log/export?format=csv", tc.roles)
			in.User.Attrs["is_admin_credential"] = tc.adminCred
			d, err := e.Decide(context.Background(), in)
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
// test: for every role, the decision on the unified read MUST match the
// decision on the export.
func TestSlice135_AdmitSetParityWithUnifiedRead(t *testing.T) {
	t.Parallel()
	e := engine(t)

	for _, tc := range slice135RolePairs {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			readDec, err := e.Decide(context.Background(),
				unifiedAuditLogInput(t, http.MethodGet, "/v1/admin/audit-log/unified", tc.roles))
			if err != nil {
				t.Fatalf("Decide(unified): %v", err)
			}
			exportDec, err := e.Decide(context.Background(),
				unifiedAuditLogInput(t, http.MethodGet, "/v1/admin/audit-log/export", tc.roles))
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

// TestSlice135_AuditorExportWriteDenied — defensive: the export is
// read-only by design (it generates a download from a read and never
// mutates the audit log).
func TestSlice135_AuditorExportWriteDenied(t *testing.T) {
	t.Parallel()
	e := engine(t)
	d, err := e.Decide(context.Background(),
		unifiedAuditLogInput(t, http.MethodPost, "/v1/admin/audit-log/export", []authz.Role{authz.RoleAuditor}))
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d.Allow {
		t.Errorf("auditor write on /v1/admin/audit-log/export should be denied; got allow")
	}
}
