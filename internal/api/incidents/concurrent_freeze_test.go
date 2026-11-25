package incidents

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
)

// An incident close refused by a concurrent audit-period freeze wrote
// nothing and succeeds on retry, so it is a 409 concurrent_freeze, not a 500
// (#3060).
func TestWriteErr_ConcurrentFreezeIs409(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("incident: ingest incident postmortem evidence: %w", ingest.ErrConcurrentFreeze)
	rec := httptest.NewRecorder()
	(&Handler{}).writeErr(rec, httptest.NewRequest(http.MethodPost, "/v1/incidents/x/close", nil), "close", err)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	var body map[string]string
	if jerr := json.Unmarshal(rec.Body.Bytes(), &body); jerr != nil || body["code"] != "concurrent_freeze" {
		t.Fatalf("body = %s (%v), want code concurrent_freeze", rec.Body.String(), jerr)
	}
}
