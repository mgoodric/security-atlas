package calendar

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/moduleaccess/moduleaccesstest"
)

// #2702: the calendar leaves out event types whose module is switched off;
// when the flag store cannot read a switch, the list answers 503
// module_state_unavailable, as ModuleGate does, not a plain 500.
func TestListEvents_StoreDownIs503(t *testing.T) {
	t.Parallel()
	h := New(nil, nil).WithModules(moduleaccesstest.StoreDown())
	req := moduleaccesstest.SuperAdminRequest(t, http.MethodGet, "/v1/calendar")

	rec := httptest.NewRecorder()
	h.ListEvents(rec, req)

	moduleaccesstest.AssertUnavailable(t, rec)
}
