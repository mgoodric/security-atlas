// pack_pdf.go — renders a quarterly board pack to a PDF byte stream via
// headless Chrome (chromedp). AC-6: "Output formats: PDF + Markdown".
//
// This reuses the EXISTING chromedp render path established by slice 022 /
// slice 031 — chromedp is already a go.mod dependency; this slice adds NO
// new dependency. The browser-allocation logic is shared with the slice-031
// brief renderer (pdf.go): connect to CHROME_DEBUG_URL when set, otherwise
// launch a local headless Chrome.
//
// The PDF is rendered ON DEMAND from the stored `content` + `narrative_md` —
// it is NOT stored. The render is deterministic, so a re-fetch produces the
// same document. A draft pack renders too (the operator previews before
// publish); a published pack renders the frozen content.
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

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	"github.com/mgoodric/security-atlas/internal/pdfrender"
)

// Note: chromedpWSURLReadTimeout is declared in pdf.go (same package).
// Slice 341 fans the slice 340 fix across packages; within package board
// both renderers share the single declaration.

// RenderPackPDF returns a PDF byte stream for the stored pack. The returned
// bytes begin with the literal `%PDF-` magic header. Blocking; caller
// supplies the timeout via ctx. Returns ErrChromeUnavailable (shared with
// the slice-031 brief renderer) when chromedp could not launch a browser —
// the HTTP handler maps that to 503.
func RenderPackPDF(ctx context.Context, sp StoredPack) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("board/pack-pdf: nil context")
	}
	htmlDoc := buildPackHTML(sp)
	return pdfrender.Default().Do(ctx, func(ctx context.Context) ([]byte, error) {
		return renderPackBytes(ctx, htmlDoc)
	})
}

// renderPackBytes performs the chromedp render under the deadline the limiter
// has already applied to ctx.
func renderPackBytes(ctx context.Context, htmlDoc string) ([]byte, error) {
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
		return nil, fmt.Errorf("board/pack-pdf: chromedp run: %w", err)
	}
	if len(buf) < 5 || string(buf[:5]) != "%PDF-" {
		return nil, fmt.Errorf("board/pack-pdf: invalid PDF output (len=%d)", len(buf))
	}
	return buf, nil
}

// buildPackHTML renders the quarterly pack as a print-friendly HTML document
// matching the Plans/_archive/mockups/board-pack.html visual reference: a cover, then
// one numbered card per section showing the section's effective narrative
// plus its structured data. Walks SectionKeys in canonical order.
func buildPackHTML(sp StoredPack) string {
	p := sp.Content
	var w strings.Builder
	w.WriteString(`<!doctype html><html><head><meta charset="utf-8"><title>`)
	w.WriteString(html.EscapeString("Quarterly Board Pack " + p.PeriodEnd))
	w.WriteString(`</title><style>
body { font-family: -apple-system, "Helvetica Neue", Arial, sans-serif; color: #0f172a; line-height: 1.55; max-width: 820px; margin: 0 auto; padding: 28px; }
h1 { font-size: 26pt; letter-spacing: -0.01em; margin-bottom: 4px; }
.cover-sub { color: #475569; font-size: 11pt; margin-bottom: 6px; }
.status-draft { display: inline-block; background: #fffbeb; color: #b45309; font-size: 8pt; font-weight: 700; text-transform: uppercase; letter-spacing: 0.06em; padding: 2px 8px; border-radius: 4px; }
.status-published { display: inline-block; background: #ecfdf5; color: #047857; font-size: 8pt; font-weight: 700; text-transform: uppercase; letter-spacing: 0.06em; padding: 2px 8px; border-radius: 4px; }
section { border: 1px solid #e2e8f0; border-radius: 14px; padding: 22px 24px; margin: 18px 0; }
.sec-num { font-family: ui-monospace, Menlo, monospace; font-size: 8pt; color: #94a3b8; }
h2 { font-size: 15pt; margin: 2px 0 12px; }
.narrative { font-size: 10.5pt; color: #1e293b; margin-bottom: 12px; }
.approved { color: #047857; font-size: 8pt; font-weight: 700; text-transform: uppercase; letter-spacing: 0.05em; }
.unapproved { color: #b45309; font-size: 8pt; font-weight: 700; text-transform: uppercase; letter-spacing: 0.05em; }
table { width: 100%; border-collapse: collapse; margin: 8px 0; font-size: 9.5pt; }
th, td { text-align: left; border-bottom: 1px solid #e5e7eb; padding: 5px 8px; }
th { background: #f8fafc; font-weight: 600; }
.grid { display: flex; flex-wrap: wrap; gap: 10px; margin: 8px 0; }
.card { border: 1px solid #e2e8f0; border-radius: 8px; padding: 9px 12px; min-width: 140px; }
.card .label { font-size: 8pt; text-transform: uppercase; letter-spacing: 0.04em; color: #64748b; }
.card .pct { font-size: 17pt; font-weight: 600; }
.muted { color: #64748b; font-size: 9.5pt; }
</style></head><body>`)

	// Cover.
	statusClass := "status-draft"
	if p.Status == PackStatusPublished {
		statusClass = "status-published"
	}
	fmt.Fprintf(&w, `<h1>Quarterly Board Pack — %s</h1>`, html.EscapeString(p.PeriodEnd))
	fmt.Fprintf(&w, `<div class="cover-sub">Generated %s</div>`, html.EscapeString(p.GeneratedAt))
	fmt.Fprintf(&w, `<span class="%s">%s</span>`, statusClass, html.EscapeString(p.Status))
	if sp.IsPublished() && sp.PublishedBy != "" {
		fmt.Fprintf(&w, ` <span class="muted">published by %s</span>`, html.EscapeString(sp.PublishedBy))
	}

	// One numbered card per section.
	for i, key := range SectionKeys {
		sec := p.Sections[key]
		w.WriteString(`<section>`)
		fmt.Fprintf(&w, `<div class="sec-num">§ %02d</div>`, i+1)
		fmt.Fprintf(&w, `<h2>%s</h2>`, html.EscapeString(sectionTitles[key]))
		approvedClass, approvedLabel := "unapproved", "not approved"
		if sec.Approved {
			approvedClass, approvedLabel = "approved", "approved"
		}
		fmt.Fprintf(&w, `<div class="%s">%s</div>`, approvedClass, approvedLabel)
		fmt.Fprintf(&w, `<div class="narrative">%s</div>`, html.EscapeString(sec.EffectiveText()))
		writeSectionData(&w, key, sec.Data)
		w.WriteString(`</section>`)
	}

	w.WriteString(`</body></html>`)
	return w.String()
}

// writeSectionData renders the structured payload for one section beneath
// its narrative — the per-section tables / stat grids that match the mockup.
func writeSectionData(w *strings.Builder, key string, d SectionData) {
	switch key {
	case SectionPosture:
		if len(d.Frameworks) == 0 {
			return
		}
		w.WriteString(`<div class="grid">`)
		for _, fp := range d.Frameworks {
			fmt.Fprintf(w,
				`<div class="card"><div class="label">%s</div><div class="pct">%d%%</div>`+
					`<div class="muted">%s %s pts · %s</div></div>`,
				html.EscapeString(fp.Name), fp.CoveragePct,
				html.EscapeString(arrowGlyph(fp.TrendArrow)),
				html.EscapeString(signedInt(fp.Delta)), html.EscapeString(fp.State))
		}
		w.WriteString(`</div>`)

	case SectionTopRisks:
		if len(d.TopRisks) == 0 {
			return
		}
		w.WriteString(`<table><thead><tr><th>#</th><th>Risk</th><th>Category</th>` +
			`<th>Treatment</th><th>Residual severity</th><th>Age (days)</th></tr></thead><tbody>`)
		for i, r := range d.TopRisks {
			fmt.Fprintf(w,
				`<tr><td>%d</td><td>%s</td><td>%s</td><td>%s</td><td>%.1f</td><td>%d</td></tr>`,
				i+1, html.EscapeString(r.Title), html.EscapeString(r.Category),
				html.EscapeString(r.Treatment), r.ResidualSeverity, r.AgeDays)
		}
		w.WriteString(`</tbody></table>`)

	case SectionCoverageTrend:
		fmt.Fprintf(w,
			`<div class="muted">Coverage %d%% · baseline %d%% · delta %s pts</div>`,
			d.CoveragePct, d.BaselineCoveragePct, signedInt(d.CoverageDelta))

	case SectionOpenFindings:
		if len(d.Findings) == 0 {
			w.WriteString(`<div class="muted">No open findings.</div>`)
			return
		}
		w.WriteString(`<table><thead><tr><th>#</th><th>Control</th><th>Scope cell</th>` +
			`<th>Evaluated</th><th>Freshness</th></tr></thead><tbody>`)
		for i, f := range d.Findings {
			fmt.Fprintf(w,
				`<tr><td>%d</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>`,
				i+1, html.EscapeString(f.ControlID), html.EscapeString(f.ScopeCellID),
				html.EscapeString(f.EvaluatedAt), html.EscapeString(f.FreshnessStatus))
		}
		w.WriteString(`</tbody></table>`)

	case SectionVendorBurndown:
		// Slice 273: render the three scalars + on-time percent as a
		// stat grid matching the slice-031 framework-posture card density.
		// No table — the section is intentionally a small card.
		if d.VendorBurndownTotal == 0 {
			w.WriteString(`<div class="muted">No high-criticality vendors registered.</div>`)
			return
		}
		w.WriteString(`<div class="grid">`)
		fmt.Fprintf(w,
			`<div class="card"><div class="label">High-criticality vendors</div><div class="pct">%d</div></div>`,
			d.VendorBurndownTotal)
		fmt.Fprintf(w,
			`<div class="card"><div class="label">Reviewed on time</div><div class="pct">%d</div>`+
				`<div class="muted">%d%% of total</div></div>`,
			d.VendorBurndownOnTime, d.VendorBurndownOnTimePct)
		fmt.Fprintf(w,
			`<div class="card"><div class="label">Past due</div><div class="pct">%d</div></div>`,
			d.VendorBurndownPastDue)
		w.WriteString(`</div>`)

	case SectionOperational:
		w.WriteString(`<table><thead><tr><th>Metric</th><th>Value</th></tr></thead><tbody>`)
		writeOptIntRow(w, "Phishing pass rate (%)", d.PhishingPassRatePct)
		writeOptIntRow(w, "P1 patch median (days)", d.P1PatchMedianDays)
		writeOptIntRow(w, "Incident count", d.IncidentCount)
		writeOptIntRow(w, "Vendor reviews on time", d.VendorReviewsOnTime)
		writeOptIntRow(w, "Vendor reviews total", d.VendorReviewsTotal)
		w.WriteString(`</tbody></table>`)

	case SectionInvestment:
		fmt.Fprintf(w,
			`<div class="muted">Spend $%d · coverage delta %s pts · ~$%.0f per coverage point</div>`,
			d.SpendUSD, signedInt(d.CoverageDelta), d.CostPerCoveragePoint)
	}
}

// writeOptIntRow writes one operator-metric table row, showing "not entered"
// when the operator has not supplied a value (decision D3 — never a
// fabricated number).
func writeOptIntRow(w *strings.Builder, label string, v *int) {
	val := "not entered"
	if v != nil {
		val = fmt.Sprintf("%d", *v)
	}
	fmt.Fprintf(w, `<tr><td>%s</td><td>%s</td></tr>`, html.EscapeString(label), html.EscapeString(val))
}
