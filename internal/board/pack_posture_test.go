package board

import (
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
		{FrameworkID: soc2, Slug: "soc2", Name: "SOC 2", Version: "2017", CoveragePct: 41.9, FreshnessPct: 60, TrendDelta90d: 12.5, ScopeActivated: true},
		{FrameworkID: iso, Slug: "iso27001", Name: "ISO 27001", Version: "2022", CoveragePct: 93.33, FreshnessPct: 80, TrendDelta90d: -2, ScopeActivated: true},
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
		{Name: "SOC 2", CoveragePct: 60, TrendDelta90d: 20, ScopeActivated: true},  // baseline 40
		{Name: "ISO", CoveragePct: 80, TrendDelta90d: -10, ScopeActivated: true},   // baseline 90
		{Name: "PCI", CoveragePct: 100, TrendDelta90d: 100, ScopeActivated: false}, // left out
	})
	cov, base, scoped := coverageTrend(postures)
	if scoped != 2 || cov != 70 || base != 65 {
		t.Errorf("coverageTrend = (%v, %v, %d), want (70, 65, 2)", cov, base, scoped)
	}
	if cov, base, scoped := coverageTrend(postures[2:]); cov != 0 || base != 0 || scoped != 0 {
		t.Errorf("coverageTrend(unscoped only) = (%v, %v, %d), want zeros", cov, base, scoped)
	}
}

// The posture narrative names each framework's own percentage, its trend
// baseline, and "scope not set" where there is no activated scope.
func TestRenderSectionNarrative_PerFrameworkPosture(t *testing.T) {
	t.Parallel()
	sec := newSection(SectionPosture, SectionData{Frameworks: packPostures([]frameworkPostureRow{
		{Name: "SOC 2", Version: "2017", CoveragePct: 41.93, TrendDelta90d: 12.5, ScopeActivated: true},
		{Name: "ISO 27001", Version: "2022", CoveragePct: 49.3, TrendDelta90d: 0, ScopeActivated: true},
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
		CoveragePct: 70, BaselineCoveragePct: 65, CoverageDelta: 5, ScopedFrameworkCount: 2,
	})
	text, err := renderSectionNarrative(sec, "2026-09-30")
	if err != nil {
		t.Fatalf("renderSectionNarrative: %v", err)
	}
	want := "Control coverage averages 70% across the 2 frameworks with an activated scope, against a baseline of 65% — a +5 point change."
	if !strings.Contains(text, want) {
		t.Errorf("coverage trend narrative = %q, want it to contain %q", text, want)
	}
	empty, _ := renderSectionNarrative(newSection(SectionCoverageTrend, SectionData{}), "2026-09-30")
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
		{Name: "SOC 2", Version: "2017", CoveragePct: 41.93, TrendDelta90d: 1, ScopeActivated: true},
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

// Top-risk residuals in the pack are the register's: the same field order
// (score, then severity, then likelihood x impact — risk.residualMagnitude
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
		if band := severityBandLabel(got.ResidualSeverity); band != c.band {
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

// Band boundaries match the register's: High from 15, Medium from 8, Low
// from 1.
func TestSeverityBandLabel(t *testing.T) {
	t.Parallel()
	for sev, want := range map[float64]string{25: "High", 15: "High", 14.9: "Medium", 8: "Medium", 7.9: "Low", 1: "Low", 0.8: "None", 0: "None"} {
		if got := severityBandLabel(sev); got != want {
			t.Errorf("severityBandLabel(%v) = %q, want %q", sev, got, want)
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
