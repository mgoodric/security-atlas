package walkthrough

// Unit tests for the slice-027 export helpers in export.go. No DB
// required. These cover the pure-Go side of the export surface --
// markdown rendering, PDF HTML assembly, helper utilities, and the
// public IsTamperFlagged accessor -- so the package's merged coverage
// reaches the slice 288 target without depending on a real Chrome
// install (the chromedp PDF render is exercised at the integration
// level via the export wire path).
//
// Load-bearing functions covered:
//   - buildPDFHTML        (export.go:178)
//   - renderMarkdown      (export.go:249)
//   - headingLevel        (export.go:311)
//   - isAllDigits         (export.go:326)
//   - renderInline        (export.go:338)
//   - encodeForDataURL    (export.go:357)
//   - isChromeUnavailable (export.go:369)
//   - safePrefix          (export.go:379)
//   - IsTamperFlagged     (export.go:391)
//
// Branches the file is designed to cover:
//   - markdown: every block kind (H1-H4, UL, OL, paragraph, blank
//     break, inline `code`)
//   - markdown: block transitions (paragraph -> list -> heading and
//     back) that exercise renderMarkdown's closeBlocks() path
//   - PDF HTML: tamper banner, status class, optional transcript +
//     attachments, live vs period-pinned walkthrough
//   - data-URL encoding: every replacement target (% # & ? \n \r)
//   - chrome-unavailable error classification: every matched substring
//     plus the nil + unrelated-error negative cases
//   - safePrefix: short, exact-16, and long byte buffers

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestRenderMarkdown_HeadingsAtEveryLevel asserts that the slice-022-
// style line-based renderer emits matching <hN> tags for #, ##, ###,
// and ####. A regression in the level counter would silently demote
// every heading to a paragraph.
func TestRenderMarkdown_HeadingsAtEveryLevel(t *testing.T) {
	src := "# H1 title\n## H2 title\n### H3 title\n#### H4 title\n"
	got := renderMarkdown(src)
	want := []string{
		"<h1>H1 title</h1>",
		"<h2>H2 title</h2>",
		"<h3>H3 title</h3>",
		"<h4>H4 title</h4>",
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("renderMarkdown missing %q\nfull: %s", w, got)
		}
	}
}

// TestRenderMarkdown_UnorderedList exercises both list-marker forms
// (-, *) and the implicit <ul>/</ul> open + close on transition.
func TestRenderMarkdown_UnorderedList(t *testing.T) {
	src := "- one\n* two\n- three\n"
	got := renderMarkdown(src)
	if !strings.Contains(got, "<ul>") || !strings.Contains(got, "</ul>") {
		t.Fatalf("expected <ul> wrapper, got %s", got)
	}
	for _, item := range []string{"<li>one</li>", "<li>two</li>", "<li>three</li>"} {
		if !strings.Contains(got, item) {
			t.Errorf("expected %q in output, got %s", item, got)
		}
	}
}

// TestRenderMarkdown_OrderedList exercises the digit-prefix path,
// including the multi-digit case (10. ...) so the isAllDigits guard
// fires.
func TestRenderMarkdown_OrderedList(t *testing.T) {
	src := "1. first\n2. second\n10. tenth\n"
	got := renderMarkdown(src)
	if !strings.Contains(got, "<ol>") || !strings.Contains(got, "</ol>") {
		t.Fatalf("expected <ol> wrapper, got %s", got)
	}
	for _, item := range []string{"<li>first</li>", "<li>second</li>", "<li>tenth</li>"} {
		if !strings.Contains(got, item) {
			t.Errorf("expected %q in output, got %s", item, got)
		}
	}
}

// TestRenderMarkdown_ParagraphsAndBlankLineBreak asserts that a blank
// line closes the current paragraph and starts a new one, and that
// consecutive non-blank lines collapse into a single paragraph with a
// space separator. Both branches of the inPara flag are exercised.
func TestRenderMarkdown_ParagraphsAndBlankLineBreak(t *testing.T) {
	src := "first line\nsecond line\n\nthird paragraph"
	got := renderMarkdown(src)
	// Two <p> blocks expected.
	if strings.Count(got, "<p>") != 2 {
		t.Errorf("expected 2 <p> blocks, got %d in %s", strings.Count(got, "<p>"), got)
	}
	if !strings.Contains(got, "first line second line") {
		t.Errorf("expected first+second lines joined by a space, got %s", got)
	}
	if !strings.Contains(got, "third paragraph") {
		t.Errorf("expected the second paragraph, got %s", got)
	}
}

// TestRenderMarkdown_InlineCode asserts that the single-backtick inline
// code path renders to a <code> wrapper and escapes the body. Untermin-
// ated backticks fall through to literal characters.
func TestRenderMarkdown_InlineCode(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{
			"closed_backticks",
			"call `func()` to invoke",
			[]string{"<code>func()</code>"},
		},
		{
			"unterminated_backticks",
			"this `is not closed",
			[]string{"`is not closed"}, // escaped literal, no <code>
		},
		{
			"escapes_html_inside_code",
			"use `<script>` carefully",
			[]string{"<code>&lt;script&gt;</code>"},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := renderMarkdown(tc.in)
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("expected %q, got %s", w, got)
				}
			}
			if tc.name == "unterminated_backticks" && strings.Contains(got, "<code>") {
				t.Errorf("unterminated backtick should not render <code>: %s", got)
			}
		})
	}
}

// TestRenderMarkdown_BlockTransitionsCloseProperly exercises the
// closeBlocks() path: a paragraph followed by a list followed by a
// heading must close the paragraph + list before opening the heading
// so the DOM is not nested.
func TestRenderMarkdown_BlockTransitionsCloseProperly(t *testing.T) {
	src := "paragraph here\n- list item\n# heading after\n"
	got := renderMarkdown(src)
	// paragraph -> list -> heading: each block must close before the
	// next opens.
	if !strings.Contains(got, "</p>") {
		t.Errorf("expected </p> close, got %s", got)
	}
	if !strings.Contains(got, "</ul>") {
		t.Errorf("expected </ul> close, got %s", got)
	}
	if !strings.Contains(got, "<h1>heading after</h1>") {
		t.Errorf("expected <h1>heading after</h1>, got %s", got)
	}
}

// TestRenderMarkdown_EmptyInput asserts that an empty input produces
// an empty output without panicking.
func TestRenderMarkdown_EmptyInput(t *testing.T) {
	if got := renderMarkdown(""); got != "" {
		t.Fatalf("expected empty output, got %q", got)
	}
}

// TestHeadingLevel covers the # counter at every accepted depth plus
// the rejection cases (5+ hashes, no trailing space, no leading hash).
func TestHeadingLevel(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"# title", 1},
		{"## title", 2},
		{"### title", 3},
		{"#### title", 4},
		{"##### title", 0}, // 5+ rejects
		{"#title", 0},      // no space
		{"plain text", 0},  // no hash
		{"", 0},            // empty
		{"#", 0},           // hash with no body
	}
	for _, tc := range cases {
		tc := tc
		t.Run(fmt.Sprintf("level_for_%q", tc.in), func(t *testing.T) {
			if got := headingLevel(tc.in); got != tc.want {
				t.Errorf("headingLevel(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestIsAllDigits exercises both branches: digit-only strings, mixed
// strings, and the empty string (returns false per the documented
// contract).
func TestIsAllDigits(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"0", true},
		{"123", true},
		{"9876543210", true},
		{"", false},    // empty rejects per contract
		{"12a", false}, // mixed
		{"a1", false},  // leading letter
		{"1.0", false}, // dot
		{" 12", false}, // leading whitespace
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			if got := isAllDigits(tc.in); got != tc.want {
				t.Errorf("isAllDigits(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestRenderInline covers the closing-backtick + non-closing branches
// directly, complementing the markdown-wrapped tests above.
func TestRenderInline(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "hello world", "hello world"},
		{"escapes_html", "<a>", "&lt;a&gt;"},
		{"backtick_pair", "x `code` y", "x <code>code</code> y"},
		{"unclosed_backtick", "x `oops", "x `oops"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := renderInline(tc.in); got != tc.want {
				t.Errorf("renderInline(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestEncodeForDataURL exercises every replacement target so a regress-
// ion in the strings.NewReplacer pair list surfaces here.
func TestEncodeForDataURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"plain", "plain"},
		{"a%b", "a%25b"},
		{"a#b", "a%23b"},
		{"a&b", "a%26b"},
		{"a?b", "a%3Fb"},
		{"a\nb", "a%0Ab"},
		{"a\rb", "ab"}, // CR is stripped
		{"all % # & ? \n \r", "all %25 %23 %26 %3F %0A "}, // composite
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			if got := encodeForDataURL(tc.in); got != tc.want {
				t.Errorf("encodeForDataURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestIsChromeUnavailable covers each substring branch plus the nil
// + unrelated-error negative cases. A regression in this classifier
// would cause RenderPDF to surface ErrChromeUnavailable for unrelated
// failures (false positive) or to mask the real error (false negative).
func TestIsChromeUnavailable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"unrelated", errors.New("some other error"), false},
		{"exec_prefix", errors.New("exec: \"chromium\": not found"), true},
		{"exec_substring_in_message", errors.New("chromedp: exec: failed"), true},
		{"executable_file_not_found", errors.New("executable file not found in $PATH"), true},
		{"no_chrome_found", errors.New("chromedp: no chrome found"), true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := isChromeUnavailable(tc.err); got != tc.want {
				t.Errorf("isChromeUnavailable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestSafePrefix covers the long-buffer + short-buffer branches.
func TestSafePrefix(t *testing.T) {
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
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := safePrefix(tc.in); got != tc.want {
				t.Errorf("safePrefix(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestIsTamperFlagged is a tiny accessor test, but it pins the exported
// surface that callers without a Store handle depend on.
func TestIsTamperFlagged(t *testing.T) {
	clean := Walkthrough{TamperDetected: false}
	if IsTamperFlagged(clean) {
		t.Errorf("IsTamperFlagged(clean) = true, want false")
	}
	tampered := Walkthrough{TamperDetected: true}
	if !IsTamperFlagged(tampered) {
		t.Errorf("IsTamperFlagged(tampered) = false, want true")
	}
}

// TestBuildPDFHTML_HappyPath exercises the canonical render path:
// non-tampered, period-pinned, with transcript, with attachments. The
// assertions pin the surface a downstream PDF renderer (chromedp) and
// a human auditor depend on.
func TestBuildPDFHTML_HappyPath(t *testing.T) {
	periodID := uuid.MustParse("99999999-9999-9999-9999-999999999999")
	w := Walkthrough{
		ID:            uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		TenantID:      uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		AuditPeriodID: &periodID,
		ControlID:     uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		Narrative:     "# Heading\n\nThe team rotates API keys on a 90-day cadence.",
		Transcript:    "auditor: walk me through the cadence",
		Status:        StatusFinalized,
		CanonicalHash: []byte{0xde, 0xad, 0xbe, 0xef},
		CreatedBy:     "engineer-001",
		CreatedAt:     time.Date(2026, 5, 13, 12, 34, 56, 0, time.UTC),
		Attachments: []Attachment{{
			ID:          uuid.MustParse("44444444-4444-4444-4444-444444444444"),
			StorageKey:  "tenant-22222222-2222-2222-2222-222222222222/44444444-4444-4444-4444-444444444444",
			ContentType: "image/png",
			SizeBytes:   2048,
			SHA256Hex:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			UploadedBy:  "engineer-001",
			UploadedAt:  time.Date(2026, 5, 13, 12, 35, 0, 0, time.UTC),
		}},
		TamperDetected: false,
	}
	html := buildPDFHTML(w)

	mustContain := []string{
		`<!doctype html>`,
		`<title>Walkthrough 11111111-1111-1111-1111-111111111111</title>`,
		// metadata table
		`<th>Walkthrough ID</th>`,
		`<th>Control ID</th>`,
		`<th>Audit period</th>`,
		`99999999-9999-9999-9999-999999999999`,
		`<th>Status</th>`,
		`status-finalized`, // status class
		`finalized`,        // status label
		`<th>Created by</th>`,
		`engineer-001`,
		`<th>Canonical hash</th>`,
		`deadbeef`, // hex of CanonicalHash
		// narrative rendered through the markdown pipeline
		`<h1>Heading</h1>`,
		// transcript section
		`<h2>Transcript</h2>`,
		`auditor: walk me through the cadence`,
		// attachments section
		`<h2>Attachments</h2>`,
		`image/png`,
		`2048`,
		`aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa`,
	}
	for _, w := range mustContain {
		if !strings.Contains(html, w) {
			t.Errorf("buildPDFHTML missing %q", w)
		}
	}
	if strings.Contains(html, "TAMPER DETECTED") {
		t.Errorf("buildPDFHTML(clean) should NOT contain tamper banner, got: %s", html)
	}
}

// TestBuildPDFHTML_TamperBanner asserts that a tampered walkthrough
// renders the prominent banner advising not to audit-bind the export.
func TestBuildPDFHTML_TamperBanner(t *testing.T) {
	w := Walkthrough{
		ID:             uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		ControlID:      uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		Narrative:      "tampered",
		Status:         StatusDraft,
		CanonicalHash:  []byte{0x01, 0x02},
		CreatedBy:      "engineer-001",
		CreatedAt:      time.Date(2026, 5, 13, 12, 34, 56, 0, time.UTC),
		TamperDetected: true,
	}
	html := buildPDFHTML(w)
	if !strings.Contains(html, "TAMPER DETECTED") {
		t.Errorf("buildPDFHTML(tampered) should contain tamper banner")
	}
	if !strings.Contains(html, "not advised") {
		t.Errorf("buildPDFHTML(tampered) should warn against audit-binding")
	}
}

// TestBuildPDFHTML_LiveWalkthrough asserts that a walkthrough with no
// audit_period_id renders the "live (no period pin)" placeholder
// instead of a missing-data hole.
func TestBuildPDFHTML_LiveWalkthrough(t *testing.T) {
	w := Walkthrough{
		ID:            uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		ControlID:     uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		Narrative:     "narrative",
		Status:        StatusDraft,
		CanonicalHash: []byte{0x01},
		CreatedBy:     "engineer-001",
		CreatedAt:     time.Date(2026, 5, 13, 12, 34, 56, 0, time.UTC),
	}
	html := buildPDFHTML(w)
	if !strings.Contains(html, "live (no period pin)") {
		t.Errorf("buildPDFHTML(live) should mark audit_period as live, got: %s", html)
	}
	// And the status class is status-draft.
	if !strings.Contains(html, "status-draft") {
		t.Errorf("buildPDFHTML(draft) should carry status-draft class")
	}
}

// TestBuildPDFHTML_OmitsTranscriptAndAttachmentsWhenEmpty asserts the
// optional sections are skipped when the walkthrough has none -- the
// PDF must not render an empty <h2>Transcript</h2> followed by nothing.
func TestBuildPDFHTML_OmitsTranscriptAndAttachmentsWhenEmpty(t *testing.T) {
	w := Walkthrough{
		ID:            uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		ControlID:     uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		Narrative:     "narrative only",
		Status:        StatusDraft,
		CanonicalHash: []byte{0x01},
		CreatedBy:     "engineer-001",
		CreatedAt:     time.Date(2026, 5, 13, 12, 34, 56, 0, time.UTC),
		// no Transcript, no Attachments
	}
	html := buildPDFHTML(w)
	if strings.Contains(html, "<h2>Transcript</h2>") {
		t.Errorf("buildPDFHTML should omit empty transcript section")
	}
	if strings.Contains(html, "<h2>Attachments</h2>") {
		t.Errorf("buildPDFHTML should omit empty attachments section")
	}
	if !strings.Contains(html, "narrative only") {
		t.Errorf("buildPDFHTML should still render narrative")
	}
}

// TestBuildPDFHTML_EscapesHTMLInIDsAndCreatedBy asserts that user-
// controlled fields are HTML-escaped so a malicious narrative or
// created_by string cannot inject markup into the rendered PDF source.
func TestBuildPDFHTML_EscapesHTMLInIDsAndCreatedBy(t *testing.T) {
	w := Walkthrough{
		ID:            uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		ControlID:     uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		Narrative:     "<script>alert(1)</script>",
		Status:        StatusDraft,
		CanonicalHash: []byte{0x01},
		CreatedBy:     `<img src=x onerror=alert(1)>`,
		CreatedAt:     time.Date(2026, 5, 13, 12, 34, 56, 0, time.UTC),
	}
	html := buildPDFHTML(w)
	// The literal opening tag must NOT appear unescaped in the document.
	if strings.Contains(html, "<script>alert(1)</script>") {
		t.Errorf("buildPDFHTML failed to escape narrative HTML; got: %s", html)
	}
	if strings.Contains(html, `<img src=x onerror=alert(1)>`) {
		t.Errorf("buildPDFHTML failed to escape created_by HTML; got: %s", html)
	}
	// The escaped form should appear instead.
	if !strings.Contains(html, "&lt;script&gt;") && !strings.Contains(html, "&lt;img") {
		t.Errorf("buildPDFHTML did not escape user-controlled fields; got: %s", html)
	}
}
