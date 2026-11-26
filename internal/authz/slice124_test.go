package authz_test

// Slice 124 — OPA matrix tests for GET /v1/admin/audit-log/unified.
//
// The slice ships read-only access to a tenant-scoped UNION ALL across the
// per-domain audit-log tables. The route lives under /v1/admin/, so
// BuildInput resolves it to resource type "admin" (id "audit-log"): admin
// and grc_engineer read it, every other role is default-deny. Auditors read
// the same rows, payload-gated per kind, through /v1/activity/unified
// (slice 270, #2375). Until #2378 these tests decided a hand-built
// "audit-log-unified" resource the real path never produces, and pinned an
// auditor admit that never applied.
//
// The inputs are built from the real request path, the way authzmw builds
// them, so the matrix is pinned at the path a caller hits.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
)

// unifiedAuditLogInput builds the OPA input authzmw builds for method+path.
func unifiedAuditLogInput(t *testing.T, method, path string, roles []authz.Role) authz.Input {
	t.Helper()
	r := httptest.NewRequest(method, path, nil)
	r = r.WithContext(authctx.WithCredential(r.Context(), credstore.Credential{
		ID: "jwt:124", TenantID: "00000000-0000-0000-0000-000000000124", UserID: "user:124",
	}))
	in := authz.BuildInput(r, nil)
	if in.Resource.Type != "admin" || in.Resource.ID != "audit-log" {
		t.Fatalf("%s resolves to %s/%s, want admin/audit-log", path, in.Resource.Type, in.Resource.ID)
	}
	in.User.Roles = roles
	return in
}

func TestSlice124_UnifiedAuditLogAccess(t *testing.T) {
	t.Parallel()
	e := engine(t)

	cases := []struct {
		name      string
		roles     []authz.Role
		adminCred bool
		wantAllow bool
		reason    string
	}{
		{
			name:      "admin-allow-read",
			roles:     []authz.Role{authz.RoleAdmin},
			adminCred: true,
			wantAllow: true,
			reason:    "an admin credential (cred.IsAdmin) reaches every non-super-admin-only /v1/admin segment (#2503)",
		},
		{
			name:      "tenant-admin-by-role-allow-read",
			roles:     []authz.Role{authz.RoleAdmin},
			wantAllow: true,
			reason:    "#2503 D5 (Matt, 2026-11-18): an org admin reads its own tenant's admin audit log",
		},
		{
			name:      "auditor-deny-read",
			roles:     []authz.Role{authz.RoleAuditor},
			wantAllow: false,
			reason:    "auditor.rego does not admit resource 'admin'; auditors use /v1/activity/unified",
		},
		{
			name:      "viewer-deny-read",
			roles:     []authz.Role{authz.RoleViewer},
			wantAllow: false,
			reason:    "viewer.rego does not admit resource 'admin' — default-deny",
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
			in := unifiedAuditLogInput(t, http.MethodGet, "/v1/admin/audit-log/unified", tc.roles)
			in.User.Attrs["is_admin_credential"] = tc.adminCred
			d, err := e.Decide(context.Background(), in)
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
	// Read-only contract: the aggregator package has no exported writer
	// (slice-124 anti-criterion P0-A8), and an auditor has no write
	// surface on the route.
	e := engine(t)
	d, err := e.Decide(context.Background(),
		unifiedAuditLogInput(t, http.MethodPost, "/v1/admin/audit-log/unified", []authz.Role{authz.RoleAuditor}))
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d.Allow {
		t.Errorf("auditor write on /v1/admin/audit-log/unified should be denied; got allow")
	}
}
