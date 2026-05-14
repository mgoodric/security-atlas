package walkthrough

// Export surface — PDF + JSON. AC-5 consumes the JSON shape for slice 030
// OSCAL assessment-results; humans consume the PDF.
//
// PDF renderer choice: chromedp reuse. The dependency is already on main
// (slice 022 policy PDF render) and a second use site here costs no
// additional supply-chain footprint vs adding a pure-Go PDF library
// (gofpdf / unidoc). The decision is documented in the PR body as the
// batch's one "spine touch dodge" -- no go.mod change required for this
// slice.

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"os"
	"strings"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// PDFTimeout is the wall-clock budget for a single render. Headless
// Chrome boot + PrintToPDF on a one-walkthrough page is typically <3s;
// 45s is generous for CI where Chrome may cold-start under contention.
const PDFTimeout = 45 * time.Second

// ErrChromeUnavailable is returned when chromedp could not launch a
// browser. Handlers map this to 503 so operators can run the platform
// without Chrome (PDF endpoint disabled) until they install it.
var ErrChromeUnavailable = errors.New("walkthrough: chrome browser unavailable")

// ExportJSON is the slice-027 export wire shape -- intentionally a
// superset of what slice 030 (OSCAL assessment-results) will consume so a
// downstream OSCAL renderer can pick what it needs without backfilling.
// All UUIDs are canonical string form; created_at is RFC-3339-nano UTC.
type ExportJSON struct {
	ID             string         `json:"id"`
	TenantID       string         `json:"tenant_id"`
	AuditPeriodID  string         `json:"audit_period_id,omitempty"`
	ControlID      string         `json:"control_id"`
	Narrative      string         `json:"narrative"`
	Transcript     string         `json:"transcript,omitempty"`
	Status         string         `json:"status"`
	CanonicalHash  string         `json:"canonical_hash"` // lowercase hex
	CreatedBy      string         `json:"created_by"`
	CreatedAt      string         `json:"created_at"`
	UpdatedAt      string         `json:"updated_at"`
	Attachments    []ExportAttach `json:"attachments"`
	TamperDetected bool           `json:"tamper_detected"`
}

// ExportAttach mirrors a walkthrough_attachments row in the export.
type ExportAttach struct {
	ID          string          `json:"id"`
	StorageKey  string          `json:"storage_key"`
	ContentType string          `json:"content_type"`
	SizeBytes   int64           `json:"size_bytes"`
	SHA256      string          `json:"sha256"`
	Annotations json.RawMessage `json:"annotations"`
	UploadedBy  string          `json:"uploaded_by"`
	UploadedAt  string          `json:"uploaded_at"`
}

// ToExportJSON renders the walkthrough's JSON export shape. The same
// function feeds both the handler's GET .../export?format=json response
// and the OSCAL bridge's slice-030 ingestion.
func ToExportJSON(w Walkthrough) ExportJSON {
	ex := ExportJSON{
		ID:             w.ID.String(),
		TenantID:       w.TenantID.String(),
		ControlID:      w.ControlID.String(),
		Narrative:      w.Narrative,
		Transcript:     w.Transcript,
		Status:         string(w.Status),
		CanonicalHash:  hex.EncodeToString(w.CanonicalHash),
		CreatedBy:      w.CreatedBy,
		CreatedAt:      w.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:      w.UpdatedAt.UTC().Format(time.RFC3339Nano),
		TamperDetected: w.TamperDetected,
		Attachments:    make([]ExportAttach, 0, len(w.Attachments)),
	}
	if w.AuditPeriodID != nil {
		ex.AuditPeriodID = w.AuditPeriodID.String()
	}
	for _, a := range w.Attachments {
		// Pass the annotations through verbatim. If a row was inserted
		// without explicit annotations, the DB default '{}' bytes will
		// flow through.
		var raw json.RawMessage = a.AnnotationsRaw
		if len(raw) == 0 {
			raw = json.RawMessage([]byte(`{}`))
		}
		ex.Attachments = append(ex.Attachments, ExportAttach{
			ID:          a.ID.String(),
			StorageKey:  a.StorageKey,
			ContentType: a.ContentType,
			SizeBytes:   a.SizeBytes,
			SHA256:      a.SHA256Hex,
			Annotations: raw,
			UploadedBy:  a.UploadedBy,
			UploadedAt:  a.UploadedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	return ex
}

// RenderPDF produces a PDF byte stream for a walkthrough. Returns bytes
// beginning with `%PDF-`. AC-5.
//
// The implementation mirrors slice 022 internal/policy/pdf/render.go: it
// builds a self-contained HTML document via the same data: URL pattern,
// then drives chromedp to PrintToPDF. No external assets are loaded; the
// renderer is sandboxable.
func RenderPDF(ctx context.Context, w Walkthrough) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("walkthrough: nil context")
	}
	htmlDoc := buildPDFHTML(w)
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
		return nil, fmt.Errorf("walkthrough: chromedp run: %w", err)
	}
	if len(buf) < 5 || string(buf[:5]) != "%PDF-" {
		return nil, fmt.Errorf("walkthrough: invalid PDF output (len=%d, prefix=%q)", len(buf), safePrefix(buf))
	}
	return buf, nil
}

// buildPDFHTML renders the walkthrough as a minimal HTML document.
// Intentionally simple: metadata table + narrative (markdown-light) +
// attachment summary. Print-friendly CSS targets A4.
func buildPDFHTML(w Walkthrough) string {
	var b strings.Builder
	b.WriteString(`<!doctype html><html><head><meta charset="utf-8"><title>Walkthrough ` + html.EscapeString(w.ID.String()) + `</title><style>
body { font-family: -apple-system, "Helvetica Neue", Arial, sans-serif; color: #111; line-height: 1.5; max-width: 720px; margin: 0 auto; padding: 24px; }
h1 { font-size: 22pt; border-bottom: 2px solid #333; padding-bottom: 8px; }
h2 { font-size: 14pt; margin-top: 28px; }
.metadata { width: 100%; border-collapse: collapse; margin: 16px 0 24px; font-size: 10pt; }
.metadata th, .metadata td { text-align: left; border-bottom: 1px solid #ddd; padding: 6px 8px; }
.metadata th { background: #f5f5f5; width: 30%; font-weight: 600; font-family: ui-monospace, monospace; }
.metadata td { font-family: ui-monospace, monospace; word-break: break-all; }
.narrative { font-size: 11pt; }
.narrative p { margin: 10px 0; }
.transcript { font-size: 10pt; background: #fafafa; border-left: 3px solid #ccc; padding: 10px 14px; margin: 12px 0; white-space: pre-wrap; }
.attachments { font-size: 10pt; margin-top: 24px; }
.attachments th, .attachments td { text-align: left; border-bottom: 1px solid #eee; padding: 4px 8px; vertical-align: top; }
.attachments th { background: #f5f5f5; }
.status { display: inline-block; padding: 2px 8px; border-radius: 3px; font-size: 9pt; text-transform: uppercase; }
.status-draft { background: #eee; color: #555; }
.status-finalized { background: #d1fae5; color: #065f46; }
.tamper { background: #fee2e2; color: #991b1b; padding: 12px; border-radius: 4px; margin: 16px 0; font-weight: 600; }
.hash { font-family: ui-monospace, monospace; font-size: 9pt; word-break: break-all; }
</style></head><body>`)
	b.WriteString(`<h1>Walkthrough</h1>`)
	if w.TamperDetected {
		b.WriteString(`<div class="tamper">TAMPER DETECTED — stored hash does not match recomputed hash. Audit-binding the export below is not advised until the cause is investigated.</div>`)
	}
	b.WriteString(`<table class="metadata"><tbody>`)
	fmt.Fprintf(&b, `<tr><th>Walkthrough ID</th><td>%s</td></tr>`, html.EscapeString(w.ID.String()))
	fmt.Fprintf(&b, `<tr><th>Control ID</th><td>%s</td></tr>`, html.EscapeString(w.ControlID.String()))
	if w.AuditPeriodID != nil {
		fmt.Fprintf(&b, `<tr><th>Audit period</th><td>%s</td></tr>`, html.EscapeString(w.AuditPeriodID.String()))
	} else {
		b.WriteString(`<tr><th>Audit period</th><td><em>live (no period pin)</em></td></tr>`)
	}
	statusClass := "status-" + string(w.Status)
	fmt.Fprintf(&b, `<tr><th>Status</th><td><span class="status %s">%s</span></td></tr>`,
		html.EscapeString(statusClass), html.EscapeString(string(w.Status)))
	fmt.Fprintf(&b, `<tr><th>Created by</th><td>%s</td></tr>`, html.EscapeString(w.CreatedBy))
	fmt.Fprintf(&b, `<tr><th>Created at</th><td>%s</td></tr>`, html.EscapeString(w.CreatedAt.UTC().Format(time.RFC3339)))
	fmt.Fprintf(&b, `<tr><th>Canonical hash</th><td class="hash">%s</td></tr>`, html.EscapeString(hex.EncodeToString(w.CanonicalHash)))
	b.WriteString(`</tbody></table>`)
	b.WriteString(`<h2>Narrative</h2>`)
	b.WriteString(`<div class="narrative">`)
	b.WriteString(renderMarkdown(w.Narrative))
	b.WriteString(`</div>`)
	if w.Transcript != "" {
		b.WriteString(`<h2>Transcript</h2>`)
		b.WriteString(`<div class="transcript">`)
		b.WriteString(html.EscapeString(w.Transcript))
		b.WriteString(`</div>`)
	}
	if len(w.Attachments) > 0 {
		b.WriteString(`<h2>Attachments</h2>`)
		b.WriteString(`<table class="attachments"><thead><tr><th>Storage key</th><th>Type</th><th>Size</th><th>SHA-256</th></tr></thead><tbody>`)
		for _, a := range w.Attachments {
			fmt.Fprintf(&b,
				`<tr><td class="hash">%s</td><td>%s</td><td>%d</td><td class="hash">%s</td></tr>`,
				html.EscapeString(a.StorageKey), html.EscapeString(a.ContentType), a.SizeBytes,
				html.EscapeString(a.SHA256Hex))
		}
		b.WriteString(`</tbody></table>`)
	}
	b.WriteString(`</body></html>`)
	return b.String()
}

// renderMarkdown is a deliberately minimal line-based converter that
// supports H1-H4 (#-####), unordered lists (-, *), ordered lists (1.),
// inline `code`, and blank-line paragraph breaks. Mirrors the slice-022
// policy renderer one-for-one so the look-and-feel is consistent across
// audit-export artifacts.
func renderMarkdown(src string) string {
	lines := strings.Split(src, "\n")
	var b strings.Builder
	var inUL, inOL, inPara bool

	closeBlocks := func() {
		if inUL {
			b.WriteString("</ul>")
			inUL = false
		}
		if inOL {
			b.WriteString("</ol>")
			inOL = false
		}
		if inPara {
			b.WriteString("</p>")
			inPara = false
		}
	}
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t")
		if trimmed == "" {
			closeBlocks()
			continue
		}
		if h := headingLevel(trimmed); h > 0 {
			closeBlocks()
			text := strings.TrimSpace(trimmed[h:])
			fmt.Fprintf(&b, "<h%d>%s</h%d>", h, renderInline(text), h)
			continue
		}
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			if !inUL {
				closeBlocks()
				b.WriteString("<ul>")
				inUL = true
			}
			b.WriteString("<li>" + renderInline(trimmed[2:]) + "</li>")
			continue
		}
		if idx := strings.Index(trimmed, ". "); idx > 0 && idx <= 3 && isAllDigits(trimmed[:idx]) {
			if !inOL {
				closeBlocks()
				b.WriteString("<ol>")
				inOL = true
			}
			b.WriteString("<li>" + renderInline(trimmed[idx+2:]) + "</li>")
			continue
		}
		if !inPara {
			closeBlocks()
			b.WriteString("<p>")
			inPara = true
		} else {
			b.WriteString(" ")
		}
		b.WriteString(renderInline(trimmed))
	}
	closeBlocks()
	return b.String()
}

func headingLevel(line string) int {
	for i, r := range line {
		if r != '#' {
			if i > 0 && i <= 4 && (line[i] == ' ') {
				return i
			}
			return 0
		}
		if i >= 4 {
			break
		}
	}
	return 0
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func renderInline(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '`' {
			end := strings.Index(s[i+1:], "`")
			if end >= 0 {
				code := s[i+1 : i+1+end]
				b.WriteString("<code>" + html.EscapeString(code) + "</code>")
				i = i + 1 + end + 1
				continue
			}
		}
		b.WriteString(html.EscapeString(string(s[i])))
		i++
	}
	return b.String()
}

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

func isChromeUnavailable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "exec:") ||
		strings.Contains(msg, "executable file not found") ||
		strings.Contains(msg, "no chrome found")
}

func safePrefix(b []byte) string {
	if len(b) > 16 {
		return string(b[:16])
	}
	return string(b)
}

// ----- visibility helpers (kept here to live next to the export shape) -----

// IsTamperFlagged returns true when the walkthrough's stored hash and
// the live re-hash disagree. Mirrors w.TamperDetected and is exported
// for callers that have the Walkthrough but not the Store handle.
func IsTamperFlagged(w Walkthrough) bool { return w.TamperDetected }
