package ucfcoverage

// helpers_test.go — slice 482 AC-8: pure-Go unit coverage of the rollup
// formula + band-threshold branches with NO database (slice 353 Q-2
// convention: fast t.Parallel table tests exercise the pure branches; the
// integration tier in integration_test.go is the safety net for the
// RLS-scoped DB behaviour AC-6 / AC-7).

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

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

// TestOverlayFrom covers the #1800 overlay provenance mapper: only an
// overlay row with a joined source gets provenance; a catalog row never does.
// #2240: an overlay row reports no mapping_tier; a catalog row reports its own.
func TestTierFor(t *testing.T) {
	t.Parallel()
	tests := []struct{ origin, tier, want string }{
		{"overlay", "verified", ""},
		{"catalog", "verified", "verified"},
		{"catalog", "draft", "draft"},
		{"", "under_review", "under_review"},
	}
	for _, tc := range tests {
		if got := tierFor(tc.origin, tc.tier); got != tc.want {
			t.Errorf("tierFor(%q, %q) = %q; want %q", tc.origin, tc.tier, got, tc.want)
		}
	}
}

func TestOverlayFrom(t *testing.T) {
	t.Parallel()
	src, by := "FY25 SOC 2 report, section 4", "user-1"
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.FixedZone("x", 3600))
	ts := pgtype.Timestamptz{Time: at, Valid: true}

	if got := overlayFrom("catalog", &src, &by, ts); got != nil {
		t.Errorf("catalog row: got %+v, want nil", got)
	}
	if got := overlayFrom("overlay", nil, &by, ts); got != nil {
		t.Errorf("overlay row without a joined source: got %+v, want nil", got)
	}
	got := overlayFrom("overlay", &src, &by, ts)
	if got == nil || got.SourceRef != src || got.CreatedBy != by || !got.CreatedAt.Equal(at) || got.CreatedAt.Location() != time.UTC {
		t.Errorf("overlay row: got %+v", got)
	}
	if got := overlayFrom("overlay", &src, nil, pgtype.Timestamptz{}); got == nil || got.CreatedBy != "" || !got.CreatedAt.IsZero() {
		t.Errorf("overlay row with null author/date: got %+v", got)
	}
}
