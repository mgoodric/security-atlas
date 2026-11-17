// Unit tests for the shared residual reader (#2185): ScoreValue,
// SeverityBand, Monetary, RanksAbove and the residual,age sort built on them.
//
// Pure functions — no DB, no build tag.
package risk

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

func TestScoreValueReadsEveryShape(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		raw    string
		want   float64
		wantOK bool
	}{
		{"deriver blob", `{"score":12,"inherent_score":20,"weighted_control_effectiveness":0.4,"breakdown":[]}`, 12, true},
		{"score wins over likelihood/impact", `{"score":3,"likelihood":5,"impact":5}`, 3, true},
		{"fully mitigated", `{"score":0}`, 0, true},
		{"precomputed severity", `{"severity":16}`, 16, true},
		{"5x5 pair", `{"likelihood":3,"impact":4}`, 12, true},
		{"null score falls through", `{"score":null,"severity":9}`, 9, true},
		{"empty object", `{}`, 0, false},
		{"partial pair", `{"likelihood":3}`, 0, false},
		{"non-numeric score", `{"score":"high"}`, 0, false},
		{"not JSON", `nope`, 0, false},
		{"empty", ``, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := ScoreValue([]byte(tc.raw))
			if got != tc.want || ok != tc.wantOK {
				t.Fatalf("ScoreValue(%s) = (%v, %v), want (%v, %v)", tc.raw, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestSeverityBand(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   float64
		want string
	}{
		{0, "None"},
		{-1, "None"},
		{0.01, "Low"},
		{0.4, "Low"},
		{1, "Low"},
		{7.9, "Low"},
		{8, "Medium"},
		{14.9, "Medium"},
		{15, "High"},
		{25, "High"},
	}
	for _, tc := range cases {
		if got := SeverityBand(tc.in); got != tc.want {
			t.Errorf("SeverityBand(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMonetaryOnlyFAIR(t *testing.T) {
	t.Parallel()
	for _, m := range []dbx.RiskMethodology{
		dbx.RiskMethodologyNist80030, dbx.RiskMethodologyQualitative5x5,
		dbx.RiskMethodologyCisRam, dbx.RiskMethodologyIso27005,
	} {
		if Monetary(m) {
			t.Errorf("Monetary(%q) = true, want false", m)
		}
	}
	if !Monetary(dbx.RiskMethodologyFair) {
		t.Error("Monetary(fair) = false, want true")
	}
}

// A FAIR risk's dollar residual never outranks a 5x5 severity by raw
// number: $250,000 sits below a residual of 2.
func TestSortByResidualThenAgeRanksFAIRSeparately(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mk := func(title string, m dbx.RiskMethodology, residual string, ageDays int) Risk {
		return Risk{
			ID:            uuid.New(),
			Title:         title,
			Methodology:   m,
			ResidualScore: []byte(residual),
			CreatedAt:     base.Add(-time.Duration(ageDays) * 24 * time.Hour),
		}
	}
	risks := []Risk{
		mk("fair-big", dbx.RiskMethodologyFair, `{"score":250000}`, 1),
		mk("5x5-low", dbx.RiskMethodologyNist80030, `{"score":2}`, 1),
		mk("fair-small", dbx.RiskMethodologyFair, `{"score":900}`, 1),
		mk("5x5-high", dbx.RiskMethodologyQualitative5x5, `{"likelihood":4,"impact":5}`, 1),
		mk("5x5-unscored", dbx.RiskMethodologyNist80030, `{}`, 5),
	}
	sortByResidualThenAge(risks)
	want := []string{"5x5-high", "5x5-low", "5x5-unscored", "fair-big", "fair-small"}
	got := make([]string, len(risks))
	for i, r := range risks {
		got[i] = r.Title
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}
