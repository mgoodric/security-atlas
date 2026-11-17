package authz_test

import (
	"testing"

	"github.com/mgoodric/security-atlas/internal/authz"
)

func needsAttentionInput(roles []authz.Role, method, path string) authz.Input {
	action := "read"
	if method != "GET" {
		action = "write"
	}
	return authz.Input{
		User:     authz.UserInput{ID: "user-2134", Roles: roles, Attrs: map[string]interface{}{}},
		TenantID: tenant,
		Action:   action,
		Resource: authz.ResourceInput{Type: "needs-attention"},
		Request:  authz.RequestInput{Method: method, Path: path},
	}
}

// #2134: every signed-in role reads GET /v1/needs-attention; the handler
// keeps for=all to admins and GRC leads.
func TestNeedsAttention2134_EveryRoleReads(t *testing.T) {
	t.Parallel()
	for _, r := range []authz.Role{
		authz.RoleViewer, authz.RoleControlOwner, authz.RoleAuditor,
		authz.RoleGRCEngineer, authz.RoleAdmin,
	} {
		if d := decide(t, needsAttentionInput([]authz.Role{r}, "GET", "/v1/needs-attention")); !d.Allow {
			t.Errorf("%s denied GET /v1/needs-attention: %s", r, d.Reason)
		}
	}
}

// #2134: the admit is the exact read. A roleless credential, a sub-path
// and a write stay denied.
func TestNeedsAttention2134_DeniesTheRest(t *testing.T) {
	t.Parallel()
	if d := decide(t, needsAttentionInput(nil, "GET", "/v1/needs-attention")); d.Allow {
		t.Error("roleless credential admitted to GET /v1/needs-attention")
	}
	for _, r := range []authz.Role{authz.RoleViewer, authz.RoleControlOwner, authz.RoleAuditor} {
		if d := decide(t, needsAttentionInput([]authz.Role{r}, "GET", "/v1/needs-attention/extra")); d.Allow {
			t.Errorf("%s admitted to GET /v1/needs-attention/extra", r)
		}
		if d := decide(t, needsAttentionInput([]authz.Role{r}, "POST", "/v1/needs-attention")); d.Allow {
			t.Errorf("%s admitted to POST /v1/needs-attention", r)
		}
	}
}
