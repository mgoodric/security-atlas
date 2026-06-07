package board

import (
	"errors"
	"log/slog"
	"net/http"

	board "github.com/mgoodric/security-atlas/internal/board"
	"github.com/mgoodric/security-atlas/internal/pdfrender"
)

// pdfDegradation classifies a PDF-render error into the graceful 503 path,
// returning the HTTP status, a client-safe message, and ok=true when the
// error is one of the three documented degradation modes. A non-degradation
// error (a genuine bug) returns ok=false so the caller falls through to a 500
// (slice 475 AC-1).
//
// The three modes — chrome absent, render deadline exceeded, render queue
// saturated — ALL map to 503 (the already-documented graceful path) so a
// slow/contended/missing chrome never produces a 500 or a hung request. The
// distinct messages let the operator tell the modes apart from the client
// side; the WARN log (logPDFDegradation) carries the same distinction
// server-side (AC-5).
func pdfDegradation(err error) (status int, msg string, ok bool) {
	switch {
	case errors.Is(err, board.ErrChromeUnavailable):
		return http.StatusServiceUnavailable,
			"PDF rendering unavailable: chrome browser not found", true
	case errors.Is(err, pdfrender.ErrRenderDeadline):
		return http.StatusServiceUnavailable,
			"PDF rendering unavailable: render deadline exceeded, retry shortly", true
	case errors.Is(err, pdfrender.ErrQueueSaturated):
		return http.StatusServiceUnavailable,
			"PDF rendering busy: too many concurrent renders, retry shortly", true
	default:
		return 0, "", false
	}
}

// logPDFDegradation emits a WARN distinguishing the degradation mode (AC-5).
// kind is the artifact ("board-brief" / "board-pack"); the reason string is
// derived from the typed error so the three modes are searchable in logs.
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
