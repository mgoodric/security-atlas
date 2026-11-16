package authz_test

// #2106 — linking and unlinking a control on a risk derive write on
// "risks", which the risk writers (admin, grc_engineer) may take. The
// unlink is a DELETE under a risk, so it is not the admin-only hard
// delete.

import (
	"net/http"
	"testing"

	"github.com/mgoodric/security-atlas/internal/authz"
)

const control2106 = "1a3c5e70-2b4d-4f60-8a1c-3e5f70902106"

func TestRiskControlLinks2106_AdmitTheRiskWriters(t *testing.T) {
	t.Parallel()
	e := engine(t)
	base := "/v1/risks/" + lifecycle2107Risk + "/controls"
	for _, req := range []struct{ method, path string }{
		{http.MethodPost, base},
		{http.MethodDelete, base + "/" + control2106},
	} {
		in := lifecycle2107Input(req.method, req.path)
		if in.Action != "write" || in.Resource.Type != "risks" {
			t.Fatalf("%s %s derives %s on %s, want write on risks", req.method, req.path, in.Action, in.Resource.Type)
		}
		decide2107(t, e, in, []lifecycle2107Case{
			{"admin", []authz.Role{authz.RoleAdmin}, true},
			{"grc_engineer", []authz.Role{authz.RoleGRCEngineer}, true},
			{"auditor", []authz.Role{authz.RoleAuditor}, false},
			{"control_owner", []authz.Role{authz.RoleControlOwner}, false},
			{"viewer", []authz.Role{authz.RoleViewer}, false},
			{"no_role", []authz.Role{}, false},
		})
	}
}
