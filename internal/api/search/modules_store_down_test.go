package search

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/moduleaccess/moduleaccesstest"
)

// #2702: search leaves out types whose module is switched off; when the
// flag store cannot read a switch, it answers 503 module_state_unavailable,
// as ModuleGate does, not a plain 500.
func TestHandle_StoreDownIs503(t *testing.T) {
	t.Parallel()
	h := New(nil, nil).WithModules(moduleaccesstest.StoreDown())
	req := moduleaccesstest.SuperAdminRequest(t, http.MethodGet, "/v1/search?q=access")

	rec := httptest.NewRecorder()
	h.Handle(rec, req)

	moduleaccesstest.AssertUnavailable(t, rec)
}
