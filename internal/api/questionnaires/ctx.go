package questionnaires

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/mgoodric/security-atlas/internal/pdfrender"
	"github.com/mgoodric/security-atlas/internal/questionnaire"
)

// pdfDegradation classifies a questionnaire PDF-render error into the graceful
// 503 path (slice 475). The three modes — chrome absent, render deadline
// exceeded, render queue saturated — ALL map to 503 so a slow/contended/
// missing chrome never produces a 500 or a hung request (AC-1). ok=false for a
// genuine bug so the caller falls through to a 500.
func pdfDegradation(err error) (status int, msg string, ok bool) {
	switch {
	case errors.Is(err, questionnaire.ErrChromeUnavailable):
		return http.StatusServiceUnavailable,
			"PDF export disabled: chrome not available in this deployment", true
	case errors.Is(err, pdfrender.ErrRenderDeadline):
		return http.StatusServiceUnavailable,
			"PDF export unavailable: render deadline exceeded, retry shortly", true
	case errors.Is(err, pdfrender.ErrQueueSaturated):
		return http.StatusServiceUnavailable,
			"PDF export busy: too many concurrent renders, retry shortly", true
	default:
		return 0, "", false
	}
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
