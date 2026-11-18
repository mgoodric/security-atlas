// Pure-Go tests for writeStoreErr's mapping of the #2449 mitigate-rule
// refusal: a 409 whose message names the risk, without the store's
// wrapping.

package actionplans

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/actionplan"
)

func TestWriteStoreErr_MitigationRequiredIs409NamingTheRisk(t *testing.T) {
	t.Parallel()
	risk := uuid.New()
	for name, err := range map[string]error{
		"bare":    &actionplan.MitigationRequiredError{RiskID: risk},
		"wrapped": fmt.Errorf("delete link: %w", &actionplan.MitigationRequiredError{RiskID: risk}),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodDelete, "/v1/action-plans/x", nil)
			(&Handler{}).writeStoreErr(rec, req, "test", err)
			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409", rec.Code)
			}
			body := rec.Body.String()
			if !strings.Contains(body, risk.String()) {
				t.Fatalf("body %q does not name risk %s", body, risk)
			}
			if strings.Contains(body, "delete link") {
				t.Fatalf("body %q leaks the store's wrapping", body)
			}
		})
	}
}
