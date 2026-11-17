package board

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// #2056: each framework keeps its own coverage, freshness and trend — the
// dashboard row's numbers, verbatim — and "audit-ready" is judged against
// that framework's own coverage, not a tenant-wide figure.
func TestPackPostures_EachFrameworkKeepsItsOwnNumbers(t *testing.T) {
	t.Parallel()
	soc2, iso := uuid.New(), uuid.New()
	got := packPostures([]frameworkPostureRow{
		{FrameworkID: soc2, Slug: "soc2", Name: "SOC 2", Version: "2017", CoveragePct: 41.9, FreshnessPct: 60, TrendDelta90d: 12.5, ScopeActivated: true, TrendHistory: true},
		{FrameworkID: iso, Slug: "iso27001", Name: "ISO 27001", Version: "2022", CoveragePct: 93.33, FreshnessPct: 80, TrendDelta90d: -2, ScopeActivated: true, TrendHistory: true},
	})
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2", len(got))
	}
	a, b := got[0], got[1]
	if a.CoveragePct != 41.9 || a.FreshnessPct != 60 || a.Delta != 12.5 || a.BaselinePct != 29.4 {
		t.Errorf("SOC 2 row = %+v, want coverage 41.9, freshness 60, delta 12.5, baseline 29.4", a)
	}
	if a.State != "at-risk" || a.TrendArrow != TrendUp || a.Version != "2017" || a.FrameworkID != soc2.String() {
		t.Errorf("SOC 2 row = %+v, want at-risk / up / 2017", a)
	}
	if b.CoveragePct != 93.33 || b.Delta != -2 || b.BaselinePct != 95.33 {
		t.Errorf("ISO row = %+v, want coverage 93.33, delta -2, baseline 95.33", b)
	}
	if b.State != "audit-ready" || b.TrendArrow != TrendDown {
		t.Errorf("ISO row = %+v, want audit-ready / down", b)
	}
}

// 89.99 is below the 90-point line: the state never rounds a framework up
// into audit-ready.
func TestPackPostures_AuditReadyLineIsNotRoundedUp(t *testing.T) {
	t.Parallel()
	got := packPostures([]frameworkPostureRow{
		{Name: "SOC 2", CoveragePct: 89.99, ScopeActivated: true},
		{Name: "ISO", CoveragePct: 90, ScopeActivated: true},
	})
	if got[0].State != "in-progress" || got[1].State != "audit-ready" {
		t.Errorf("states = %q, %q; want in-progress, audit-ready", got[0].State, got[1].State)
	}
}

// #2071 B3: no activated framework scope -> "Scope not set", no percentage.
func TestPackPostures_NoActivatedScopeIsScopeNotSet(t *testing.T) {
	t.Parallel()
	got := packPostures([]frameworkPostureRow{
		{Name: "PCI DSS", Version: "4.0", CoveragePct: 100, FreshnessPct: 100, TrendDelta90d: 100, ScopeActivated: false},
	})[0]
	if !got.ScopeNotSet || got.State != PostureScopeNotSet {
		t.Fatalf("row = %+v, want scope-not-set", got)
	}
	if got.CoveragePct != 0 || got.FreshnessPct != 0 || got.Delta != 0 || got.BaselinePct != 0 {
		t.Errorf("scope-not-set row carries numbers: %+v", got)
	}
}

// The coverage_trend baseline comes from the posture trend (coverage minus
// the 90-day delta), averaged over the scoped frameworks; unscoped
// frameworks stay out of it. No hard-coded 0.
func TestCoverageTrend_BaselineFromPostureTrend(t *testing.T) {
	t.Parallel()
	postures := packPostures([]frameworkPostureRow{
		{Name: "SOC 2", CoveragePct: 60, TrendDelta90d: 20, ScopeActivated: true, TrendHistory: true}, // baseline 40
		{Name: "ISO", CoveragePct: 80, TrendDelta90d: -10, ScopeActivated: true, TrendHistory: true},  // baseline 90
		{Name: "PCI", CoveragePct: 100, TrendDelta90d: 100, ScopeActivated: false},                    // left out
	})
	cov, base, scoped, ok := coverageTrend(postures)
	if scoped != 2 || cov != 70 || base != 65 || !ok {
		t.Errorf("coverageTrend = (%v, %v, %d, %v), want (70, 65, 2, true)", cov, base, scoped, ok)
	}
	if cov, base, scoped, ok := coverageTrend(postures[2:]); cov != 0 || base != 0 || scoped != 0 || ok {
		t.Errorf("coverageTrend(unscoped only) = (%v, %v, %d, %v), want zeros, no baseline", cov, base, scoped, ok)
	}
}

// Review B1: no evaluation on or before the 90-day cutoff. The query's delta
// then equals the coverage, which would read "up from 0%" — #2056's own
// symptom. The row reports no baseline and no delta instead.
func TestPackPostures_NoTrendHistoryHasNoBaseline(t *testing.T) {
	t.Parallel()
	got := packPostures([]frameworkPostureRow{
		{Name: "SOC 2", Version: "2017", CoveragePct: 75, TrendDelta90d: 75, ScopeActivated: true, TrendHistory: false},
	})[0]
	if !got.NoTrendHistory || got.BaselinePct != 0 || got.Delta != 0 || got.TrendArrow != TrendFlat {
		t.Errorf("row = %+v, want no_trend_history with no baseline or delta", got)
	}
	if got.CoveragePct != 75 || got.State != "in-progress" {
		t.Errorf("row = %+v, want its own 75%% coverage and state kept", got)
	}
	sec := newSection(SectionPosture, SectionData{Frameworks: []PackFrameworkPosture{got}})
	text, err := renderSectionNarrative(sec, "2026-09-30")
	if err != nil {
		t.Fatalf("renderSectionNarrative: %v", err)
	}
	if !strings.Contains(text, "SOC 2 2017 at 75% coverage (in-progress, no baseline yet: less than 90 days of evaluation history)") {
		t.Errorf("posture narrative = %q", text)
	}
	if strings.Contains(text, "from 0%") || strings.Contains(text, "+75") {
		t.Errorf("posture narrative reports a 0%% baseline: %q", text)
	}
	sp := newDraftPack()
	sp.Content.Sections[SectionPosture] = sec
	if html := buildPackHTML(sp); !strings.Contains(html, "no baseline yet: less than 90 days of evaluation history · in-progress") {
		t.Errorf("PDF posture card missing the no-baseline line")
	}
}

// One scoped framework without history means the section has no baseline:
// a mean over real and missing baselines would understate the start.
func TestCoverageTrend_AnyFrameworkWithoutHistoryMeansNoBaseline(t *testing.T) {
	t.Parallel()
	postures := packPostures([]frameworkPostureRow{
		{Name: "SOC 2", CoveragePct: 60, TrendDelta90d: 20, ScopeActivated: true, TrendHistory: true},
		{Name: "ISO", CoveragePct: 80, TrendDelta90d: 80, ScopeActivated: true, TrendHistory: false},
	})
	cov, base, scoped, ok := coverageTrend(postures)
	if ok || cov != 70 || base != 0 || scoped != 2 {
		t.Errorf("coverageTrend = (%v, %v, %d, %v), want (70, 0, 2, false)", cov, base, scoped, ok)
	}
}

// No baseline -> no delta on either section, and the investment cost per
// point is n/a in the narrative and the PDF. An operator-entered baseline
// restores the delta and the cost.
func TestRecomputeDerived_NoBaselineMakesCostNA(t *testing.T) {
	t.Parallel()
	p := Pack{PeriodEnd: "2026-09-30", Sections: map[string]Section{
		SectionCoverageTrend: newSection(SectionCoverageTrend, SectionData{CoveragePct: 70, ScopedFrameworkCount: intPtr(2), NoBaseline: true}),
		SectionInvestment:    newSection(SectionInvestment, SectionData{SpendUSD: 30000}),
	}}
	recomputeDerived(p.Sections)
	trend, inv := p.Sections[SectionCoverageTrend].Data, p.Sections[SectionInvestment].Data
	if trend.CoverageDelta != 0 || inv.CoverageDelta != 0 || inv.CostPerCoveragePoint != 0 || !inv.NoBaseline {
		t.Fatalf("trend %+v / investment %+v, want no delta and n/a cost", trend, inv)
	}
	for key, want := range map[string]string{
		SectionCoverageTrend: "with no baseline yet: less than 90 days of evaluation history, so no change is reported",
		SectionInvestment:    "Cost per coverage point: n/a — no baseline yet: less than 90 days of evaluation history.",
	} {
		text, err := renderSectionNarrative(p.Sections[key], p.PeriodEnd)
		if err != nil {
			t.Fatalf("renderSectionNarrative(%s): %v", key, err)
		}
		if !strings.Contains(text, want) {
			t.Errorf("%s narrative = %q, want it to contain %q", key, text, want)
		}
	}
	html := buildPackHTML(StoredPack{Content: p})
	for _, want := range []string{
		"Coverage 70% · baseline n/a (no baseline yet: less than 90 days of evaluation history)",
		"Spend $30000 · cost per coverage point n/a",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("PDF HTML missing %q", want)
		}
	}

	baseline := 50
	out := applySectionEdit(p, SectionEdit{SectionKey: SectionCoverageTrend, Inputs: &SectionInputs{BaselineCoveragePct: &baseline}})
	trend, inv = out.Sections[SectionCoverageTrend].Data, out.Sections[SectionInvestment].Data
	if trend.NoBaseline || trend.CoverageDelta != 20 || inv.NoBaseline || inv.CostPerCoveragePoint != 1500 {
		t.Errorf("after operator baseline: trend %+v / investment %+v, want delta 20, cost 1500", trend, inv)
	}
}

// Review N2: spend with no scoped framework is n/a, not spend / 0 points,
// even after an operator enters a baseline.
func TestRecomputeDerived_NoScopedFrameworkMakesCostNA(t *testing.T) {
	t.Parallel()
	baseline := 40
	p := Pack{PeriodEnd: "2026-09-30", Sections: map[string]Section{
		SectionCoverageTrend: newSection(SectionCoverageTrend, SectionData{NoBaseline: true, ScopedFrameworkCount: intPtr(0)}),
		SectionInvestment:    newSection(SectionInvestment, SectionData{SpendUSD: 30000}),
	}}
	out := applySectionEdit(p, SectionEdit{SectionKey: SectionCoverageTrend, Inputs: &SectionInputs{BaselineCoveragePct: &baseline}})
	inv := out.Sections[SectionInvestment]
	if !inv.Data.NoBaseline || inv.Data.CostPerCoveragePoint != 0 {
		t.Errorf("investment = %+v, want n/a cost with no scoped framework", inv.Data)
	}
	if !strings.Contains(inv.TemplatedText, "Cost per coverage point: n/a — no framework has an activated scope.") {
		t.Errorf("investment narrative = %q", inv.TemplatedText)
	}
}

// The posture narrative names each framework's own percentage, its trend
// baseline, and "scope not set" where there is no activated scope.
func TestRenderSectionNarrative_PerFrameworkPosture(t *testing.T) {
	t.Parallel()
	sec := newSection(SectionPosture, SectionData{Frameworks: packPostures([]frameworkPostureRow{
		{Name: "SOC 2", Version: "2017", CoveragePct: 41.93, TrendDelta90d: 12.5, ScopeActivated: true, TrendHistory: true},
		{Name: "ISO 27001", Version: "2022", CoveragePct: 49.3, TrendDelta90d: 0, ScopeActivated: true, TrendHistory: true},
		{Name: "PCI DSS", Version: "4.0", ScopeActivated: false},
	})})
	text, err := renderSectionNarrative(sec, "2026-09-30")
	if err != nil {
		t.Fatalf("renderSectionNarrative: %v", err)
	}
	for _, want := range []string{
		"SOC 2 2017 at 41.9% coverage (at-risk, up +12.5 pts over 90 days, from 29.4%)",
		"ISO 27001 2022 at 49.3% coverage (at-risk, flat 0 pts over 90 days, from 49.3%)",
		"PCI DSS 4.0 has no activated framework scope (scope not set)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("posture narrative missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "100%") {
		t.Errorf("posture narrative carries a tenant-wide 100%%: %s", text)
	}
}

// The coverage_trend narrative states the trend baseline, not a 0% one.
func TestRenderSectionNarrative_CoverageTrendFromPosture(t *testing.T) {
	t.Parallel()
	sec := newSection(SectionCoverageTrend, SectionData{
		CoveragePct: 70, BaselineCoveragePct: 65, CoverageDelta: 5, ScopedFrameworkCount: intPtr(2),
	})
	text, err := renderSectionNarrative(sec, "2026-09-30")
	if err != nil {
		t.Fatalf("renderSectionNarrative: %v", err)
	}
	want := "Control coverage averages 70% across the 2 frameworks with an activated scope, against a baseline of 65% — a +5 point change."
	if !strings.Contains(text, want) {
		t.Errorf("coverage trend narrative = %q, want it to contain %q", text, want)
	}
	empty, _ := renderSectionNarrative(newSection(SectionCoverageTrend, SectionData{ScopedFrameworkCount: intPtr(0)}), "2026-09-30")
	if !strings.Contains(empty, "No framework has an activated scope") {
		t.Errorf("empty coverage trend narrative = %q", empty)
	}
}

// The posture card in the PDF shows "Scope not set" in place of a number.
func TestBuildPackHTML_ScopeNotSetCard(t *testing.T) {
	t.Parallel()
	sp := newDraftPack()
	posture := sp.Content.Sections[SectionPosture]
	posture.Data.Frameworks = packPostures([]frameworkPostureRow{
		{Name: "PCI DSS", Version: "4.0", ScopeActivated: false},
		{Name: "SOC 2", Version: "2017", CoveragePct: 41.93, TrendDelta90d: 1, ScopeActivated: true, TrendHistory: true},
	})
	sp.Content.Sections[SectionPosture] = posture
	got := buildPackHTML(sp)
	if !strings.Contains(got, `PCI DSS 4.0</div><div class="pct">Scope not set</div>`) {
		t.Errorf("PDF HTML missing the scope-not-set card")
	}
	if !strings.Contains(got, `SOC 2 2017</div><div class="pct">41.9%</div>`) {
		t.Errorf("PDF HTML missing SOC 2's own 41.9%% card")
	}
}

// #2112: the share covered only by exception is carried from the posture
// row and reported beside the coverage, in the narrative and the PDF card.
// A framework with no exception coverage says nothing about exceptions.
func TestPackPostures_CoveredByExceptionIsReportedSeparately(t *testing.T) {
	t.Parallel()
	sp := newDraftPack()
	posture := sp.Content.Sections[SectionPosture]
	posture.Data.Frameworks = packPostures([]frameworkPostureRow{
		{Name: "SOC 2", Version: "2017", CoveragePct: 75, ExceptionPct: 25, TrendDelta90d: 25, ScopeActivated: true, TrendHistory: true},
		{Name: "ISO 27001", Version: "2022", CoveragePct: 50, TrendDelta90d: 0, ScopeActivated: true, TrendHistory: true},
	})
	if got := posture.Data.Frameworks[0].CoveredByExceptionPct; got != 25 {
		t.Fatalf("CoveredByExceptionPct = %v, want 25", got)
	}
	sp.Content.Sections[SectionPosture] = posture

	text, err := renderSectionNarrative(posture, "2026-09-30")
	if err != nil {
		t.Fatalf("renderSectionNarrative: %v", err)
	}
	if want := "SOC 2 2017 at 75% coverage (25% of requirements covered by exception, in-progress"; !strings.Contains(text, want) {
		t.Errorf("posture narrative missing %q:\n%s", want, text)
	}
	if want := "ISO 27001 2022 at 50% coverage (at-risk,"; !strings.Contains(text, want) {
		t.Errorf("posture narrative missing %q:\n%s", want, text)
	}

	html := buildPackHTML(sp)
	if !strings.Contains(html, `<div class="muted">25% covered by exception</div>`) {
		t.Errorf("PDF HTML missing the covered-by-exception line")
	}
	if strings.Count(html, "covered by exception</div>") != 1 {
		t.Errorf("PDF HTML reports exception coverage on a framework that has none")
	}
}

// Top-risk residuals in the pack are the register's: the same field order
// (score, then severity, then likelihood x impact — risk.ScoreValue
// and web/app/(authed)/risks/filters.ts residualSeverity), on the 1..25
// scale with no division, and the register's band labels.
func TestTopRisks_ResidualMatchesRegisterScaleAndBand(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		residual string
		want     float64
		band     string
	}{
		// The residual deriver's shape: inherent x (1 - effectiveness).
		{"derived residual", `{"score": 20, "inherent_score": 25, "control_effectiveness": 0.2}`, 20, "High"},
		{"aggregated parent", `{"severity": 9.5}`, 9.5, "Medium"},
		{"likelihood x impact", `{"likelihood": 2, "impact": 3}`, 6, "Low"},
	}
	for _, c := range cases {
		rows := []riskRow{{ID: uuid.New(), Title: c.name, ResidualScore: []byte(c.residual)}}
		got := rankTopRisks(rows, time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC), 3)[0]
		if got.ResidualSeverity != c.want {
			t.Errorf("%s: residual = %v, want %v (the register's 1..25 value)", c.name, got.ResidualSeverity, c.want)
		}
		if band := got.Band(); band != c.band {
			t.Errorf("%s: band = %q, want %q", c.name, band, c.band)
		}
		sec := newSection(SectionTopRisks, SectionData{TopRisks: []RiskAging{got}})
		text, err := renderSectionNarrative(sec, "2026-09-30")
		if err != nil {
			t.Fatalf("renderSectionNarrative: %v", err)
		}
		if !strings.Contains(text, "of 25, "+c.band) {
			t.Errorf("%s: top-risks narrative missing the 1..25 scale and band: %q", c.name, text)
		}
	}
}

// A FAIR top risk is printed in dollars with no band in the pack
// narrative, the pack PDF and the brief PDF; a 5x5 one carries its band in
// all three (#2185). Titles are still HTML-escaped in the PDFs.
func TestTopRisks_FAIRInDollarsAndBandInEveryRender(t *testing.T) {
	t.Parallel()
	risks := []RiskAging{
		{ID: "a", Title: "Patch <lag>", Category: "ops", Treatment: "mitigate", ResidualSeverity: 12, AgeDays: 3},
		{ID: "b", Title: "Wire fraud", Category: "fin", Treatment: "mitigate", Methodology: "fair", ResidualSeverity: 1250000, AgeDays: 9},
	}
	sec := newSection(SectionTopRisks, SectionData{TopRisks: risks})
	text, err := renderSectionNarrative(sec, "2026-09-30")
	if err != nil {
		t.Fatalf("renderSectionNarrative: %v", err)
	}
	for _, want := range []string{"residual severity 12.0 of 25, Medium", "FAIR residual annualized loss exposure $1,250,000"} {
		if !strings.Contains(text, want) {
			t.Errorf("pack narrative missing %q: %q", want, text)
		}
	}
	if strings.Contains(text, "1250000.0") {
		t.Errorf("pack narrative prints the FAIR dollars as a severity: %q", text)
	}

	var w strings.Builder
	writeSectionData(&w, SectionTopRisks, SectionData{TopRisks: risks})
	pdf := w.String()
	for _, want := range []string{"<td>12.0 Medium</td>", "<td>$1,250,000</td>", "Patch &lt;lag&gt;"} {
		if !strings.Contains(pdf, want) {
			t.Errorf("pack PDF missing %q: %s", want, pdf)
		}
	}

	brief := buildBriefHTML(StoredBrief{PeriodEnd: "2026-09-30", Content: Brief{TopRisks: risks}})
	for _, want := range []string{"<td>12.0 Medium</td>", "<td>$1,250,000</td>", "Patch &lt;lag&gt;"} {
		if !strings.Contains(brief, want) {
			t.Errorf("brief PDF missing %q", want)
		}
	}
	if strings.Contains(brief, "Patch <lag>") {
		t.Error("brief PDF does not escape the risk title")
	}

	md, err := RenderNarrative(Brief{TopRisks: risks})
	if err != nil {
		t.Fatalf("RenderNarrative: %v", err)
	}
	for _, want := range []string{"residual severity 12.0 (Medium)", "FAIR residual annualized loss exposure $1,250,000"} {
		if !strings.Contains(md, want) {
			t.Errorf("brief narrative missing %q: %q", want, md)
		}
	}
}

func TestSignedPtsAndFormatPct(t *testing.T) {
	t.Parallel()
	for v, want := range map[float64]string{12.5: "+12.5", -3: "-3", 0: "0", -0.04: "0", 0.04: "0", 41.93: "+41.9"} {
		if got := signedPts(v); got != want {
			t.Errorf("signedPts(%v) = %q, want %q", v, got, want)
		}
	}
	for v, want := range map[float64]string{41.93: "41.9", 100: "100", 0: "0", 49.25: "49.3"} {
		if got := formatPct(v); got != want {
			t.Errorf("formatPct(%v) = %q, want %q", v, got, want)
		}
	}
}

func intPtr(n int) *int { return &n }

// Second review, item 1: a coverage_trend section stored before #2056 has no
// scoped_framework_count. Its PDF and narrative must show the stored
// figures, not "No framework has an activated scope", and editing the
// draft must not turn its investment cost into n/a.
func TestLegacyCoverageTrend_RendersStoredFigures(t *testing.T) {
	t.Parallel()
	var legacy SectionData
	if err := json.Unmarshal([]byte(`{"coverage_pct":92,"baseline_coverage_pct":89,"coverage_delta":3}`), &legacy); err != nil {
		t.Fatalf("decode legacy section: %v", err)
	}
	if !legacy.ScopeCountUnknown() || legacy.NoScopedFrameworks() {
		t.Fatalf("legacy section = %+v, want an unknown scope count", legacy)
	}

	sp := newDraftPack()
	trend := sp.Content.Sections[SectionCoverageTrend]
	trend.Data = legacy
	sp.Content.Sections[SectionCoverageTrend] = trend
	html := buildPackHTML(sp)
	if strings.Contains(html, "No framework has an activated scope") {
		t.Errorf("legacy PDF says no framework is scoped over real figures")
	}
	if !strings.Contains(html, "Coverage 92% · baseline 89% · delta +3 pts") {
		t.Errorf("legacy PDF missing the stored figures")
	}

	text, err := renderSectionNarrative(newSection(SectionCoverageTrend, legacy), "2026-03-31")
	if err != nil {
		t.Fatalf("renderSectionNarrative: %v", err)
	}
	if want := "Control coverage stands at 92% at quarter end, against a baseline of 89% — a +3 point change."; !strings.Contains(text, want) {
		t.Errorf("legacy narrative = %q, want %q", text, want)
	}

	p := Pack{PeriodEnd: "2026-03-31", Sections: map[string]Section{
		SectionCoverageTrend: newSection(SectionCoverageTrend, legacy),
		SectionInvestment:    newSection(SectionInvestment, SectionData{}),
	}}
	spend := 30000
	out := applySectionEdit(p, SectionEdit{SectionKey: SectionInvestment, Inputs: &SectionInputs{SpendUSD: &spend}})
	if inv := out.Sections[SectionInvestment].Data; inv.NoBaseline || inv.CostPerCoveragePoint != 10000 {
		t.Errorf("legacy investment = %+v, want cost 10000 (30000 / 3 pts)", inv)
	}
}
