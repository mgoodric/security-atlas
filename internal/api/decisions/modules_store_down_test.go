package decisions

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/decision"
	"github.com/mgoodric/security-atlas/internal/moduleaccess/moduleaccesstest"
)

// #2702: a decision's Exceptions links hang on a module switch; when the
// flag store cannot read it, the read answers 503 module_state_unavailable,
// as ModuleGate does, not a plain 500.
func TestWriteDecision_StoreDownIs503(t *testing.T) {
	t.Parallel()
	h := New(nil).WithModules(moduleaccesstest.StoreDown())
	req := moduleaccesstest.SuperAdminRequest(t, http.MethodGet, "/v1/decisions/x")

	rec := httptest.NewRecorder()
	h.writeDecision(rec, req, decision.Decision{ID: uuid.New()}, decision.Linkage{})

	moduleaccesstest.AssertUnavailable(t, rec)
}
