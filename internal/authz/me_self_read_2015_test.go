package authz_test

import (
	"testing"

	"github.com/mgoodric/security-atlas/internal/authz"
)

// meSelfReadPaths are the /v1/me reads every signed-in role needs
// (defaults.rego me_self_read_paths).
var meSelfReadPaths = []string{
	"/v1/me",
	"/v1/me/tenancy",
	"/v1/me/tenants",
	"/v1/me/preferences",
	"/v1/me/notifications",
	"/v1/me/acknowledgments",
	"/v1/me/sessions",
	"/v1/me/email-channel",
	"/v1/me/slack-channel",
	"/v1/me/webhook-channel",
}

// #2015: every role, viewer and control_owner included, reads its own
// /v1/me surfaces.
func TestMeSelfRead2015_EveryRoleReads(t *testing.T) {
	t.Parallel()
	for _, r := range []authz.Role{
		authz.RoleViewer, authz.RoleControlOwner, authz.RoleAuditor,
		authz.RoleGRCEngineer, authz.RoleAdmin,
	} {
		for _, p := range meSelfReadPaths {
			if d := decide(t, meReadInput([]authz.Role{r}, p)); !d.Allow {
				t.Errorf("%s denied GET %s: %s", r, p, d.Reason)
			}
		}
	}
}

// #2015: the rule matches exact paths. The auditor-only reads, any
// sub-path or unlisted path, and a roleless credential stay denied.
func TestMeSelfRead2015_DeniesTheRest(t *testing.T) {
	t.Parallel()
	denied := []string{
		"/v1/me/audit-period",
		"/v1/me/audit-periods",
		"/v1/me/",
		"/v1/me/sessions/sess-abc",
		"/v1/me/notifications/n-1/read",
		"/v1/me/tenants/extra",
		"/v1/me/not-a-route",
	}
	for _, r := range []authz.Role{authz.RoleViewer, authz.RoleControlOwner} {
		for _, p := range denied {
			if d := decide(t, meReadInput([]authz.Role{r}, p)); d.Allow {
				t.Errorf("%s admitted to GET %s; only the listed /v1/me paths are open to every role", r, p)
			}
		}
	}
	for _, p := range meSelfReadPaths {
		if d := decide(t, meReadInput(nil, p)); d.Allow {
			t.Errorf("roleless credential admitted to GET %s", p)
		}
	}
}

// #2015: the rule admits reads only. A viewer cannot write its profile,
// preferences or channel opt-ins through it.
func TestMeSelfRead2015_WritesNotAdmitted(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ method, path string }{
		{"PATCH", "/v1/me"},
		{"PATCH", "/v1/me/preferences"},
		{"PUT", "/v1/me/email-channel"},
		{"DELETE", "/v1/me/sessions"},
	} {
		in := meReadInput([]authz.Role{authz.RoleViewer}, tc.path)
		in.Action = "write"
		in.Request.Method = tc.method
		if d := decide(t, in); d.Allow {
			t.Errorf("viewer admitted to %s %s", tc.method, tc.path)
		}
	}
}
