package policies

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	policypdf "github.com/mgoodric/security-atlas/internal/policy/pdf"
)

// #2786: a policy PDF on a deployment with no chrome is a 409
// feature_not_enabled, not a retryable 503; any other render error is a 500.
func TestWritePDFRenderErr(t *testing.T) {
	t.Parallel()

	t.Run("chrome absent", func(t *testing.T) {
		t.Parallel()
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/v1/policies/x/pdf", nil)
		writePDFRenderErr(w, r, fmt.Errorf("%w: exec: no chrome", policypdf.ErrChromeUnavailable))
		if w.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409", w.Code)
		}
		if got := w.Header().Get("Retry-After"); got != "" {
			t.Errorf("Retry-After = %q, want none", got)
		}
		var body map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("body not JSON: %v (%q)", err, w.Body.String())
		}
		if body["code"] != httpresp.CodeFeatureNotEnabled {
			t.Errorf("code = %q, want %q", body["code"], httpresp.CodeFeatureNotEnabled)
		}
	})

	t.Run("genuine bug", func(t *testing.T) {
		t.Parallel()
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/v1/policies/x/pdf", nil)
		writePDFRenderErr(w, r, errors.New("template exploded"))
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", w.Code)
		}
	})
}
