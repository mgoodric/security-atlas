package pdfrender

// #1941: PrintHTML must load nothing but its own data: URL. These tests
// render HTML that references a canary server on 127.0.0.1 and assert the
// canary never sees a request. They need a real Chrome and skip without one,
// unless ATLAS_REQUIRE_PDF is set (CI), where a missing Chrome fails.

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chromedp/chromedp"
)

func newCanary(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		t.Logf("canary hit: %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// lnaOff switches off Chrome's Local Network Access checks, which on their
// own stop a data: page reaching 127.0.0.1. Without this the canary would
// stay silent whether or not PrintHTML blocks anything: loopback stands in
// for an internal address that LNA does not cover. Like allocatorOptions'
// own disable-features flag, it repeats chromedp's two entries.
var lnaOff = chromedp.Flag("disable-features",
	"Translate,BlinkGenPropertyTrees,OptimizationHints,MediaRouter,"+
		"LocalNetworkAccessChecks,BlockInsecurePrivateNetworkRequests,PrivateNetworkAccessSendPreflights")

// noResolverBlock drops host-resolver-rules, as a remote CHROME_DEBUG_URL
// browser does (its flags are not ours to set).
var noResolverBlock = chromedp.Flag("host-resolver-rules", false)

func printOrSkip(t *testing.T, htmlDoc string, r renderer) []byte {
	t.Helper()
	pdf, err := r.print(t.Context(), htmlDoc)
	if errors.Is(err, ErrChromeUnavailable) && !RequireRealPDF() {
		t.Skipf("chrome unavailable: %v", err)
	}
	if err != nil {
		t.Fatalf("PrintHTML: %v", err)
	}
	return pdf
}

func canaryHTML(base string) string {
	return fmt.Sprintf(`<!doctype html><html><head>
<link rel="stylesheet" href="%[1]s/canary.css">
<script src="%[1]s/canary.js"></script>
</head><body>
<p>canary</p>
<img src="%[1]s/canary.png">
<iframe src="%[1]s/canary-frame"></iframe>
<div style="background:url('%[1]s/canary-bg.png')">bg</div>
</body></html>`, base)
}

func assertNoCanaryHits(t *testing.T, r renderer) {
	t.Helper()
	t.Setenv("CHROME_DEBUG_URL", "")
	srv, hits := newCanary(t)
	pdf := printOrSkip(t, canaryHTML(srv.URL), r)
	if string(pdf[:5]) != "%PDF-" {
		t.Fatalf("output is not a PDF: %q", safePrefix(pdf))
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("canary server received %d request(s); PrintHTML must block every non-data: request", n)
	}
}

// TestPrintHTML_BlocksNonDataRequests renders with the production local
// flags (Fetch block + host-resolver-rules).
func TestPrintHTML_BlocksNonDataRequests(t *testing.T) {
	assertNoCanaryHits(t, renderer{opts: append(allocatorOptions(), lnaOff)})
}

// TestPrintHTML_FetchBlockAlone drops host-resolver-rules, as on the remote
// path, so the Fetch interception is the only network guard left. The
// canary page's markup needs no script to make its requests.
func TestPrintHTML_FetchBlockAlone(t *testing.T) {
	assertNoCanaryHits(t, renderer{opts: append(allocatorOptions(), lnaOff, noResolverBlock)})
}

// TestPrintHTML_ScriptDisabled covers what Fetch interception cannot see:
// a WebSocket, a worker, an image built by script after load. With script
// off none of them is ever created. Fetch interception and
// host-resolver-rules are both removed here, so a hit can only mean script
// ran. The fetch() and WebSocket go to the canary by IP literal.
func TestPrintHTML_ScriptDisabled(t *testing.T) {
	t.Setenv("CHROME_DEBUG_URL", "")
	srv, hits := newCanary(t)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	htmlDoc := fmt.Sprintf(`<!doctype html><html><body><p>script</p>
<script>
fetch(%[1]q + "/canary-fetch").catch(function(){});
try { new WebSocket(%[2]q + "/canary-ws"); } catch (e) {}
new Image().src = %[1]q + "/canary-img";
</script></body></html>`, srv.URL, wsURL)

	pdf := printOrSkip(t, htmlDoc, renderer{
		opts:         append(allocatorOptions(), lnaOff, noResolverBlock),
		noFetchBlock: true,
	})
	if string(pdf[:5]) != "%PDF-" {
		t.Fatalf("output is not a PDF: %q", safePrefix(pdf))
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("canary server received %d request(s); PrintHTML must not run script", n)
	}
}

// TestAllocatorOptions_BlockHostResolution pins the host-resolver-rules flag
// on the local launch, the second layer behind the Fetch block.
func TestAllocatorOptions_BlockHostResolution(t *testing.T) {
	t.Parallel()
	if flags := allocatorFlags(t); !slices.Contains(flags, "--host-resolver-rules=MAP * ~NOTFOUND") {
		t.Errorf("allocator flags missing --host-resolver-rules=MAP * ~NOTFOUND; got %v", flags)
	}
}

// TestPrintHTML_AllowsDataResources keeps the block from over-reaching: an
// inline data: image is part of a self-contained document and still renders.
func TestPrintHTML_AllowsDataResources(t *testing.T) {
	t.Setenv("CHROME_DEBUG_URL", "")
	const png1x1 = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="
	pdf := printOrSkip(t, `<!doctype html><html><body><img src="`+png1x1+`"><p>ok</p></body></html>`, renderer{opts: allocatorOptions()})
	if string(pdf[:5]) != "%PDF-" {
		t.Fatalf("output is not a PDF: %q", safePrefix(pdf))
	}
}
