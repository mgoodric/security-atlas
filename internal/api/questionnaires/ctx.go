package questionnaires

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/pdfrender"
	"github.com/mgoodric/security-atlas/internal/questionnaire"
)

// chromeAbsentMsg is the refusal for a deployment with no chrome installed.
const chromeAbsentMsg = "PDF export is not enabled on this deployment: chrome is not installed"

// writePDFDegradation answers a questionnaire PDF-render error that is one of
// the three documented degradation modes (slice 475) and reports true; a
// genuine bug returns false so the caller falls through to a 500.
//
//   - chrome absent: 409 feature_not_enabled. Retrying cannot install chrome,
//     so this is a refusal about the deployment, not a server failure (#2786).
//   - render deadline exceeded, render queue saturated: 503 with Retry-After.
//     Both clear on their own once the renders ahead finish.
func writePDFDegradation(w http.ResponseWriter, r *http.Request, err error) bool {
	var msg string
	switch {
	case errors.Is(err, questionnaire.ErrChromeUnavailable):
		logPDFDegradation(r, err)
		httpresp.WriteFeatureNotEnabled(w, chromeAbsentMsg)
		return true
	case errors.Is(err, pdfrender.ErrRenderDeadline):
		msg = "PDF export unavailable: render deadline exceeded, retry shortly"
	case errors.Is(err, pdfrender.ErrQueueSaturated):
		msg = "PDF export busy: too many concurrent renders, retry shortly"
	default:
		return false
	}
	logPDFDegradation(r, err)
	w.Header().Set("Retry-After", strconv.Itoa(pdfrender.RetryAfterSeconds))
	httpresp.WriteError(w, http.StatusServiceUnavailable, msg)
	return true
}

// logPDFDegradation emits a WARN distinguishing the degradation mode (AC-5).
func logPDFDegradation(r *http.Request, err error) {
	slog.WarnContext(r.Context(), "pdf render degraded",
		"artifact", "questionnaire",
		"reason", pdfDegradationReason(err),
		"path", r.URL.Path,
	)
}

func pdfDegradationReason(err error) string {
	switch {
	case errors.Is(err, questionnaire.ErrChromeUnavailable):
		return "chrome_absent"
	case errors.Is(err, pdfrender.ErrRenderDeadline):
		return "render_deadline_exceeded"
	case errors.Is(err, pdfrender.ErrQueueSaturated):
		return "render_queue_saturated"
	default:
		return "unknown"
	}
}
