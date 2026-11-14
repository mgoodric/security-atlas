package pdfrender

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
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
		// #1941: no hostname resolves, so a request that slips past the
		// Fetch block in PrintHTML still cannot reach a named host. It does
		// not apply to a remote CHROME_DEBUG_URL browser, whose flags we do
		// not set; there the Fetch block is the only guard.
		chromedp.Flag("host-resolver-rules", "MAP * ~NOTFOUND"),
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
// URL. Two guards hold that on both paths, so a builder that misses an
// escape cannot turn the renderer into an SSRF client (#1941):
//
//   - script is disabled, so nothing can open a WebSocket, start a worker
//     or build a request after load. No builder emits script;
//   - every other request the document makes (img, link, iframe, CSS url())
//     is failed through Fetch interception before it leaves the browser.
//
// PrintHTML does not take a render slot; callers that serve requests wrap it
// in Default().Do so the shared concurrency cap and deadline apply.
func PrintHTML(ctx context.Context, htmlDoc string) ([]byte, error) {
	return renderer{opts: allocatorOptions()}.print(ctx, htmlDoc)
}

// renderer is PrintHTML's configuration. Production sets only opts; the
// network tests set the other fields to remove one guard at a time and show
// each one holds on its own.
type renderer struct {
	opts         []chromedp.ExecAllocatorOption
	noFetchBlock bool
	allowScript  bool
}

func (r renderer) print(ctx context.Context, htmlDoc string) ([]byte, error) {
	var allocCtx context.Context
	var cancelAlloc context.CancelFunc
	if debugURL := os.Getenv("CHROME_DEBUG_URL"); debugURL != "" {
		allocCtx, cancelAlloc = remote.NewAllocator(ctx, debugURL)
	} else {
		allocCtx, cancelAlloc = chromedp.NewExecAllocator(ctx, r.opts...)
	}
	defer cancelAlloc()
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	var guards []chromedp.Action[chromedp.Void]
	if !r.allowScript {
		guards = append(guards, chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
			_, err := cdp.Call(ctx, t, emulation.SetScriptExecutionDisabled,
				emulation.SetScriptExecutionDisabledParams{Value: true})
			return err
		}))
	}
	if !r.noFetchBlock {
		if err := blockNonDataRequests(browserCtx); err != nil {
			return nil, renderError(err)
		}
		guards = append(guards, chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
			_, err := cdp.Call(ctx, t, fetch.Enable, fetch.EnableParams{
				Patterns: []*fetch.RequestPattern{{URLPattern: "*"}},
			})
			return err
		}))
	}

	dataURL := "data:text/html;charset=utf-8," + encodeForDataURL(htmlDoc)
	var buf []byte
	err := chromedp.Do(browserCtx, append(guards,
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
	)...)
	if err != nil {
		return nil, renderError(err)
	}
	if len(buf) < 5 || string(buf[:5]) != "%PDF-" {
		return nil, fmt.Errorf("pdfrender: invalid PDF output (len=%d, prefix=%q)", len(buf), safePrefix(buf))
	}
	return buf, nil
}

// blockNonDataRequests answers every request Fetch pauses in the tab of
// browserCtx: a data: URL continues, anything else fails as BlockedByClient.
// The listener must not run commands itself (chromedp.Listen), so each answer
// goes out from its own goroutine. Fetch.enable is sent by the caller, before
// navigation.
func blockNonDataRequests(browserCtx context.Context) error {
	return chromedp.Listen(browserCtx, func(ev any) {
		paused, ok := ev.(*fetch.EventRequestPaused)
		if !ok {
			return
		}
		go func() {
			// Listen has set up the tab, so the Target is in place.
			target := chromedp.FromContext(browserCtx).Target
			if paused.Request != nil && strings.HasPrefix(paused.Request.URL, "data:") {
				_, _ = cdp.Call(browserCtx, target, fetch.ContinueRequest, fetch.ContinueRequestParams{RequestID: paused.RequestID})
				return
			}
			_, _ = cdp.Call(browserCtx, target, fetch.FailRequest, fetch.FailRequestParams{
				RequestID:   paused.RequestID,
				ErrorReason: network.ErrorReasonBlockedByClient,
			})
		}()
	})
}

// renderError wraps a chromedp error, mapping a missing browser to
// ErrChromeUnavailable so handlers 503.
func renderError(err error) error {
	if isChromeUnavailable(err) {
		return fmt.Errorf("%w: %v", ErrChromeUnavailable, err)
	}
	return fmt.Errorf("pdfrender: chromedp run: %w", err)
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
