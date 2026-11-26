package pdfrender

// Unit tests for the pure helpers behind PrintHTML. The chromedp render
// itself needs a real browser and is exercised by the renderers'
// integration tests and the PDF handler tests. These tests moved here from
// internal/board, internal/questionnaire and internal/audit/walkthrough when
// the five copies of the render block became one (#1847).

import (
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/chromedp/chromedp"
)

// TestEncodeForDataURL exercises every replacement target so a regression
// in the strings.NewReplacer pair list surfaces here.
func TestEncodeForDataURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"plain text 1 2 3", "plain text 1 2 3"},
		{"<!doctype html><div>ABC</div>", "<!doctype html><div>ABC</div>"},
		{"a%b", "a%25b"},
		{"a#b", "a%23b"},
		{"a&b", "a%26b"},
		{"a?b", "a%3Fb"},
		{"a\nb", "a%0Ab"},
		{"a\rb", "ab"},
		{"a\r\nb", "a%0Ab"},
		{"all % # & ? \n \r", "all %25 %23 %26 %3F %0A "},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := encodeForDataURL(tc.in); got != tc.want {
				t.Errorf("encodeForDataURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestIsChromeUnavailable covers each substring branch plus the nil and
// unrelated-error negative cases. A false positive would 503 a real render
// failure; a false negative would 500 a host that simply lacks Chrome.
func TestIsChromeUnavailable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"unrelated", errors.New("network: i/o timeout"), false},
		{"exec_prefix", errors.New(`exec: "chromium": not found`), true},
		{"exec_substring", errors.New("chromedp: exec: failed"), true},
		{"executable_file_not_found", errors.New("executable file not found in $PATH"), true},
		{"no_chrome_found", errors.New("chromedp: no chrome found in standard locations"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isChromeUnavailable(tc.err); got != tc.want {
				t.Errorf("isChromeUnavailable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestSafePrefix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{"empty", []byte{}, ""},
		{"short", []byte("abc"), "abc"},
		{"exactly_16", []byte("0123456789abcdef"), "0123456789abcdef"},
		{"longer_than_16", []byte("0123456789abcdef!!!extra"), "0123456789abcdef"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := safePrefix(tc.in); got != tc.want {
				t.Errorf("safePrefix(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestAllocatorOptions_DisableStartupNetworkCalls pins the flags that stop a
// cold Chrome calling update.googleapis.com / clients2.google.com, and checks
// the disable-features override still carries chromedp's own entries.
func TestAllocatorOptions_DisableStartupNetworkCalls(t *testing.T) {
	t.Parallel()
	flags := allocatorFlags(t)
	for _, want := range []string{
		"--disable-component-update",
		"--disable-background-networking",
		"--disable-domain-reliability",
		"--no-pings",
		"--headless",
		"--no-sandbox",
	} {
		if !slices.Contains(flags, want) {
			t.Errorf("allocator flags missing %s; got %v", want, flags)
		}
	}
	var features string
	for _, f := range flags {
		if v, ok := strings.CutPrefix(f, "--disable-features="); ok {
			features = v
		}
	}
	for _, want := range []string{"Translate", "BlinkGenPropertyTrees", "OptimizationHints", "MediaRouter"} {
		if !slices.Contains(strings.Split(features, ","), want) {
			t.Errorf("--disable-features=%q missing %s", features, want)
		}
	}
}

func TestRequireRealPDF(t *testing.T) {
	cases := map[string]bool{"": false, "0": false, "false": false, "junk": false, "1": true, "true": true}
	for v, want := range cases {
		t.Setenv(envRequirePDF, v)
		if got := RequireRealPDF(); got != want {
			t.Errorf("ATLAS_REQUIRE_PDF=%q: RequireRealPDF() = %v, want %v", v, got, want)
		}
	}
}

func TestErrChromeUnavailable_WrapsForErrorsIs(t *testing.T) {
	t.Parallel()
	wrapped := errors.Join(errors.New("board/pdf"), ErrChromeUnavailable)
	if !errors.Is(wrapped, ErrChromeUnavailable) {
		t.Fatal("a wrapped ErrChromeUnavailable must match errors.Is so handlers 503")
	}
}

// allocatorFlags renders allocatorOptions into Chrome's command line. It
// allocates against a nonexistent binary and captures the argument list
// through chromedp's ModifyCmdFunc hook, which runs before the (failing)
// process start. No browser is launched.
func allocatorFlags(t *testing.T) []string {
	t.Helper()
	var args []string
	opts := append(allocatorOptions(),
		chromedp.ExecPath(filepath.Join(t.TempDir(), "no-chrome")),
		chromedp.ModifyCmdFunc(func(cmd *exec.Cmd) { args = cmd.Args[1:] }),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(t.Context(), opts...)
	defer cancelAlloc()
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()
	if err := chromedp.Do(browserCtx, chromedp.Navigate("about:blank")); err == nil {
		t.Fatal("allocating a nonexistent Chrome must fail")
	}
	if len(args) == 0 {
		t.Fatal("ModifyCmdFunc never saw the Chrome command line")
	}
	return args
}
