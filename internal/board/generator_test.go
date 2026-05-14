// Unit tests for the board-brief generator's pure logic (slice 031):
// program-posture math, risk ranking, and severity extraction. These are
// deterministic transforms — no DB, no network, no LLM.

package board

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/freshness"
)

// programPosture: freshnessPct is fresh/total; coveragePct is
// not-stale-with-evidence / with-evidence.
func TestProgramPosture(t *testing.T) {
	rows := []freshness.ControlFreshness{
		{IsStale: false, EvidenceCount: 3}, // fresh, has evidence -> covered
		{IsStale: false, EvidenceCount: 1}, // fresh, has evidence -> covered
		{IsStale: true, EvidenceCount: 2},  // stale, has evidence -> not covered
		{IsStale: true, EvidenceCount: 0},  // no evidence -> stale, not covered, not in coverage denominator
	}
	coverage, fresh := programPosture(rows)
	// fresh = 2/4 = 50%.
	if fresh != 50 {
		t.Errorf("freshnessPct = %d, want 50", fresh)
	}
	// coverage = covered-with-evidence (2) / with-evidence (3) = 66.67 -> 67%.
	if coverage != 67 {
		t.Errorf("coveragePct = %d, want 67", coverage)
	}
}

// programPosture: zero controls is an honest 0%/0% — a program with no
// controls has nothing covered and nothing fresh.
func TestProgramPosture_NoControls(t *testing.T) {
	coverage, fresh := programPosture(nil)
	if coverage != 0 || fresh != 0 {
		t.Errorf("programPosture(nil) = (%d, %d), want (0, 0)", coverage, fresh)
	}
}

// programPosture: controls exist but none have evidence -> coverage 0%
// (not a divide-by-zero), freshness reflects the not-stale set.
func TestProgramPosture_NoEvidence(t *testing.T) {
	rows := []freshness.ControlFreshness{
		{IsStale: true, EvidenceCount: 0},
		{IsStale: true, EvidenceCount: 0},
	}
	coverage, fresh := programPosture(rows)
	if coverage != 0 {
		t.Errorf("coveragePct = %d, want 0 (no evidence anywhere)", coverage)
	}
	if fresh != 0 {
		t.Errorf("freshnessPct = %d, want 0 (all stale)", fresh)
	}
}

// trendFromDelta maps the signed drift delta to a trend token.
func TestTrendFromDelta(t *testing.T) {
	cases := map[int]string{5: TrendUp, -3: TrendDown, 0: TrendFlat}
	for delta, want := range cases {
		if got := trendFromDelta(delta); got != want {
			t.Errorf("trendFromDelta(%d) = %q, want %q", delta, got, want)
		}
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

// extractSeverity pulls a ranking scalar from the methodology-dependent
// score JSONB shapes.
func TestExtractSeverity(t *testing.T) {
	cases := []struct {
		name string
		blob string
		want float64
	}{
		{"residual score field", `{"score": 12.5, "warning": ""}`, 12.5},
		{"aggregated severity field", `{"severity": 20, "child_count": 3}`, 20},
		{"likelihood times impact", `{"likelihood": 4, "impact": 5}`, 20},
		{"empty blob", ``, 0},
		{"unrecognized shape", `{"foo": "bar"}`, 0},
		{"malformed json", `{not json`, 0},
	}
	for _, c := range cases {
		got := extractSeverity([]byte(c.blob))
		if got != c.want {
			t.Errorf("%s: extractSeverity(%q) = %v, want %v", c.name, c.blob, got, c.want)
		}
	}
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
