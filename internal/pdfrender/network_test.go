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

func printOrSkip(t *testing.T, htmlDoc string, opts []chromedp.ExecAllocatorOption) []byte {
	t.Helper()
	pdf, err := printHTML(t.Context(), htmlDoc, opts)
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

func assertNoCanaryHits(t *testing.T, opts []chromedp.ExecAllocatorOption) {
	t.Helper()
	t.Setenv("CHROME_DEBUG_URL", "")
	srv, hits := newCanary(t)
	pdf := printOrSkip(t, canaryHTML(srv.URL), opts)
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
	assertNoCanaryHits(t, append(allocatorOptions(), lnaOff))
}

// TestPrintHTML_FetchBlockAlone drops host-resolver-rules, as a remote
// CHROME_DEBUG_URL browser does (its flags are not ours to set), so the
// Fetch interception is the only guard left.
func TestPrintHTML_FetchBlockAlone(t *testing.T) {
	assertNoCanaryHits(t, append(allocatorOptions(), lnaOff, chromedp.Flag("host-resolver-rules", false)))
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
	pdf := printOrSkip(t, `<!doctype html><html><body><img src="`+png1x1+`"><p>ok</p></body></html>`, allocatorOptions())
	if string(pdf[:5]) != "%PDF-" {
		t.Fatalf("output is not a PDF: %q", safePrefix(pdf))
	}
}
