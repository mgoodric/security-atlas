package admindemo

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/demoseed"
)

// #3094: POST /v1/admin/demo/teardown answers a teardown that gave up on the
// audit_periods lock with 503, Retry-After and the teardown_busy code, and
// every other failure with the generic 500. The busy error reaches the
// handler wrapped, as demoseed returns it.
func TestWriteTeardownError(t *testing.T) {
	t.Parallel()
	busy := fmt.Errorf("demoseed: teardown exec audit_periods: %w",
		fmt.Errorf("%w: canceling statement due to lock timeout (SQLSTATE 55P03)", demoseed.ErrTeardownBusy))
	for name, tc := range map[string]struct {
		err        error
		status     int
		retryAfter string
		code       string
	}{
		"busy":  {busy, http.StatusServiceUnavailable, "5", "teardown_busy"},
		"other": {errors.New("demoseed: teardown commit: boom"), http.StatusInternalServerError, "", ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := httptest.NewRecorder()
			writeTeardownError(w, httptest.NewRequest(http.MethodPost, "/v1/admin/demo/teardown", nil), tc.err)
			if w.Code != tc.status {
				t.Errorf("status = %d, want %d", w.Code, tc.status)
			}
			if got := w.Header().Get("Retry-After"); got != tc.retryAfter {
				t.Errorf("Retry-After = %q, want %q", got, tc.retryAfter)
			}
			var body map[string]string
			if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body["code"] != tc.code {
				t.Errorf("code = %q, want %q", body["code"], tc.code)
			}
			if body["error"] != "internal error" {
				t.Errorf("error = %q, want the generic message, no error detail", body["error"])
			}
		})
	}
}
