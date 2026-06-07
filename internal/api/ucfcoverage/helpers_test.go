package ucfcoverage

// helpers_test.go — slice 482 AC-8: pure-Go unit coverage of the rollup
// formula + band-threshold branches with NO database (slice 353 Q-2
// convention: fast t.Parallel table tests exercise the pure branches; the
// integration tier in integration_test.go is the safety net for the
// RLS-scoped DB behaviour AC-6 / AC-7).

import "testing"

func TestRollupCoverageStrength(t *testing.T) {
	t.Parallel()
	const eps = 1e-9
	tests := []struct {
		name    string
		anchors []anchorCoverage
		wantVal float64
		wantAny bool
	}{
		{
			name:    "empty → uncovered (0, false)",
			anchors: nil,
			wantVal: 0,
			wantAny: false,
		},
		{
			// The canvas §3.2 worked example, verbatim: ISO→anchor at 0.8,
			// anchor evidence at 1.0 → requirement covered at 0.8.
			name: "canvas single-anchor weakest-link example = 0.8",
			anchors: []anchorCoverage{
				{edgeStrength: 0.8, anchorCover: 1.0, hasCoverage: true},
			},
			wantVal: 0.8,
			wantAny: true,
		},
		{
			// Anchor evidence below 1.0 multiplies through.
			name: "single anchor edge 0.8 × cover 0.5 = 0.4",
			anchors: []anchorCoverage{
				{edgeStrength: 0.8, anchorCover: 0.5, hasCoverage: true},
			},
			wantVal: 0.4,
			wantAny: true,
		},
		{
			// Best-satisfying-path: the stronger of two paths wins (MAX).
			name: "two anchors → MAX path (0.9×0.9=0.81 > 0.8×0.5=0.4)",
			anchors: []anchorCoverage{
				{edgeStrength: 0.8, anchorCover: 0.5, hasCoverage: true},
				{edgeStrength: 0.9, anchorCover: 0.9, hasCoverage: true},
			},
			wantVal: 0.81,
			wantAny: true,
		},
		{
			// A no-coverage anchor never drags the MAX down to 0.
			name: "no-coverage anchor ignored, covered anchor wins",
			anchors: []anchorCoverage{
				{edgeStrength: 1.0, anchorCover: 0.0, hasCoverage: false},
				{edgeStrength: 0.7, anchorCover: 1.0, hasCoverage: true},
			},
			wantVal: 0.7,
			wantAny: true,
		},
		{
			// ALL anchors lack coverage → uncovered (0, false), NOT 0-covered.
			name: "all anchors no-coverage → uncovered",
			anchors: []anchorCoverage{
				{edgeStrength: 1.0, anchorCover: 0.5, hasCoverage: false},
				{edgeStrength: 0.8, anchorCover: 0.9, hasCoverage: false},
			},
			wantVal: 0,
			wantAny: false,
		},
		{
			// Covered but failing: in-scope control, 0% pass rate → 0 score
			// but hasAny=true (distinct from uncovered).
			name: "covered-but-failing: cover 0 with hasCoverage true",
			anchors: []anchorCoverage{
				{edgeStrength: 1.0, anchorCover: 0.0, hasCoverage: true},
			},
			wantVal: 0,
			wantAny: true,
		},
		{
			// Out-of-range inputs clamp to [0,1].
			name: "clamps out-of-range edge/cover to [0,1]",
			anchors: []anchorCoverage{
				{edgeStrength: 1.4, anchorCover: 1.2, hasCoverage: true},
			},
			wantVal: 1.0,
			wantAny: true,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotVal, gotAny := rollupCoverageStrength(tt.anchors)
			if gotAny != tt.wantAny {
				t.Fatalf("hasAny = %v; want %v", gotAny, tt.wantAny)
			}
			if d := gotVal - tt.wantVal; d < -eps || d > eps {
				t.Fatalf("score = %v; want %v", gotVal, tt.wantVal)
			}
		})
	}
}

func TestClassifyBand(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		score  float64
		hasAny bool
		want   ConfidenceBand
	}{
		{"no coverage → uncovered", 0, false, BandUncovered},
		{"covered-but-failing 0.0 → weak (not uncovered)", 0, true, BandWeak},
		{"just below weak ceiling → weak", 0.49, true, BandWeak},
		{"at weak/partial boundary 0.5 → partial", 0.5, true, BandPartial},
		{"mid partial → partial", 0.7, true, BandPartial},
		{"just below partial/strong boundary → partial", 0.79, true, BandPartial},
		{"at partial/strong boundary 0.8 → strong (canvas example floor)", 0.8, true, BandStrong},
		{"full coverage 1.0 → strong", 1.0, true, BandStrong},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := classifyBand(tt.score, tt.hasAny); got != tt.want {
				t.Fatalf("classifyBand(%v, %v) = %q; want %q", tt.score, tt.hasAny, got, tt.want)
			}
		})
	}
}

func TestClamp01(t *testing.T) {
	t.Parallel()
	cases := map[float64]float64{-0.5: 0, 0: 0, 0.5: 0.5, 1: 1, 1.7: 1}
	for in, want := range cases {
		if got := clamp01(in); got != want {
			t.Fatalf("clamp01(%v) = %v; want %v", in, got, want)
		}
	}
}
