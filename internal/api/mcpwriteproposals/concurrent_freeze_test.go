package mcpwriteproposals

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
)

// A confirmed proposal whose evidence a concurrent freeze refused on every
// Process attempt wrote nothing and succeeds on retry, so it is a 409
// concurrent_freeze, not a 500 (#3060).
func TestApplyErrStatus_ConcurrentFreezeIs409(t *testing.T) {
	t.Parallel()
	status, code, ok := applyErrStatus(fmt.Errorf("apply attestation: %w", ingest.ErrConcurrentFreeze))
	if !ok || status != http.StatusConflict || code != "concurrent_freeze" {
		t.Fatalf("applyErrStatus = (%d, %q, %v), want (409, concurrent_freeze, true)", status, code, ok)
	}
}
