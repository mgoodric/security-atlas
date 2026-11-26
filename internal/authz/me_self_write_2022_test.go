package authz_test

import (
	"testing"

	"github.com/mgoodric/security-atlas/internal/authz"
)

func meWriteInput(roles []authz.Role, method, path string) authz.Input {
	in := meReadInput(roles, path)
	in.Action = "write"
	in.Request.Method = method
	return in
}

type meRoute struct{ method, path string }

// meSelfWrites are the /v1/me settings writes every signed-in role makes
// on its own account (defaults.rego me_self_writes + me_self_id_write).
var meSelfWrites = []meRoute{
	{"PATCH", "/v1/me"},
	{"PATCH", "/v1/me/preferences"},
	{"PUT", "/v1/me/email-channel"},
	{"PUT", "/v1/me/slack-channel"},
	{"PUT", "/v1/me/webhook-channel"},
	{"DELETE", "/v1/me/sessions"},
	{"DELETE", "/v1/me/sessions/sess-abc"},
	{"PATCH", "/v1/me/notifications/n-1/read"},
}

// #2022: every role, viewer and control_owner included, changes its own
// /v1/me settings and revokes its own sessions.
func TestMeSelfWrite2022_EveryRoleWrites(t *testing.T) {
	t.Parallel()
	for _, r := range []authz.Role{
		authz.RoleViewer, authz.RoleControlOwner, authz.RoleAuditor,
		authz.RoleGRCEngineer, authz.RoleAdmin,
	} {
		for _, rt := range meSelfWrites {
			if d := decide(t, meWriteInput([]authz.Role{r}, rt.method, rt.path)); !d.Allow {
				t.Errorf("%s denied %s %s: %s", r, rt.method, rt.path, d.Reason)
			}
		}
	}
}

// #2022: the rule pins method and path. A listed path under another
// method, a read-only /v1/me path, a deeper or empty id segment, and a
// roleless credential stay denied.
func TestMeSelfWrite2022_DeniesTheRest(t *testing.T) {
	t.Parallel()
	denied := []meRoute{
		// listed path, other method
		{"DELETE", "/v1/me"},
		{"PUT", "/v1/me/preferences"},
		{"PATCH", "/v1/me/email-channel"},
		{"POST", "/v1/me/sessions"},
		{"PATCH", "/v1/me/sessions/sess-abc"},
		{"DELETE", "/v1/me/notifications/n-1/read"},
		// read-only /v1/me paths
		{"PATCH", "/v1/me/tenants"},
		{"PUT", "/v1/me/tenancy"},
		{"DELETE", "/v1/me/notifications"},
		{"POST", "/v1/me/acknowledgments"},
		{"PATCH", "/v1/me/audit-period"},
		// shape of the id-bearing routes
		{"DELETE", "/v1/me/sessions/"},
		{"DELETE", "/v1/me/sessions/sess-abc/extra"},
		{"PATCH", "/v1/me/notifications//read"},
		{"PATCH", "/v1/me/notifications/n-1"},
		{"PATCH", "/v1/me/notifications/n-1/read/extra"},
		{"PATCH", "/v1/me/notifications/n-1/unread"},
		// unlisted
		{"PATCH", "/v1/me/"},
		{"PUT", "/v1/me/not-a-route"},
	}
	for _, r := range []authz.Role{authz.RoleViewer, authz.RoleControlOwner} {
		for _, rt := range denied {
			if d := decide(t, meWriteInput([]authz.Role{r}, rt.method, rt.path)); d.Allow {
				t.Errorf("%s admitted to %s %s; only the listed /v1/me writes are open to every role", r, rt.method, rt.path)
			}
		}
	}
	for _, rt := range meSelfWrites {
		if d := decide(t, meWriteInput(nil, rt.method, rt.path)); d.Allow {
			t.Errorf("roleless credential admitted to %s %s", rt.method, rt.path)
		}
	}
}

// #2022: the rule is bound to resource type "me". The same method and
// path shape on another resource type is not admitted through it.
func TestMeSelfWrite2022_OnlyResourceMe(t *testing.T) {
	t.Parallel()
	in := meWriteInput([]authz.Role{authz.RoleViewer}, "PATCH", "/v1/me/preferences")
	in.Resource.Type = "controls"
	if d := decide(t, in); d.Allow {
		t.Errorf("viewer admitted to a controls write through the /v1/me rule")
	}
}
