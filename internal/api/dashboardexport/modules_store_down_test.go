package dashboardexport

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/moduleaccess/moduleaccesstest"
)

// #2702: the export's upcoming and risk panels leave out modules that are
// switched off; when the flag store cannot read a switch, the export
// answers 503 module_state_unavailable, as ModuleGate does, not a plain
// 500.
func TestExportDashboard_StoreDownIs503(t *testing.T) {
	t.Parallel()
	h := NewHandler(nil, nil).WithModules(moduleaccesstest.StoreDown())
	req := moduleaccesstest.SuperAdminRequest(t, http.MethodGet, "/v1/dashboard/export")
	cred, _ := authctx.CredentialFromContext(req.Context())
	cred.IsApprover = true
	req = req.WithContext(authctx.WithCredential(req.Context(), cred))

	rec := httptest.NewRecorder()
	h.ExportDashboard(rec, req)

	moduleaccesstest.AssertUnavailable(t, rec)
}
