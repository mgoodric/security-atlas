// Unit tests for the pure HTML-rendering / helper layer of the slice-031
// monthly board brief PDF path (internal/board/pdf.go). The chromedp render
// itself needs a real browser and is exercised by integration tests; this
// file covers the load-bearing pure helpers:
//
//   - buildBriefHTML — assembles the print-ready HTML for the brief. The
//     chromedp PDF render is deterministic given the same HTML, so a
//     regression here is the proximate cause of brief visual drift.
//
// The data-URL encoder and the chrome-unavailable classifier moved to
// internal/pdfrender with the shared render helper (#1847); their tests
// live in internal/pdfrender/print_test.go.
//
// These mirror the slice-032 pack_pdf_html_test pattern for the quarterly
// pack, and are a slice-283 coverage lift: pre-lift these three functions
// were 0% covered (the slice-031 integration test only ran the chromedp
// path, not the HTML builder directly).

package board

import (
	"errors"
	"strings"
	"testing"
)

func sampleStoredBrief() StoredBrief {
	return StoredBrief{
		PeriodEnd: "2026-04-30",
		Content: Brief{
			PeriodEnd:   "2026-04-30",
			GeneratedAt: "2026-05-14T12:00:00Z",
			Frameworks: []FrameworkPosture{
				{Slug: "soc2", Name: "SOC 2", CoveragePct: 92, FreshnessPct: 88,
					TrendArrow: TrendUp, Delta: 2, State: "audit-ready"},
				{Slug: "iso27001", Name: "ISO 27001", CoveragePct: 75, FreshnessPct: 70,
					TrendArrow: TrendFlat, Delta: 0, State: "in-progress"},
			},
			Drift: DriftSummary{
				WindowDays: 30, Since: "2026-03-31", Through: "2026-04-30",
				Delta: -2, FlippedOutCount: 1,
			},
			TopRisks: []RiskAging{
				{ID: "11111111-1111-1111-1111-111111111111",
					Title: "Unpatched CVE backlog", Category: "operational",
					Treatment: "mitigate", ResidualSeverity: 16.5, AgeDays: 95},
			},
		},
	}
}

// ===== buildBriefHTML — shape + safety =====

// AC: the rendered HTML carries the brief's period-end, framework names,
// and the drift summary in the expected order. Empty-risk + populated-risk
// branches are covered by sibling tests below.
func TestBuildBriefHTML_RendersCoreSections(t *testing.T) {
	t.Parallel()
	got := buildBriefHTML(sampleStoredBrief())
	checks := []string{
		"Monthly Board Brief — 2026-04-30",
		"Program posture",
		"SOC 2",
		"92%",
		"ISO 27001",
		"Control drift",
		"1 control(s) drifted out of passing",
		"Top risks aging",
		"Unpatched CVE backlog",
	}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Errorf("buildBriefHTML missing %q", want)
		}
	}
}

// Zero-flip branch renders the "no controls drifted out of passing" copy.
func TestBuildBriefHTML_ZeroFlippedRendersNoFlippedCopy(t *testing.T) {
	t.Parallel()
	sb := sampleStoredBrief()
	sb.Content.Drift.FlippedOutCount = 0
	got := buildBriefHTML(sb)
	if !strings.Contains(got, "no controls drifted out of passing") {
		t.Fatal("zero-flip drift must render 'no controls drifted out of passing'")
	}
}

// Empty-risk branch renders the explicit "No open risks in the register" copy.
func TestBuildBriefHTML_EmptyTopRisksRendersEmptyCopy(t *testing.T) {
	t.Parallel()
	sb := sampleStoredBrief()
	sb.Content.TopRisks = nil
	got := buildBriefHTML(sb)
	if !strings.Contains(got, "No open risks in the register.") {
		t.Fatal("empty top-risks must render the 'no open risks' line")
	}
	if strings.Contains(got, "<table>") {
		t.Fatal("empty top-risks must NOT render the risks table")
	}
}

// buildBriefHTML escapes HTML-active characters in operator-controllable
// fields so a malicious framework name or period_end does not break out
// into the surrounding markup. Defence-in-depth: chromedp renders the
// produced HTML directly.
func TestBuildBriefHTML_EscapesHTMLActiveChars(t *testing.T) {
	t.Parallel()
	sb := sampleStoredBrief()
	sb.Content.PeriodEnd = `2026-04-30<script>alert(1)</script>`
	sb.Content.Frameworks[0].Name = `SOC <b>2</b>`
	got := buildBriefHTML(sb)
	if strings.Contains(got, "<script>alert(1)") {
		t.Fatal("period_end script tag must be escaped")
	}
	if strings.Contains(got, "<b>2</b>") {
		t.Fatal("framework name bold tag must be escaped")
	}
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Fatal("escaped period_end must appear in the output")
	}
}

// The framework posture cards include the state-class CSS so the styled
// "audit-ready" / "in-progress" / "at-risk" colour is applied — this is
// the visual contract the slice-031 mockup relies on.
func TestBuildBriefHTML_PostureStateClass(t *testing.T) {
	t.Parallel()
	sb := sampleStoredBrief()
	got := buildBriefHTML(sb)
	if !strings.Contains(got, `class="meta state-audit-ready"`) {
		t.Fatal("SOC 2 posture card must carry the state-audit-ready class")
	}
	if !strings.Contains(got, `class="meta state-in-progress"`) {
		t.Fatal("ISO 27001 posture card must carry the state-in-progress class")
	}
}

// The drift summary uses the signed-int helper so the narrative reads
// "+3" / "-2" / "0" unambiguously.
func TestBuildBriefHTML_DriftDeltaIsSigned(t *testing.T) {
	t.Parallel()
	sb := sampleStoredBrief()
	sb.Content.Drift.Delta = -2
	got := buildBriefHTML(sb)
	if !strings.Contains(got, "drift count -2") {
		t.Fatalf("drift delta must render with explicit sign; got: %q", got[:200])
	}
}

// ===== ErrChromeUnavailable sentinel =====

// ErrChromeUnavailable is the sentinel the HTTP handler maps to 503 — the
// slice-031 brief PDF path AND the slice-032 pack PDF path share it. A
// regression that renames or duplicates this would silently break the 503
// behavior, so we anchor it here.
func TestErrChromeUnavailable_IsExportedSentinel(t *testing.T) {
	t.Parallel()
	if ErrChromeUnavailable == nil {
		t.Fatal("ErrChromeUnavailable must be a non-nil sentinel error")
	}
	wrapped := errors.New("wrapper: " + ErrChromeUnavailable.Error())
	if errors.Is(wrapped, ErrChromeUnavailable) {
		t.Fatal("plain string concatenation must not match errors.Is — sanity check")
	}
}
