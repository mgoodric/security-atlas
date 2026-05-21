package questionnaires

import (
	"context"

	"github.com/mgoodric/security-atlas/internal/questionnaire"
)

// contextWithPDFDeadline derives a child context with the PDF render
// timeout (mirrors internal/board/pdf usage).
func contextWithPDFDeadline(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, questionnaire.PDFTimeout)
}
