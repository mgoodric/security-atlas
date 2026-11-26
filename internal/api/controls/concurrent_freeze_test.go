package controls

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
)

// An attestation a concurrent freeze refused on every Process attempt wrote
// nothing and succeeds on retry, so it is a 409 concurrent_freeze, not a
// 500 (#3060).
func TestWriteIngestError_ConcurrentFreezeIs409(t *testing.T) {
	t.Parallel()
	if got := ingestErrorToStatus(ingest.ErrConcurrentFreeze); got != http.StatusConflict {
		t.Fatalf("ingestErrorToStatus = %d, want 409", got)
	}
	rec := httptest.NewRecorder()
	writeIngestError(rec, fmt.Errorf("attest: %w", ingest.ErrConcurrentFreeze))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["code"] != "concurrent_freeze" {
		t.Fatalf("body = %s (%v), want code concurrent_freeze", rec.Body.String(), err)
	}
}
