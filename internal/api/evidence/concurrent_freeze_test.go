package evidence

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/mgoodric/security-atlas/internal/evidence/historicalimport"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
)

// A push refused by a concurrent freeze on every attempt wrote nothing; both
// transports answer it as a retryable conflict, not an internal error
// (#3060).
func TestConcurrentFreeze_PushAnswersConflict(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writeBatchError(rec, 0, ingest.DecisionRejectedConcurrentFreeze, ingest.ErrConcurrentFreeze)
	if rec.Code != http.StatusConflict {
		t.Fatalf("HTTP status = %d, want 409", rec.Code)
	}
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Code != "concurrent_freeze" {
		t.Fatalf("HTTP body = %s (%v), want code concurrent_freeze", rec.Body.String(), err)
	}
	if got := status.Code(decisionToGRPC(ingest.DecisionRejectedConcurrentFreeze, ingest.ErrConcurrentFreeze)); got != codes.Aborted {
		t.Fatalf("gRPC code = %s, want Aborted", got)
	}
}

// The import's sentinel wraps the live-ingest one, so a caller can match
// either (#3060).
func TestHistoricalImportConcurrentFreezeWrapsIngest(t *testing.T) {
	t.Parallel()
	if !errors.Is(historicalimport.ErrConcurrentFreeze, ingest.ErrConcurrentFreeze) {
		t.Fatal("historicalimport.ErrConcurrentFreeze does not wrap ingest.ErrConcurrentFreeze")
	}
}
