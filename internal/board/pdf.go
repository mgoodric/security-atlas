// pdf.go — renders a frozen board brief to a PDF byte stream via headless
// Chrome (chromedp). AC-4: "Output formats: PDF + Markdown".
//
// This reuses the EXISTING chromedp render path established by
// internal/policy/pdf (slice 022) — chromedp is already a go.mod
// dependency; this slice adds NO new dependency. The render path is
// intentionally minimal: structured Brief -> simple HTML -> chrome
// PrintToPDF.
//
// The PDF is rendered ON DEMAND from the frozen `content` + `narrative_md`
// (decisions log D5) — it is NOT stored. The render is deterministic, so a
// re-fetch produces the same document; storing the bytes would bloat the
// row for no correctness gain.
//
// PDF correctness is asserted by the integration test via the leading magic
// bytes `%PDF-`. The render path is NOT a stub.
package board

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
)

// PDFTimeout is the wall-clock budget for a single render. Headless Chrome
// boot + PrintToPDF on a one-page brief is typically <2s; 30s is generous.
// Mirrors internal/policy/pdf.DefaultTimeout.
const PDFTimeout = 30 * time.Second

// ErrChromeUnavailable is returned when chromedp could not launch a browser.
// The HTTP handler maps this to 503 so operators can run the platform
// without Chrome (PDF endpoint disabled) until they install it — the brief
// + Markdown still work. Mirrors internal/policy/pdf.ErrChromeUnavailable.
var ErrChromeUnavailable = errors.New("board/pdf: chrome browser unavailable")

// RenderPDF returns a PDF byte stream for the frozen brief. The returned
// bytes begin with the literal `%PDF-` magic header. Blocking; caller
// supplies the timeout via ctx.
func RenderPDF(ctx context.Context, sb StoredBrief) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("board/pdf: nil context")
	}
	htmlDoc := buildBriefHTML(sb)

	// Browser allocation mirrors internal/policy/pdf: connect to a remote
	// CDP endpoint when CHROME_DEBUG_URL is set (CI / dev machines without a
	// local Chrome), otherwise launch a local Chrome with --no-sandbox. We
	// never load untrusted URLs — only the inline data: URL we construct.
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
		)
		var allocCtx context.Context
		allocCtx, cancelAlloc = chromedp.NewExecAllocator(ctx, opts...)
		browserCtx, cancelBrowser = chromedp.NewContext(allocCtx)
	}
	defer cancelBrowser()
	defer cancelAlloc()

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
		return nil, fmt.Errorf("board/pdf: chromedp run: %w", err)
	}
	if len(buf) < 5 || string(buf[:5]) != "%PDF-" {
		return nil, fmt.Errorf("board/pdf: invalid PDF output (len=%d)", len(buf))
	}
	return buf, nil
}

// buildBriefHTML renders the frozen brief as a minimal, print-friendly HTML
// document: title + posture stat grid + drift summary + top-risks table.
// Intentional simplicity — the brief is a single page (canvas §7.5).
func buildBriefHTML(sb StoredBrief) string {
	b := sb.Content
	var w strings.Builder
	w.WriteString(`<!doctype html><html><head><meta charset="utf-8"><title>`)
	w.WriteString(html.EscapeString("Monthly Board Brief " + b.PeriodEnd))
	w.WriteString(`</title><style>
body { font-family: -apple-system, "Helvetica Neue", Arial, sans-serif; color: #111; line-height: 1.5; max-width: 760px; margin: 0 auto; padding: 24px; }
h1 { font-size: 22pt; border-bottom: 2px solid #333; padding-bottom: 8px; margin-bottom: 4px; }
.sub { color: #555; font-size: 10pt; margin-bottom: 20px; }
h2 { font-size: 14pt; margin-top: 26px; border-bottom: 1px solid #ddd; padding-bottom: 4px; }
.grid { display: flex; flex-wrap: wrap; gap: 10px; margin: 12px 0; }
.card { border: 1px solid #ddd; border-radius: 8px; padding: 10px 12px; min-width: 150px; }
.card .label { font-size: 8pt; text-transform: uppercase; letter-spacing: 0.04em; color: #777; }
.card .pct { font-size: 18pt; font-weight: 600; }
.card .meta { font-size: 9pt; color: #555; }
.state-audit-ready { color: #065f46; }
.state-in-progress { color: #92400e; }
.state-at-risk { color: #991b1b; }
table { width: 100%; border-collapse: collapse; margin: 12px 0; font-size: 10pt; }
th, td { text-align: left; border-bottom: 1px solid #e5e5e5; padding: 6px 8px; }
th { background: #f5f5f5; font-weight: 600; }
.drift { font-size: 11pt; margin: 10px 0; }
</style></head><body>`)

	fmt.Fprintf(&w, `<h1>Monthly Board Brief — %s</h1>`, html.EscapeString(b.PeriodEnd))
	fmt.Fprintf(&w, `<div class="sub">Generated %s · pinned snapshot — posture as of the report date.</div>`,
		html.EscapeString(b.GeneratedAt))

	// Posture stat grid.
	w.WriteString(`<h2>Program posture</h2><div class="grid">`)
	for _, fp := range b.Frameworks {
		stateClass := "state-" + strings.ReplaceAll(fp.State, " ", "-")
		fmt.Fprintf(&w,
			`<div class="card"><div class="label">%s</div><div class="pct">%d%%</div>`+
				`<div class="meta">%s %s pts · freshness %d%%</div>`+
				`<div class="meta %s">%s</div></div>`,
			html.EscapeString(fp.Name), fp.CoveragePct,
			html.EscapeString(arrowGlyph(fp.TrendArrow)), html.EscapeString(signedInt(fp.Delta)),
			fp.FreshnessPct,
			html.EscapeString(stateClass), html.EscapeString(fp.State))
	}
	w.WriteString(`</div>`)

	// Drift summary.
	w.WriteString(`<h2>Control drift</h2>`)
	flipped := "no controls drifted out of passing"
	if b.Drift.FlippedOutCount > 0 {
		flipped = fmt.Sprintf("%d control(s) drifted out of passing", b.Drift.FlippedOutCount)
	}
	fmt.Fprintf(&w,
		`<div class="drift">Last %d days (%s to %s): drift count %s — %s.</div>`,
		b.Drift.WindowDays, html.EscapeString(b.Drift.Since), html.EscapeString(b.Drift.Through),
		html.EscapeString(signedInt(b.Drift.Delta)), html.EscapeString(flipped))

	// Top risks table.
	w.WriteString(`<h2>Top risks aging</h2>`)
	if len(b.TopRisks) == 0 {
		w.WriteString(`<div class="drift">No open risks in the register.</div>`)
	} else {
		w.WriteString(`<table><thead><tr><th>#</th><th>Risk</th><th>Category</th>` +
			`<th>Treatment</th><th>Residual severity</th><th>Age (days)</th></tr></thead><tbody>`)
		for i, r := range b.TopRisks {
			fmt.Fprintf(&w,
				`<tr><td>%d</td><td>%s</td><td>%s</td><td>%s</td><td>%.1f</td><td>%d</td></tr>`,
				i+1, html.EscapeString(r.Title), html.EscapeString(r.Category),
				html.EscapeString(r.Treatment), r.ResidualSeverity, r.AgeDays)
		}
		w.WriteString(`</tbody></table>`)
	}

	w.WriteString(`</body></html>`)
	return w.String()
}

// encodeForDataURL percent-encodes the small set of characters that would
// confuse chromedp's data-URL parser. Mirrors internal/policy/pdf.
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

// isChromeUnavailable heuristically classifies a chromedp error as
// "browser executable missing" so the handler can 503 rather than 500.
// Mirrors internal/policy/pdf.
func isChromeUnavailable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "exec:") ||
		strings.Contains(msg, "executable file not found") ||
		strings.Contains(msg, "no chrome found")
}
