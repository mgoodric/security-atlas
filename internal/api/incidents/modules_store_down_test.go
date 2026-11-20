package incidents

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/incident"
	"github.com/mgoodric/security-atlas/internal/moduleaccess/moduleaccesstest"
)

// #2702: an incident's risk and vendor links hang on module switches; when
// the flag store cannot read them, the detail read and the create-time
// link check answer 503 module_state_unavailable, as ModuleGate does, not
// a plain 500.
func TestWriteDetail_StoreDownIs503(t *testing.T) {
	t.Parallel()
	h := New(nil).WithModules(moduleaccesstest.StoreDown())
	req := moduleaccesstest.SuperAdminRequest(t, http.MethodGet, "/v1/incidents/x")

	rec := httptest.NewRecorder()
	h.writeDetail(rec, req, http.StatusOK, incident.Detail{})

	moduleaccesstest.AssertUnavailable(t, rec)
}

func TestRefuseHiddenLinks_StoreDownIs503(t *testing.T) {
	t.Parallel()
	for name, ids := range map[string]parsedLinks{
		"risk_ids":   {RiskIDs: []uuid.UUID{uuid.New()}},
		"vendor_ids": {VendorIDs: []uuid.UUID{uuid.New()}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := New(nil).WithModules(moduleaccesstest.StoreDown())
			req := moduleaccesstest.SuperAdminRequest(t, http.MethodPost, "/v1/incidents")

			rec := httptest.NewRecorder()
			if !h.refuseHiddenLinks(rec, req, ids) {
				t.Fatal("refuseHiddenLinks did not answer")
			}

			moduleaccesstest.AssertUnavailable(t, rec)
		})
	}
}
