package incidents

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
)

// TestWriteErr_IngestRefusals pins the status incident close returns when
// ingest refuses the postmortem evidence: a client-side refusal is a 4xx,
// never a 500 (#1933).
func TestWriteErr_IngestRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err  error
		want int
	}{
		{ingest.ErrObservedAtSkew, http.StatusBadRequest},
		{ingest.ErrValidation, http.StatusBadRequest},
		{ingest.ErrMissingField, http.StatusBadRequest},
		{ingest.ErrScopeViolation, http.StatusBadRequest},
		{ingest.ErrIdempotencyMismatch, http.StatusConflict},
		{ingest.ErrUnavailable, http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.err.Error(), func(t *testing.T) {
			t.Parallel()
			// The store wraps ingest errors; writeErr must see through it.
			err := fmt.Errorf("incident: ingest postmortem evidence: %w", tc.err)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/incidents/x/close", nil)
			(&Handler{}).writeErr(rec, req, "close", err)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}
