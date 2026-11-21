package walkthroughs

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/audit/walkthrough"
	"github.com/mgoodric/security-atlas/internal/pdfrender"
)

// chromeAbsentMsg is the refusal for a deployment with no chrome installed.
const chromeAbsentMsg = "PDF export is not enabled on this deployment: chrome is not installed"

// writePDFDegradation answers a walkthrough PDF-render error that is one of
// the three documented degradation modes (slice 477, mirroring the slice-475
// board/questionnaire pattern) and reports true; a genuine bug returns false
// so the caller falls through to a 500.
//
//   - chrome absent: 409 feature_not_enabled. Retrying cannot install chrome,
//     so this is a refusal about the deployment, not a server failure (#2786).
//   - render deadline exceeded, render queue saturated: 503 with Retry-After.
//     Both clear on their own once the renders ahead finish.
//
// Before slice 477 a render that exceeded the deadline returned a wrapped
// context.DeadlineExceeded that did NOT match walkthrough.ErrChromeUnavailable,
// so it fell through to httperr.WriteInternal = 500. Routing the render through
// pdfrender.Default().Do now classifies that case as pdfrender.ErrRenderDeadline
// → 503, deterministically.
func writePDFDegradation(w http.ResponseWriter, r *http.Request, err error) bool {
	var msg string
	switch {
	case errors.Is(err, walkthrough.ErrChromeUnavailable):
		logPDFDegradation(r, err)
		httpresp.WriteFeatureNotEnabled(w, chromeAbsentMsg)
		return true
	case errors.Is(err, pdfrender.ErrRenderDeadline):
		msg = "PDF rendering unavailable: render deadline exceeded, retry shortly"
	case errors.Is(err, pdfrender.ErrQueueSaturated):
		msg = "PDF rendering busy: too many concurrent renders, retry shortly"
	default:
		return false
	}
	logPDFDegradation(r, err)
	w.Header().Set("Retry-After", strconv.Itoa(pdfrender.RetryAfterSeconds))
	httpresp.WriteError(w, http.StatusServiceUnavailable, msg)
	return true
}

// logPDFDegradation emits a WARN distinguishing the degradation mode (slice 475
// AC-5) so the three modes are searchable in logs.
func logPDFDegradation(r *http.Request, err error) {
	slog.WarnContext(r.Context(), "pdf render degraded",
		"artifact", "walkthrough",
		"reason", pdfDegradationReason(err),
		"path", r.URL.Path,
	)
}

func pdfDegradationReason(err error) string {
	switch {
	case errors.Is(err, walkthrough.ErrChromeUnavailable):
		return "chrome_absent"
	case errors.Is(err, pdfrender.ErrRenderDeadline):
		return "render_deadline_exceeded"
	case errors.Is(err, pdfrender.ErrQueueSaturated):
		return "render_queue_saturated"
	default:
		return "unknown"
	}
}
