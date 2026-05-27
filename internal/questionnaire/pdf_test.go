// Slice 319 — pure-Go unit tests for the pdf.go renderer helpers.
//
// Load-bearing functions covered:
//   - buildHTML — every branch of the per-item rendering ladder
//     (needs-mapping vs anchor present, answer present vs unanswered,
//     domain present vs blank, answer_value present vs narrative-only).
//   - encodeForDataURL — every replacement pair in the replacer.
//   - isChromeUnavailable — nil-error, "exec:" prefix, "executable file
//     not found" prefix, "no chrome found" prefix, and unrelated errors
//     (negative case).
//
// RenderPDF itself is exercised end-to-end by the existing
// internal/api/questionnaires/integration_test.go (AC-A5) when Chrome is
// available; this unit suite intentionally does NOT spin headless Chrome
// so it stays green in environments where the browser is not installed
// (pre-existing chromedp flake awareness — slice 319 prompt).
package questionnaire

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestBuildHTML_HeaderAndSubline(t *testing.T) {
	in := PDFInput{
		QuestionnaireName: "ACME-CAIQ-2026",
		SourceLabel:       "CAIQ v4.1",
		GeneratedAt:       "2026-05-27T12:00:00Z",
	}
	got := buildHTML(in)
	if !strings.Contains(got, "<h1>ACME-CAIQ-2026</h1>") {
		t.Fatalf("expected H1 with questionnaire name, got: %s", excerpt(got))
	}
	if !strings.Contains(got, `CAIQ v4.1 · generated 2026-05-27T12:00:00Z`) {
		t.Fatalf("expected sub-line with source + generated, got: %s", excerpt(got))
	}
}

func TestBuildHTML_EscapesHTMLSpecialChars(t *testing.T) {
	// The PDF input is hydrated from DB columns. A vendor questionnaire
	// row with a <script> tag in the question text must NOT escape from
	// its <div> when the data: URL is rendered. The renderer html-escapes
	// every field — this asserts that contract.
	in := PDFInput{
		QuestionnaireName: "<script>alert('x')</script>",
		SourceLabel:       "<img onerror=1>",
		GeneratedAt:       "now",
		Items: []PDFItem{
			{
				Code:        "Q&A-01",
				Text:        "Does <vendor> require 'MFA'?",
				Domain:      "Identity & Access",
				ScfAnchorID: "IAC-06",
				AnswerValue: "yes",
				Narrative:   "Configured at <provider>.",
			},
		},
	}
	got := buildHTML(in)
	// The escaped form of < is &lt; and ' becomes &#39; per Go's
	// html.EscapeString. Any literal `<script>` in the output is a bug.
	if strings.Contains(got, "<script>alert") {
		t.Fatal("buildHTML failed to escape <script> in QuestionnaireName")
	}
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Fatal("expected html-escaped <script>, none found")
	}
	if !strings.Contains(got, "Q&amp;A-01") {
		t.Fatal("expected ampersand-escaped Code, none found")
	}
	if !strings.Contains(got, "Does &lt;vendor&gt; require") {
		t.Fatal("expected escaped angle brackets in question text")
	}
}

func TestBuildHTML_NeedsMappingBranch(t *testing.T) {
	in := PDFInput{
		QuestionnaireName: "Q1",
		SourceLabel:       "src",
		GeneratedAt:       "t",
		Items: []PDFItem{
			{
				Code:         "X-01",
				Text:         "unmapped question",
				NeedsMapping: true,
				// ScfAnchorID intentionally empty
			},
		},
	}
	got := buildHTML(in)
	if !strings.Contains(got, "needs manual SCF mapping") {
		t.Fatalf("expected needs-mapping marker; got: %s", excerpt(got))
	}
	if strings.Contains(got, "SCF anchor:") {
		t.Fatal("must NOT render SCF anchor when needs-mapping is true")
	}
}

func TestBuildHTML_AnchorBranch(t *testing.T) {
	in := PDFInput{
		QuestionnaireName: "Q1",
		SourceLabel:       "src",
		GeneratedAt:       "t",
		Items: []PDFItem{
			{
				Code:         "X-02",
				Text:         "mapped question",
				ScfAnchorID:  "IAC-06",
				NeedsMapping: false,
			},
		},
	}
	got := buildHTML(in)
	if !strings.Contains(got, "SCF anchor:") {
		t.Fatalf("expected SCF anchor line; got: %s", excerpt(got))
	}
	if !strings.Contains(got, "IAC-06") {
		t.Fatal("expected anchor id in output")
	}
	if strings.Contains(got, "needs manual SCF mapping") {
		t.Fatal("must NOT render needs-mapping when an anchor is set")
	}
}

func TestBuildHTML_DomainBranch(t *testing.T) {
	in := PDFInput{
		QuestionnaireName: "Q1",
		SourceLabel:       "src",
		GeneratedAt:       "t",
		Items: []PDFItem{
			{Code: "A-1", Text: "no domain", ScfAnchorID: "X-01"},
			{Code: "A-2", Text: "with domain", Domain: "Crypto", ScfAnchorID: "X-02"},
		},
	}
	got := buildHTML(in)
	if !strings.Contains(got, "A-2 · Crypto") {
		t.Fatalf("expected `A-2 · Crypto` rendered, got: %s", excerpt(got))
	}
	// The blank-domain item must render bare `A-1` without trailing dot.
	if strings.Contains(got, "A-1 · ") {
		t.Fatal("blank domain must NOT render the separator")
	}
}

func TestBuildHTML_AnswerVsUnanswered(t *testing.T) {
	in := PDFInput{
		QuestionnaireName: "Q1",
		SourceLabel:       "src",
		GeneratedAt:       "t",
		Items: []PDFItem{
			{Code: "A-1", Text: "no answer", ScfAnchorID: "X-01"},
			{Code: "A-2", Text: "value only", ScfAnchorID: "X-02", AnswerValue: "yes"},
			{Code: "A-3", Text: "narrative only", ScfAnchorID: "X-03", Narrative: "see policy"},
			{Code: "A-4", Text: "both", ScfAnchorID: "X-04", AnswerValue: "no", Narrative: "exception filed"},
		},
	}
	got := buildHTML(in)
	if !strings.Contains(got, "(unanswered)") {
		t.Fatal("expected `(unanswered)` marker for A-1")
	}
	if !strings.Contains(got, `<span class="value">yes</span>`) {
		t.Fatal("expected `yes` rendered as a value chip for A-2")
	}
	if !strings.Contains(got, "see policy") {
		t.Fatal("expected narrative rendered for A-3")
	}
	if !strings.Contains(got, `<span class="value">no</span>`) || !strings.Contains(got, "exception filed") {
		t.Fatal("expected both value chip + narrative rendered for A-4")
	}
}

func TestBuildHTML_EmptyItemsRendersHeaderOnly(t *testing.T) {
	in := PDFInput{
		QuestionnaireName: "Empty-Q",
		SourceLabel:       "src",
		GeneratedAt:       "t",
	}
	got := buildHTML(in)
	if !strings.Contains(got, "<h1>Empty-Q</h1>") {
		t.Fatal("expected H1 even with no items")
	}
	if strings.Contains(got, `<div class="item">`) {
		t.Fatal("must not render an item div when Items is empty")
	}
}

func TestEncodeForDataURL_ReplacesEveryControlChar(t *testing.T) {
	// One assertion per replacement pair in the replacer.
	cases := []struct {
		raw  string
		want string
	}{
		{"%", "%25"},
		{"#", "%23"},
		{"&", "%26"},
		{"?", "%3F"},
		{"\n", "%0A"},
		{"\r", ""},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%q", tc.raw), func(t *testing.T) {
			got := encodeForDataURL(tc.raw)
			if got != tc.want {
				t.Fatalf("encodeForDataURL(%q) = %q; want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestEncodeForDataURL_PreservesSafeChars(t *testing.T) {
	in := "<!doctype html><html><body><div>plain text 123 ABC</div></body></html>"
	got := encodeForDataURL(in)
	// Angle brackets and ASCII alpha/numerics are untouched.
	if !strings.Contains(got, "<!doctype html>") {
		t.Fatalf("unexpected mangling of safe chars: %s", got)
	}
}

func TestIsChromeUnavailable_NilErrorIsFalse(t *testing.T) {
	if isChromeUnavailable(nil) {
		t.Fatal("nil error must report false")
	}
}

func TestIsChromeUnavailable_RecognizesPatterns(t *testing.T) {
	patterns := []string{
		"exec: \"chrome\": executable file not found in $PATH",
		"chromedp: executable file not found",
		"chromedp: no chrome found in standard locations",
	}
	for _, p := range patterns {
		if !isChromeUnavailable(errors.New(p)) {
			t.Fatalf("expected pattern %q to be recognized as Chrome-unavailable", p)
		}
	}
}

func TestIsChromeUnavailable_UnrelatedErrorIsFalse(t *testing.T) {
	if isChromeUnavailable(errors.New("network: i/o timeout")) {
		t.Fatal("unrelated error must report false")
	}
}

func TestRenderPDF_NilContextRejected(t *testing.T) {
	_, err := RenderPDF(nil, PDFInput{QuestionnaireName: "Q"}) //nolint:staticcheck // explicit nil
	if err == nil {
		t.Fatal("expected nil-context error")
	}
	if !strings.Contains(err.Error(), "nil context") {
		t.Fatalf("expected nil-context message; got %v", err)
	}
}

// excerpt clips long strings for readable failure output.
func excerpt(s string) string {
	if len(s) <= 240 {
		return s
	}
	return s[:240] + "...(truncated)"
}
