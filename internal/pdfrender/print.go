package pdfrender

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/remote"
)

// ErrChromeUnavailable is returned when chromedp could not launch a browser.
// Every renderer's own ErrChromeUnavailable is this sentinel, so a handler's
// errors.Is check maps it to 503 and operators can run the platform without
// Chrome (PDF endpoints disabled) until they install it.
var ErrChromeUnavailable = errors.New("pdfrender: chrome browser unavailable")

// allocatorOptions are the flags for a locally launched headless Chrome.
//
// chromedp's defaults already disable background networking, sync,
// safe-browsing updates and the first-run flow. The extra flags stop the
// remaining startup calls a cold Chrome makes to Google: the component
// updater (update.googleapis.com, clients2.google.com), domain reliability
// reports, hyperlink-audit pings, optimization-guide hint fetches and media
// router discovery. On a CI runner whose egress is audited those calls
// pushed a cold render to ~20s (#1847). A data: URL render needs none of
// them.
func allocatorOptions() []chromedp.ExecAllocatorOption {
	return append(
		chromedp.DefaultExecAllocatorOptions[:],
		chromedp.NoSandbox,
		chromedp.DisableGPU,
		chromedp.Headless,
		chromedp.Flag("hide-scrollbars", true),
		chromedp.Flag("disable-component-update", true),
		chromedp.Flag("disable-domain-reliability", true),
		chromedp.Flag("no-pings", true),
		// Replaces the default's value, so it repeats chromedp's two entries.
		chromedp.Flag("disable-features", "Translate,BlinkGenPropertyTrees,OptimizationHints,MediaRouter"),
	)
}

// PrintHTML renders a self-contained HTML document to PDF bytes with
// headless Chrome. The returned bytes begin with the `%PDF-` magic header.
//
// Browser allocation has two paths:
//
//  1. CHROME_DEBUG_URL set: connect to a remote Chrome DevTools Protocol
//     endpoint (typically a `chromedp/headless-shell` container on 9222).
//  2. Otherwise: launch a local Chrome with allocatorOptions.
//
// Only the inline data: URL built here is ever loaded, never an untrusted
// URL. PrintHTML does not take a render slot; callers that serve requests
// wrap it in Default().Do so the shared concurrency cap and deadline apply.
func PrintHTML(ctx context.Context, htmlDoc string) ([]byte, error) {
	var allocCtx context.Context
	var cancelAlloc context.CancelFunc
	if debugURL := os.Getenv("CHROME_DEBUG_URL"); debugURL != "" {
		allocCtx, cancelAlloc = remote.NewAllocator(ctx, debugURL)
	} else {
		allocCtx, cancelAlloc = chromedp.NewExecAllocator(ctx, allocatorOptions()...)
	}
	defer cancelAlloc()
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	dataURL := "data:text/html;charset=utf-8," + encodeForDataURL(htmlDoc)
	var buf []byte
	err := chromedp.Do(browserCtx,
		chromedp.Navigate(dataURL),
		chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
			res, err := cdp.Call(ctx, t, page.PrintToPDF, page.PrintToPDFParams{
				PrintBackground: new(true),
				MarginTop:       new(0.4),
				MarginBottom:    new(0.4),
				MarginLeft:      new(0.4),
				MarginRight:     new(0.4),
			})
			buf = res.Data
			return err
		}),
	)
	if err != nil {
		if isChromeUnavailable(err) {
			return nil, fmt.Errorf("%w: %v", ErrChromeUnavailable, err)
		}
		return nil, fmt.Errorf("pdfrender: chromedp run: %w", err)
	}
	if len(buf) < 5 || string(buf[:5]) != "%PDF-" {
		return nil, fmt.Errorf("pdfrender: invalid PDF output (len=%d, prefix=%q)", len(buf), safePrefix(buf))
	}
	return buf, nil
}

// encodeForDataURL percent-encodes the small set of characters that would
// confuse chromedp's data-URL parser. Full URL encoding is overkill; only
// `%`, `#`, `&`, `?` and newlines need attention because
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

// isChromeUnavailable heuristically classifies a chromedp error as "browser
// executable missing" so the handler can 503 rather than 500.
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
