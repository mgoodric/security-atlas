package walkthroughs

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/audit/walkthrough"
	"github.com/mgoodric/security-atlas/internal/pdfrender"
)

// #2786: chrome absent is a 409 feature_not_enabled with no Retry-After; the
// two transient modes stay 503 with Retry-After; anything else is not a
// degradation.
func TestWritePDFDegradation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		err        error
		wantOK     bool
		wantStatus int
		wantCode   string
		wantRetry  string
	}{
		{"chrome absent", fmt.Errorf("%w: exec: no chrome", walkthrough.ErrChromeUnavailable), true, http.StatusConflict, httpresp.CodeFeatureNotEnabled, ""},
		{"render deadline", pdfrender.ErrRenderDeadline, true, http.StatusServiceUnavailable, "", strconv.Itoa(pdfrender.RetryAfterSeconds)},
		{"queue saturated", pdfrender.ErrQueueSaturated, true, http.StatusServiceUnavailable, "", strconv.Itoa(pdfrender.RetryAfterSeconds)},
		{"genuine bug", errors.New("template exploded"), false, 0, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/v1/walkthroughs/x/export?format=pdf", nil)
			ok := writePDFDegradation(w, r, tc.err)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				if w.Body.Len() != 0 {
					t.Fatalf("non-degradation wrote a body: %q", w.Body.String())
				}
				return
			}
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", w.Code, tc.wantStatus)
			}
			if got := w.Header().Get("Retry-After"); got != tc.wantRetry {
				t.Errorf("Retry-After = %q, want %q", got, tc.wantRetry)
			}
			var body map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("body not JSON: %v (%q)", err, w.Body.String())
			}
			if body["code"] != tc.wantCode {
				t.Errorf("code = %q, want %q", body["code"], tc.wantCode)
			}
			if body["error"] == "" {
				t.Error("error message is empty")
			}
		})
	}
}
