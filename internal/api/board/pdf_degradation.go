package board

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	board "github.com/mgoodric/security-atlas/internal/board"
	"github.com/mgoodric/security-atlas/internal/pdfrender"
)

// chromeAbsentMsg is the refusal for a deployment with no chrome installed.
const chromeAbsentMsg = "PDF export is not enabled on this deployment: chrome is not installed"

// writePDFDegradation answers a PDF-render error that is one of the three
// documented degradation modes and reports true; a genuine bug returns false
// so the caller falls through to a 500 (slice 475 AC-1).
//
//   - chrome absent: 409 feature_not_enabled. Retrying cannot install chrome,
//     so this is a refusal about the deployment, not a server failure (#2786).
//   - render deadline exceeded, render queue saturated: 503 with Retry-After.
//     Both clear on their own once the renders ahead finish.
//
// A WARN distinguishing the mode is logged either way (slice 475 AC-5). kind
// is the artifact ("board-brief" / "board-pack").
func writePDFDegradation(w http.ResponseWriter, r *http.Request, kind string, err error) bool {
	var msg string
	switch {
	case errors.Is(err, board.ErrChromeUnavailable):
		logPDFDegradation(r, kind, err)
		httpresp.WriteFeatureNotEnabled(w, chromeAbsentMsg)
		return true
	case errors.Is(err, pdfrender.ErrRenderDeadline):
		msg = "PDF rendering unavailable: render deadline exceeded, retry shortly"
	case errors.Is(err, pdfrender.ErrQueueSaturated):
		msg = "PDF rendering busy: too many concurrent renders, retry shortly"
	default:
		return false
	}
	logPDFDegradation(r, kind, err)
	w.Header().Set("Retry-After", strconv.Itoa(pdfrender.RetryAfterSeconds))
	httpresp.WriteError(w, http.StatusServiceUnavailable, msg)
	return true
}

// logPDFDegradation emits a WARN distinguishing the degradation mode (AC-5).
// The reason string is derived from the typed error so the three modes are
// searchable in logs.
func logPDFDegradation(r *http.Request, kind string, err error) {
	slog.WarnContext(r.Context(), "pdf render degraded",
		"artifact", kind,
		"reason", pdfDegradationReason(err),
		"path", r.URL.Path,
	)
}

func pdfDegradationReason(err error) string {
	switch {
	case errors.Is(err, board.ErrChromeUnavailable):
		return "chrome_absent"
	case errors.Is(err, pdfrender.ErrRenderDeadline):
		return "render_deadline_exceeded"
	case errors.Is(err, pdfrender.ErrQueueSaturated):
		return "render_queue_saturated"
	default:
		return "unknown"
	}
}
