// Package pdf renders a Policy markdown body to a PDF byte stream via
// headless Chrome (chromedp). AC-5: "PDF render of a policy via GET
// /v1/policies/:id/pdf".
//
// The render path is intentionally minimal: markdown → simple HTML →
// chrome PrintToPDF. We do NOT pull in a full-fat markdown library
// (goldmark / blackfriday) for v1 — the stock policies are simple
// (headings, paragraphs, lists). If a tenant later authors a policy
// with complex markdown we can graduate to goldmark without breaking
// the public surface; the render function shape stays stable.
//
// PDF correctness is asserted by the integration test via the leading
// magic bytes `%PDF-`. The render path is NOT a stub.
package pdf

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

// Doc is the data passed to the renderer. Pure-content; the caller
// supplies whatever it has from the Policy row.
type Doc struct {
	Title         string
	Version       string
	EffectiveDate string // YYYY-MM-DD; empty if not yet published
	OwnerRole     string
	ApproverRole  string
	Status        string
	BodyMd        string
}

// DefaultTimeout is the wall-clock budget for a single render. Headless
// Chrome boot + PrintToPDF on a 1-page policy is typically <2s on a
// developer laptop, but the GitHub Actions ubuntu-latest runner with
// StepSecurity Harden-Runner audit-mode egress instrumentation (slice 117)
// can take 20-40s to print its DevTools websocket URL on a cold start
// because Chrome's startup network calls (component-updater, GPU blocklist
// refresh) traverse the audited egress hook. The 90s budget = 60s WS-URL
// read window (see chromedpWSURLReadTimeout below) + 30s render headroom.
// Slice 340 raised this from the original 30s after diagnosing the
// `--- FAIL: TestRender_ProducesRealPDF (20.04s)` chromedp websocket-url
// timeout flake (5 consecutive CI failures across slices 312/315/320).
const DefaultTimeout = 90 * time.Second

// chromedpWSURLReadTimeout overrides chromedp's hardcoded 20s
// "wait for Chrome to print `DevTools listening on ws://...` to stderr"
// timer. The default fires at 20.0s flat in CI when Chrome's startup is
// stretched by Harden-Runner audit-mode latency (slice 117) — see
// internal slice 340 decisions log for the diagnosis trail. 60s is the
// canonical "make chromedp work in CI containers" value used by
// upstream chromedp examples. Local fast-path renders are unaffected
// because Chrome prints the WS URL in <1s on a warm machine.
const chromedpWSURLReadTimeout = 60 * time.Second

// ErrChromeUnavailable is returned when chromedp could not launch a
// browser. The HTTP handler maps this to 503 so operators can run the
// platform without Chrome (PDF endpoint disabled) until they install it.
var ErrChromeUnavailable = errors.New("policy/pdf: chrome browser unavailable")

// Render returns a PDF byte stream for the supplied document. The
// returned bytes begin with the literal `%PDF-` magic header. Blocking;
// caller supplies the timeout via ctx.
func Render(ctx context.Context, doc Doc) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("policy/pdf: nil context")
	}
	htmlDoc := buildHTML(doc)
	// Browser allocation has two paths:
	//
	//   1. CHROME_DEBUG_URL set → connect to a remote Chrome DevTools
	//      Protocol endpoint (typically a `chromedp/headless-shell`
	//      container exposed on port 9222). Used by CI and by local dev
	//      machines without a Chrome install.
	//   2. Otherwise → launch a local Chrome via ExecAllocator with
	//      --no-sandbox so it runs in CI containers as well as locally.
	//
	// We never load untrusted URLs; only the inline data: URL we
	// construct ourselves.
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
			// Slice 340: extend chromedp's hardcoded 20s "wait for
			// DevTools websocket URL" watchdog to 60s. Without this,
			// the GitHub Actions runner with Harden-Runner audit-mode
			// (slice 117) flakes at exactly 20.04s in 4-of-5 runs.
			// Local renders complete in <1s and are unaffected.
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
		// Heuristic: any failure to launch the browser executable
		// returns an error matching "exec:" or "executable file not
		// found". Surface as ErrChromeUnavailable so callers can
		// distinguish missing-dependency from real render failures.
		if isChromeUnavailable(err) {
			return nil, fmt.Errorf("%w: %v", ErrChromeUnavailable, err)
		}
		return nil, fmt.Errorf("policy/pdf: chromedp run: %w", err)
	}
	if len(buf) < 5 || string(buf[:5]) != "%PDF-" {
		return nil, fmt.Errorf("policy/pdf: invalid PDF output (len=%d, prefix=%q)", len(buf), safePrefix(buf))
	}
	return buf, nil
}

// buildHTML renders the policy as a minimal HTML document. Intentional
// simplicity: heading + metadata table + markdown body. CSS keeps the
// output A4-print-friendly.
func buildHTML(doc Doc) string {
	var b strings.Builder
	b.WriteString(`<!doctype html><html><head><meta charset="utf-8"><title>`)
	b.WriteString(html.EscapeString(doc.Title))
	b.WriteString(`</title><style>
body { font-family: -apple-system, "Helvetica Neue", Arial, sans-serif; color: #111; line-height: 1.5; max-width: 720px; margin: 0 auto; padding: 24px; }
h1 { font-size: 22pt; border-bottom: 2px solid #333; padding-bottom: 8px; }
h2 { font-size: 16pt; margin-top: 28px; }
h3 { font-size: 13pt; margin-top: 20px; }
.metadata { width: 100%; border-collapse: collapse; margin: 16px 0 24px; font-size: 10pt; }
.metadata th, .metadata td { text-align: left; border-bottom: 1px solid #ddd; padding: 6px 8px; }
.metadata th { background: #f5f5f5; width: 30%; font-weight: 600; }
.body { font-size: 11pt; }
.body p { margin: 10px 0; }
.body ul, .body ol { margin: 10px 0 10px 24px; }
.body code { background: #f3f3f3; padding: 1px 4px; border-radius: 3px; font-family: ui-monospace, monospace; }
.status { display: inline-block; padding: 2px 8px; border-radius: 3px; font-size: 9pt; text-transform: uppercase; }
.status-draft { background: #eee; color: #555; }
.status-under_review { background: #fef3c7; color: #92400e; }
.status-approved { background: #dbeafe; color: #1e40af; }
.status-published { background: #d1fae5; color: #065f46; }
.status-superseded { background: #e5e7eb; color: #374151; }
</style></head><body>`)
	b.WriteString(`<h1>`)
	b.WriteString(html.EscapeString(doc.Title))
	b.WriteString(`</h1>`)
	b.WriteString(`<table class="metadata"><tbody>`)
	fmt.Fprintf(&b, `<tr><th>Version</th><td>%s</td></tr>`, html.EscapeString(doc.Version))
	if doc.EffectiveDate != "" {
		fmt.Fprintf(&b, `<tr><th>Effective date</th><td>%s</td></tr>`, html.EscapeString(doc.EffectiveDate))
	}
	fmt.Fprintf(&b, `<tr><th>Owner role</th><td>%s</td></tr>`, html.EscapeString(doc.OwnerRole))
	fmt.Fprintf(&b, `<tr><th>Approver role</th><td>%s</td></tr>`, html.EscapeString(doc.ApproverRole))
	statusClass := "status-" + strings.ReplaceAll(doc.Status, " ", "_")
	fmt.Fprintf(&b, `<tr><th>Status</th><td><span class="status %s">%s</span></td></tr>`,
		html.EscapeString(statusClass), html.EscapeString(doc.Status))
	b.WriteString(`</tbody></table>`)
	b.WriteString(`<div class="body">`)
	b.WriteString(renderMarkdown(doc.BodyMd))
	b.WriteString(`</div></body></html>`)
	return b.String()
}

// renderMarkdown is a deliberately minimal line-based markdown converter.
// Supports: H1-H4 (#-####), unordered lists (-, *), ordered lists (1.),
// inline `code`, blank-line paragraph breaks. Anything else passes
// through escaped.
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
		// Heading
		if h := headingLevel(trimmed); h > 0 {
			closeBlocks()
			text := strings.TrimSpace(trimmed[h:])
			fmt.Fprintf(&b, "<h%d>%s</h%d>", h, renderInline(text), h)
			continue
		}
		// Unordered list
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			if !inUL {
				closeBlocks()
				b.WriteString("<ul>")
				inUL = true
			}
			b.WriteString("<li>" + renderInline(trimmed[2:]) + "</li>")
			continue
		}
		// Ordered list (1. style)
		if idx := strings.Index(trimmed, ". "); idx > 0 && idx <= 3 && isAllDigits(trimmed[:idx]) {
			if !inOL {
				closeBlocks()
				b.WriteString("<ol>")
				inOL = true
			}
			b.WriteString("<li>" + renderInline(trimmed[idx+2:]) + "</li>")
			continue
		}
		// Paragraph line
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

// renderInline handles `inline code` only. Everything else is HTML-escaped.
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

// encodeForDataURL percent-encodes the small set of characters that would
// confuse chromedp's data-URL parser. Full URL encoding is overkill; only
// `#`, `%`, and `&` (and the quote chars) need attention because
// `data:text/html;charset=utf-8,` introduces the inline document.
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
