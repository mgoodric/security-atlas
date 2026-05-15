// Unit tests for the templated board-brief narrative renderer (slice 031).
//
// AC-3 + AC-6: the narrative is rendered via Go text/template over the
// structured Brief — pure, deterministic, NO LLM. These tests assert the
// template interpolates the structured fields and that the render is a
// total function of the Brief (no DB, no network, no inference).

package board

import (
	"strings"
	"testing"
)

func sampleBrief() Brief {
	return Brief{
		PeriodEnd:   "2026-04-30",
		GeneratedAt: "2026-05-14T16:00:00Z",
		Frameworks: []FrameworkPosture{
			{
				Slug: "soc2", Name: "SOC 2", CoveragePct: 94, FreshnessPct: 88,
				TrendArrow: TrendUp, Delta: 2, State: "audit-ready",
			},
			{
				Slug: "iso27001", Name: "ISO 27001", CoveragePct: 78, FreshnessPct: 71,
				TrendArrow: TrendFlat, Delta: 0, State: "in-progress",
			},
		},
		Drift: DriftSummary{
			WindowDays: 30, Since: "2026-03-31", Through: "2026-04-30",
			Delta: -1, FlippedOutCount: 1,
		},
		TopRisks: []RiskAging{
			{
				ID: "11111111-1111-1111-1111-111111111111", Title: "Unpatched CVE backlog",
				Category: "operational", Treatment: "mitigate",
				ResidualSeverity: 16, AgeDays: 95,
			},
		},
	}
}

// AC-3: the templated narrative interpolates coverage_pct, trend_arrow, and
// delta for each framework — the issue's worked example shape.
func TestRenderNarrative_InterpolatesFrameworkPosture(t *testing.T) {
	md, err := RenderNarrative(sampleBrief())
	if err != nil {
		t.Fatalf("RenderNarrative: %v", err)
	}
	// AC-3 worked example: "We are in audit-ready state for SOC 2 (94% ..."
	want := "We are in audit-ready state for SOC 2 (94% coverage, up +2 pts over 30 days)"
	if !strings.Contains(md, want) {
		t.Errorf("AC-3: narrative missing SOC 2 posture line\nwant substring: %q\ngot:\n%s", want, md)
	}
	if !strings.Contains(md, "We are in in-progress state for ISO 27001 (78% coverage, flat 0 pts over 30 days)") {
		t.Errorf("AC-3: narrative missing ISO 27001 posture line\ngot:\n%s", md)
	}
	if !strings.Contains(md, "Evidence freshness 88%") {
		t.Errorf("AC-3: narrative missing SOC 2 freshness\ngot:\n%s", md)
	}
}

// AC-2: the narrative carries the 30-day drift section.
func TestRenderNarrative_IncludesDriftSection(t *testing.T) {
	md, err := RenderNarrative(sampleBrief())
	if err != nil {
		t.Fatalf("RenderNarrative: %v", err)
	}
	if !strings.Contains(md, "Control drift — last 30 days") {
		t.Errorf("AC-2: narrative missing drift heading\ngot:\n%s", md)
	}
	if !strings.Contains(md, "drift count is -1") {
		t.Errorf("AC-2: narrative missing signed drift delta\ngot:\n%s", md)
	}
	if !strings.Contains(md, "1 control(s) drifted out of passing") {
		t.Errorf("AC-2: narrative missing flipped-out count\ngot:\n%s", md)
	}
}

// AC-2: the narrative carries the top-3 risks aging section.
func TestRenderNarrative_IncludesTopRisks(t *testing.T) {
	md, err := RenderNarrative(sampleBrief())
	if err != nil {
		t.Fatalf("RenderNarrative: %v", err)
	}
	if !strings.Contains(md, "Top risks aging") {
		t.Errorf("AC-2: narrative missing top-risks heading\ngot:\n%s", md)
	}
	if !strings.Contains(md, "Unpatched CVE backlog") {
		t.Errorf("AC-2: narrative missing risk title\ngot:\n%s", md)
	}
	if !strings.Contains(md, "open 95 day(s)") {
		t.Errorf("AC-2: narrative missing risk age\ngot:\n%s", md)
	}
}

// The render is deterministic — the same Brief produces byte-identical
// Markdown every time. This underpins AC-5 (the frozen narrative is stable).
func TestRenderNarrative_Deterministic(t *testing.T) {
	b := sampleBrief()
	first, err := RenderNarrative(b)
	if err != nil {
		t.Fatalf("RenderNarrative (1): %v", err)
	}
	second, err := RenderNarrative(b)
	if err != nil {
		t.Fatalf("RenderNarrative (2): %v", err)
	}
	if first != second {
		t.Errorf("render is not deterministic:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// An empty risk register renders the explicit "no open risks" line rather
// than an empty section.
func TestRenderNarrative_EmptyRiskRegister(t *testing.T) {
	b := sampleBrief()
	b.TopRisks = nil
	md, err := RenderNarrative(b)
	if err != nil {
		t.Fatalf("RenderNarrative: %v", err)
	}
	if !strings.Contains(md, "No open risks in the register.") {
		t.Errorf("expected empty-register line\ngot:\n%s", md)
	}
}

// signedInt formats with an explicit sign so the narrative reads
// unambiguously.
func TestSignedInt(t *testing.T) {
	cases := map[int]string{3: "+3", 0: "0", -2: "-2"}
	for in, want := range cases {
		if got := signedInt(in); got != want {
			t.Errorf("signedInt(%d) = %q, want %q", in, got, want)
		}
	}
}
