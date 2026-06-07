package walkthroughs

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/mgoodric/security-atlas/internal/audit/walkthrough"
	"github.com/mgoodric/security-atlas/internal/pdfrender"
)

// pdfDegradation classifies a walkthrough PDF-render error into the graceful
// 503 path (slice 477, mirroring the slice-475 board/questionnaire pattern).
// The three modes — chrome absent, render deadline exceeded, render queue
// saturated — ALL map to 503 so a slow/contended/missing chrome never produces
// a 500 or a hung request (slice 475 AC-1). A non-degradation error (a genuine
// bug) returns ok=false so the caller falls through to a 500.
//
// Before slice 477 a render that exceeded the deadline returned a wrapped
// context.DeadlineExceeded that did NOT match walkthrough.ErrChromeUnavailable,
// so it fell through to httperr.WriteInternal = 500. Routing the render through
// pdfrender.Default().Do now classifies that case as pdfrender.ErrRenderDeadline
// → 503, deterministically.
func pdfDegradation(err error) (status int, msg string, ok bool) {
	switch {
	case errors.Is(err, walkthrough.ErrChromeUnavailable):
		return http.StatusServiceUnavailable,
			"PDF rendering unavailable: chromedp browser missing", true
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
