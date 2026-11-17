// Unit tests for the board-brief generator's pure logic (slice 031):
// program-posture math, risk ranking, and severity extraction. These are
// deterministic transforms — no DB, no network, no LLM.

package board

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/freshness"
)

// evidenceFreshnessRate is fresh / total controls, rounded: stale controls
// count against it whether or not they have evidence.
func TestEvidenceFreshnessRate(t *testing.T) {
	rows := []freshness.ControlFreshness{
		{IsStale: false, EvidenceCount: 3},
		{IsStale: false, EvidenceCount: 1},
		{IsStale: true, EvidenceCount: 2},
	}
	// 2/3 = 66.67 -> 67%.
	if got := evidenceFreshnessRate(rows); got != 67 {
		t.Errorf("evidenceFreshnessRate = %d, want 67", got)
	}
}

// evidenceFreshnessRate: zero controls is 0%, not a divide-by-zero.
func TestEvidenceFreshnessRate_NoControls(t *testing.T) {
	if got := evidenceFreshnessRate(nil); got != 0 {
		t.Errorf("evidenceFreshnessRate(nil) = %d, want 0", got)
	}
}

// stateFromCoverage maps coverage thresholds to posture labels.
func TestStateFromCoverage(t *testing.T) {
	cases := map[int]string{
		95: "audit-ready",
		90: "audit-ready",
		89: "in-progress",
		70: "in-progress",
		69: "at-risk",
		0:  "at-risk",
	}
	for pct, want := range cases {
		if got := stateFromCoverage(pct); got != want {
			t.Errorf("stateFromCoverage(%d) = %q, want %q", pct, got, want)
		}
	}
}

// rankTopRisks keeps FAIR (dollar) residuals out of the 5x5 ranking
// (#2185): every 5x5 risk ranks above every FAIR risk, so a $250,000 FAIR
// residual never outranks a residual severity of 2 by raw number. Among
// FAIR risks the larger exposure ranks first.
func TestRankTopRisks_FAIRNeverOutranks5x5(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC)
	mk := func(title string, m dbx.RiskMethodology, residual string) riskRow {
		return riskRow{
			ID: uuid.New(), Title: title, Methodology: m,
			ResidualScore: []byte(residual), UpdatedAt: now,
		}
	}
	rows := []riskRow{
		mk("fair big", dbx.RiskMethodologyFair, `{"score": 250000}`),
		mk("5x5 low", dbx.RiskMethodologyNist80030, `{"score": 2}`),
		mk("fair small", dbx.RiskMethodologyFair, `{"score": 900}`),
		mk("5x5 high", dbx.RiskMethodologyQualitative5x5, `{"likelihood": 4, "impact": 5}`),
	}
	top := rankTopRisks(rows, now, 4)
	want := []string{"5x5 high", "5x5 low", "fair big", "fair small"}
	for i, w := range want {
		if top[i].Title != w {
			t.Fatalf("top[%d] = %q, want %q (order %v)", i, top[i].Title, w, titles(top))
		}
	}
	if top[2].Methodology != "fair" || !top[2].Monetary() || top[2].Band() != "" {
		t.Errorf("FAIR row: methodology=%q monetary=%v band=%q, want fair/true/\"\"",
			top[2].Methodology, top[2].Monetary(), top[2].Band())
	}
	if got := top[2].ResidualLabel(); got != "$250,000" {
		t.Errorf("FAIR ResidualLabel = %q, want $250,000", got)
	}
	if got := top[0].ResidualLabel(); got != "20.0 High" {
		t.Errorf("5x5 ResidualLabel = %q, want \"20.0 High\"", got)
	}
}

// #2511: a risk with no score at all ranks after every scored risk, FAIR
// included, prints as Unscored rather than a severity of 0, and the brief
// renders it.
func TestRankTopRisks_UnscoredLast(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC)
	mk := func(title string, m dbx.RiskMethodology, inherent, residual string, age int) riskRow {
		return riskRow{
			ID: uuid.New(), Title: title, Methodology: m,
			InherentScore: []byte(inherent), ResidualScore: []byte(residual),
			UpdatedAt: now.AddDate(0, 0, -age),
		}
	}
	rows := []riskRow{
		mk("unscored old", dbx.RiskMethodologyNist80030, `{}`, `{}`, 90),
		mk("fair small", dbx.RiskMethodologyFair, `{}`, `{"score": 900}`, 1),
		mk("unscored nil", dbx.RiskMethodologyFair, ``, ``, 1),
		mk("5x5 low", dbx.RiskMethodologyNist80030, `{"likelihood":1,"impact":1}`, `{}`, 1),
	}
	top := rankTopRisks(rows, now, 4)
	want := []string{"5x5 low", "fair small", "unscored old", "unscored nil"}
	for i, w := range want {
		if top[i].Title != w {
			t.Fatalf("top[%d] = %q, want %q (order %v)", i, top[i].Title, w, titles(top))
		}
	}
	if top[0].Unscored || top[1].Unscored || !top[2].Unscored || !top[3].Unscored {
		t.Fatalf("Unscored flags %v %v %v %v, want false false true true",
			top[0].Unscored, top[1].Unscored, top[2].Unscored, top[3].Unscored)
	}
	if got := top[2].ResidualLabel(); got != "Unscored" {
		t.Errorf("unscored ResidualLabel = %q, want Unscored", got)
	}
	if got := top[3].Band(); got != "" {
		t.Errorf("unscored Band = %q, want empty", got)
	}
	md, err := RenderNarrative(Brief{TopRisks: top})
	if err != nil {
		t.Fatalf("RenderNarrative: %v", err)
	}
	if !strings.Contains(md, "**unscored old** (") || !strings.Contains(md, "— unscored, open 90 day(s)") {
		t.Errorf("narrative does not print the unscored risk as unscored:\n%s", md)
	}
}

func titles(rs []RiskAging) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Title
	}
	return out
}

// rankTopRisks: ranks by residual severity DESC, then age DESC, and keeps
// the top N.
func TestRankTopRisks(t *testing.T) {
	now := time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC)
	mk := func(title string, residual, inherent string, updatedDaysAgo int) riskRow {
		return riskRow{
			ID:            uuid.New(),
			Title:         title,
			Category:      "operational",
			Treatment:     "mitigate",
			ResidualScore: []byte(residual),
			InherentScore: []byte(inherent),
			UpdatedAt:     now.AddDate(0, 0, -updatedDaysAgo),
		}
	}
	rows := []riskRow{
		mk("low severity", `{"score": 4}`, `{"likelihood":2,"impact":2}`, 10),
		mk("high severity new", `{"score": 20}`, `{"likelihood":5,"impact":4}`, 5),
		mk("high severity old", `{"score": 20}`, `{"likelihood":5,"impact":4}`, 100),
		mk("mid severity", `{"score": 12}`, `{"likelihood":3,"impact":4}`, 50),
	}
	top := rankTopRisks(rows, now, 3)
	if len(top) != 3 {
		t.Fatalf("rankTopRisks returned %d, want 3", len(top))
	}
	// Highest severity, oldest first within a severity tie.
	if top[0].Title != "high severity old" {
		t.Errorf("top[0] = %q, want 'high severity old' (sev 20, age 100)", top[0].Title)
	}
	if top[1].Title != "high severity new" {
		t.Errorf("top[1] = %q, want 'high severity new' (sev 20, age 5)", top[1].Title)
	}
	if top[2].Title != "mid severity" {
		t.Errorf("top[2] = %q, want 'mid severity' (sev 12)", top[2].Title)
	}
	// "low severity" (sev 4) is dropped — only top 3 survive.
	for _, r := range top {
		if r.Title == "low severity" {
			t.Error("low-severity risk should have been dropped from the top 3")
		}
	}
	// AgeDays is computed from updated_at.
	if top[0].AgeDays != 100 {
		t.Errorf("top[0].AgeDays = %d, want 100", top[0].AgeDays)
	}
}

// rankTopRisks: residual of 0 falls back to inherent severity.
func TestRankTopRisks_FallsBackToInherent(t *testing.T) {
	now := time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC)
	rows := []riskRow{
		{
			ID: uuid.New(), Title: "no residual yet",
			Category: "strategic", Treatment: "accept",
			ResidualScore: []byte(`{}`),
			InherentScore: []byte(`{"likelihood": 5, "impact": 5}`),
			UpdatedAt:     now.AddDate(0, 0, -3),
		},
	}
	top := rankTopRisks(rows, now, 3)
	if len(top) != 1 {
		t.Fatalf("rankTopRisks returned %d, want 1", len(top))
	}
	// Falls back to inherent: 5 * 5 = 25.
	if top[0].ResidualSeverity != 25 {
		t.Errorf("ResidualSeverity = %v, want 25 (inherent fallback)", top[0].ResidualSeverity)
	}
}

// rankTopRisks: an empty register returns an empty slice, not nil-panic.
func TestRankTopRisks_Empty(t *testing.T) {
	top := rankTopRisks(nil, time.Now(), 3)
	if len(top) != 0 {
		t.Errorf("rankTopRisks(nil) returned %d rows, want 0", len(top))
	}
}
