// Slice 155 — PDF export of a populated questionnaire.
//
// Reuses the chromedp render path established by internal/board/pdf.go
// (slice 022/027/137 precedent). Zero new go.mod dependencies for PDF.
// The structured render is intentionally minimal: header + question /
// answer pairs in a single print-friendly column.
//
// Like the board PDF, this is rendered ON DEMAND from the live record;
// the bytes are never persisted. A re-render produces the same document
// given the same source rows (chromedp output is deterministic enough
// for the audit-trace use case — the source rows are the immutable
// record).
package questionnaire

import (
	"context"
	"errors"
	"fmt"
	"html"
	"os"
	"strings"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	"github.com/mgoodric/security-atlas/internal/pdfrender"
)

// PDFTimeout is retained as a compatibility alias. The authoritative bounded,
// env-tunable render deadline now lives on the shared pdfrender.Limiter
// (slice 475); RenderPDF routes through pdfrender.Default().
//
// Deprecated: use pdfrender.Default().RenderTimeout().
const PDFTimeout = pdfrender.DefaultRenderTimeout

// chromedpWSURLReadTimeout overrides chromedp's hardcoded 20s
// wsURLReadTimeout watchdog (see chromedp v0.15.1 allocate.go:249).
// Slice 340 diagnosed this watchdog firing on Harden-Runner audit-
// mode-stretched Chrome startup; this slice fans the fix out to
// the four sibling PDF renderers. See slice 341.
const chromedpWSURLReadTimeout = 60 * time.Second

// ErrChromeUnavailable signals the headless browser isn't available
// in this environment. The HTTP handler maps this to 503 so operators
// can run the platform without Chrome (PDF export disabled) until they
// install it — the questionnaire workflow itself still works. Mirrors
// internal/board/pdf.go.
var ErrChromeUnavailable = errors.New("questionnaire/pdf: chrome browser unavailable")

// PDFInput is the structured snapshot the renderer turns into bytes.
// Callers (the HTTP handler) hydrate this from the DB via the standard
// queries.
type PDFInput struct {
	QuestionnaireName string
	SourceLabel       string
	GeneratedAt       string
	Items             []PDFItem
}

// PDFItem is one question / answer pair on the rendered page.
type PDFItem struct {
	Code         string
	Text         string
	Domain       string
	ScfAnchorID  string
	AnswerValue  string
	Narrative    string
	NeedsMapping bool
}

// RenderPDF returns a PDF byte stream for the questionnaire. Bytes
// begin with the literal `%PDF-` magic header. Blocking; caller
// supplies the timeout via ctx.
func RenderPDF(ctx context.Context, in PDFInput) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("questionnaire/pdf: nil context")
	}
	htmlDoc := buildHTML(in)
	return pdfrender.Default().Do(ctx, func(ctx context.Context) ([]byte, error) {
		return renderQuestionnaireBytes(ctx, htmlDoc)
	})
}

// renderQuestionnaireBytes performs the chromedp render under the deadline the
// limiter has already applied to ctx.
func renderQuestionnaireBytes(ctx context.Context, htmlDoc string) ([]byte, error) {
	var browserCtx context.Context
	var cancelAlloc context.CancelFunc
	var cancelBrowser context.CancelFunc
	if remote := os.Getenv("CHROME_DEBUG_URL"); remote != "" {
		var allocCtx context.Context
		allocCtx, cancelAlloc = chromedp.NewRemoteAllocator(ctx, remote)
		browserCtx, cancelBrowser = chromedp.NewContext(allocCtx)
	} else {
		opts := append(
			chromedp.DefaultExecAllocatorOptions[:],
			chromedp.NoSandbox,
			chromedp.DisableGPU,
			chromedp.Headless,
			chromedp.Flag("hide-scrollbars", true),
			chromedp.WSURLReadTimeout(chromedpWSURLReadTimeout),
		)
		var allocCtx context.Context
		allocCtx, cancelAlloc = chromedp.NewExecAllocator(ctx, opts...)
		browserCtx, cancelBrowser = chromedp.NewContext(allocCtx)
	}
	defer cancelBrowser()
	defer cancelAlloc()

	// data: URL only — we never navigate to an untrusted URL. The HTML
	// we render is entirely server-side templated against escaped DB
	// data, so XSS / SSRF risk is bounded by the renderer interface.
	dataURL := "data:text/html;charset=utf-8," + encodeForDataURL(htmlDoc)
	var buf []byte
	err := chromedp.Run(browserCtx,
		chromedp.Navigate(dataURL),
		chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			buf, _, err = page.PrintToPDF().
				WithPrintBackground(true).
				WithMarginTop(0.4).
				WithMarginBottom(0.4).
				WithMarginLeft(0.4).
				WithMarginRight(0.4).
				Do(ctx)
			return err
		}),
	)
	if err != nil {
		if isChromeUnavailable(err) {
			return nil, fmt.Errorf("%w: %v", ErrChromeUnavailable, err)
		}
		return nil, fmt.Errorf("questionnaire/pdf: chromedp run: %w", err)
	}
	if len(buf) < 5 || string(buf[:5]) != "%PDF-" {
		return nil, fmt.Errorf("questionnaire/pdf: invalid PDF output (len=%d)", len(buf))
	}
	return buf, nil
}

func buildHTML(in PDFInput) string {
	var w strings.Builder
	w.WriteString(`<!doctype html><html><head><meta charset="utf-8"><title>`)
	w.WriteString(html.EscapeString("Questionnaire — " + in.QuestionnaireName))
	w.WriteString(`</title><style>
body { font-family: -apple-system, "Helvetica Neue", Arial, sans-serif; color: #111; line-height: 1.5; max-width: 760px; margin: 0 auto; padding: 24px; }
h1 { font-size: 20pt; border-bottom: 2px solid #333; padding-bottom: 8px; margin-bottom: 4px; }
.sub { color: #555; font-size: 10pt; margin-bottom: 18px; }
.item { border-top: 1px solid #ddd; padding: 12px 0; page-break-inside: avoid; }
.code { font-family: ui-monospace, Menlo, monospace; font-size: 9pt; color: #555; }
.qtext { font-size: 11pt; font-weight: 600; margin: 2px 0 6px 0; }
.meta { font-size: 9pt; color: #666; margin-bottom: 6px; }
.answer { font-size: 10pt; }
.value { display: inline-block; padding: 2px 6px; background: #ecfdf5; color: #065f46; border-radius: 4px; font-weight: 600; font-size: 9pt; margin-right: 6px; }
.unmapped { color: #92400e; font-style: italic; font-size: 9pt; }
.unanswered { color: #6b7280; font-style: italic; font-size: 9pt; }
</style></head><body>`)

	fmt.Fprintf(&w, `<h1>%s</h1>`, html.EscapeString(in.QuestionnaireName))
	fmt.Fprintf(&w, `<div class="sub">%s · generated %s</div>`,
		html.EscapeString(in.SourceLabel), html.EscapeString(in.GeneratedAt))

	for _, it := range in.Items {
		w.WriteString(`<div class="item">`)
		fmt.Fprintf(&w, `<div class="code">%s`, html.EscapeString(it.Code))
		if it.Domain != "" {
			fmt.Fprintf(&w, ` · %s`, html.EscapeString(it.Domain))
		}
		w.WriteString(`</div>`)
		fmt.Fprintf(&w, `<div class="qtext">%s</div>`, html.EscapeString(it.Text))

		if it.NeedsMapping {
			w.WriteString(`<div class="unmapped">needs manual SCF mapping</div>`)
		} else if it.ScfAnchorID != "" {
			fmt.Fprintf(&w, `<div class="meta">SCF anchor: <span class="code">%s</span></div>`,
				html.EscapeString(it.ScfAnchorID))
		}

		hasAnswer := it.AnswerValue != "" || it.Narrative != ""
		if hasAnswer {
			w.WriteString(`<div class="answer">`)
			if it.AnswerValue != "" {
				fmt.Fprintf(&w, `<span class="value">%s</span>`, html.EscapeString(it.AnswerValue))
			}
			if it.Narrative != "" {
				w.WriteString(html.EscapeString(it.Narrative))
			}
			w.WriteString(`</div>`)
		} else {
			w.WriteString(`<div class="unanswered">(unanswered)</div>`)
		}

		w.WriteString(`</div>`)
	}

	w.WriteString(`</body></html>`)
	return w.String()
}

// encodeForDataURL — same minimal set as internal/board/pdf.
func encodeForDataURL(s string) string {
	r := strings.NewReplacer(
		"%", "%25",
		"#", "%23",
		"&", "%26",
		"?", "%3F",
		"\n", "%0A",
		"\r", "",
	)
	return r.Replace(s)
}

// isChromeUnavailable — same heuristic as internal/board/pdf.
func isChromeUnavailable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "exec:") ||
		strings.Contains(msg, "executable file not found") ||
		strings.Contains(msg, "no chrome found")
}
