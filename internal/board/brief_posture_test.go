// #2210: the monthly brief reports coverage from the same per-framework
// posture as the board pack. Unit tests for the brief's posture rendering,
// the shared reduction, and the decode of briefs stored before #2210.

package board

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// postureBrief is a #2210 brief over the pack's own posture rows: two scoped
// frameworks (75% and 20%) and one without an activated scope.
func postureBrief() Brief {
	rows := []frameworkPostureRow{
		{FrameworkID: uuid.New(), Name: "Framework A", Version: "2022", CoveragePct: 75, FreshnessPct: 60, TrendDelta90d: 50, ScopeActivated: true, TrendHistory: true},
		{FrameworkID: uuid.New(), Name: "Framework B", Version: "1.0", CoveragePct: 20, FreshnessPct: 40, TrendDelta90d: 20, ScopeActivated: true, TrendHistory: true},
		{FrameworkID: uuid.New(), Name: "Framework C", Version: "4", CoveragePct: 100, ScopeActivated: false},
	}
	postures := packPostures(rows)
	coverage, _, scoped, _ := coverageTrend(postures)
	return Brief{
		PeriodEnd:   "2026-10-31",
		GeneratedAt: "2026-11-01T09:00:00Z",
		Frameworks:  postures,
		Coverage:    &CoverageSummary{CoveragePct: coverage, ScopedFrameworkCount: scoped, EvidenceFreshnessRatePct: 67},
		Drift:       DriftSummary{WindowDays: 30, Since: "2026-10-01", Through: "2026-10-31"},
	}
}

func TestBrief_CoverageIsThePacksFigure(t *testing.T) {
	b := postureBrief()
	if b.Coverage.CoveragePct != 47.5 || b.Coverage.ScopedFrameworkCount != 2 {
		t.Fatalf("coverage = %+v, want 47.5 over 2 scoped frameworks", *b.Coverage)
	}
}

func TestRenderNarrative_PerFrameworkPosture(t *testing.T) {
	md, err := RenderNarrative(postureBrief())
	if err != nil {
		t.Fatalf("RenderNarrative: %v", err)
	}
	for _, want := range []string{
		"Requirement coverage averages 47.5% across the 2 frameworks with an activated scope.",
		"- **Framework A 2022** — in-progress, 75% requirement coverage (up +50 pts over 90 days, from 25%).",
		"- **Framework B 1.0** — at-risk, 20% requirement coverage (up +20 pts over 90 days, from 0%).",
		"- **Framework C 4** — Scope not set: no framework scope is activated, so no coverage is reported.",
		"Evidence freshness rate (tenant-wide): 67% of controls have fresh evidence.",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("narrative missing %q\ngot:\n%s", want, md)
		}
	}
	if strings.Contains(md, "100%") {
		t.Errorf("the unscoped framework's coverage leaked into the narrative:\n%s", md)
	}
}

func TestRenderNarrative_NoScopedFrameworkIsNotZeroPercent(t *testing.T) {
	b := postureBrief()
	b.Frameworks = b.Frameworks[2:]
	b.Coverage = &CoverageSummary{EvidenceFreshnessRatePct: 67}
	md, err := RenderNarrative(b)
	if err != nil {
		t.Fatalf("RenderNarrative: %v", err)
	}
	if !strings.Contains(md, "No framework has an activated scope, so no coverage figure is reported.") {
		t.Errorf("narrative does not say no framework is scoped:\n%s", md)
	}
	if strings.Contains(md, "0%") || strings.Contains(md, "averages") {
		t.Errorf("narrative states a coverage figure with no scoped framework:\n%s", md)
	}
}

func TestBuildBriefHTML_PerFrameworkPosture(t *testing.T) {
	out := buildBriefHTML(StoredBrief{Content: postureBrief()})
	for _, want := range []string{
		"Requirement coverage averages 47.5% across the 2 framework(s) with an activated scope.",
		"Framework C 4</div><div class=\"pct\">Scope not set",
		"Evidence freshness rate (tenant-wide): 67% of controls have fresh evidence.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("brief HTML missing %q", want)
		}
	}
	if strings.Contains(out, "evidence freshness rate (tenant-wide)</div>") {
		t.Error("brief HTML labels a framework card with the legacy tenant-wide figure")
	}

	b := postureBrief()
	b.Frameworks = b.Frameworks[2:]
	b.Coverage = &CoverageSummary{EvidenceFreshnessRatePct: 67}
	out = buildBriefHTML(StoredBrief{Content: b})
	if !strings.Contains(out, "No framework has an activated scope, so no coverage figure is reported.") || strings.Contains(out, ">0%") {
		t.Error("brief HTML with no scoped framework must say so and print no 0%")
	}
}

// legacyBriefJSON is a brief as slice 031 stored it: integer figures, the
// 30-day drift count as the delta, and no coverage object.
const legacyBriefJSON = `{
  "period_end": "2026-04-30",
  "generated_at": "2026-05-01T09:00:00Z",
  "frameworks": [
    {"slug": "soc2", "name": "SOC 2", "coverage_pct": 94, "freshness_pct": 88, "trend_arrow": "up", "delta": 2, "state": "audit-ready"}
  ],
  "drift": {"window_days": 30, "since": "2026-03-31", "through": "2026-04-30", "delta": 2, "flipped_out_count": 0},
  "top_risks": [],
  "exceptions": {"active_count": 0, "past_due_count": 0, "oldest_active_age_days": 0}
}`

func TestLegacyBrief_DecodesAndRendersAsStored(t *testing.T) {
	var b Brief
	if err := json.Unmarshal([]byte(legacyBriefJSON), &b); err != nil {
		t.Fatalf("decode legacy brief: %v", err)
	}
	if b.Coverage != nil {
		t.Fatalf("legacy brief decoded a coverage object: %+v", b.Coverage)
	}
	md, err := RenderNarrative(b)
	if err != nil {
		t.Fatalf("RenderNarrative: %v", err)
	}
	want := "We are in audit-ready state for SOC 2 (94% evidence freshness rate (tenant-wide), up +2 pts over 30 days). Controls with fresh evidence 88% (tenant-wide)."
	if !strings.Contains(md, want) {
		t.Errorf("legacy narrative changed\nwant substring: %q\ngot:\n%s", want, md)
	}
	if strings.Contains(md, "Requirement coverage") || strings.Contains(md, "Evidence freshness rate (tenant-wide):") {
		t.Errorf("legacy brief rendered with the #2210 posture wording:\n%s", md)
	}
	out := buildBriefHTML(StoredBrief{Content: b})
	if !strings.Contains(out, `<div class="pct">94%</div><div class="meta">evidence freshness rate (tenant-wide)</div>`) ||
		!strings.Contains(out, "up +2 pts · controls fresh 88%") {
		t.Errorf("legacy brief HTML changed:\n%s", out)
	}
}
