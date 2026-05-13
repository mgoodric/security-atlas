package risk_test

import (
	"errors"
	"math"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/risk"
)

// ----- ComputeSeverity -----

func TestComputeSeverity_Max(t *testing.T) {
	t.Parallel()
	scores := []risk.ChildScore{
		{Likelihood: 3, Impact: 5}, // 15
		{Likelihood: 4, Impact: 3}, // 12
		{Likelihood: 3, Impact: 3}, // 9
	}
	got, err := risk.ComputeSeverity(risk.SeverityFunctionMax, scores)
	if err != nil {
		t.Fatalf("ComputeSeverity: %v", err)
	}
	if got != 15 {
		t.Fatalf("max severity: got %d, want 15", got)
	}
}

func TestComputeSeverity_WeightedMax_ThreeChildren(t *testing.T) {
	t.Parallel()
	// max(15, 12, 9) = 15; weight = 1 + log10(3) ≈ 1.477; raw ≈ 22.16; ceil = 23.
	scores := []risk.ChildScore{
		{Likelihood: 3, Impact: 5},
		{Likelihood: 4, Impact: 3},
		{Likelihood: 3, Impact: 3},
	}
	got, err := risk.ComputeSeverity(risk.SeverityFunctionWeightedMax, scores)
	if err != nil {
		t.Fatalf("ComputeSeverity: %v", err)
	}
	expected := int(math.Ceil(15.0 * (1 + math.Log10(3))))
	if got != expected {
		t.Fatalf("weighted_max severity: got %d, want %d", got, expected)
	}
	if got != 23 {
		t.Fatalf("weighted_max expected concrete fixture 23, got %d", got)
	}
}

func TestComputeSeverity_WeightedMax_SingleChild(t *testing.T) {
	t.Parallel()
	// log10(1) = 0, weight = 1, so result equals the single child's severity.
	scores := []risk.ChildScore{{Likelihood: 4, Impact: 4}}
	got, err := risk.ComputeSeverity(risk.SeverityFunctionWeightedMax, scores)
	if err != nil {
		t.Fatalf("ComputeSeverity: %v", err)
	}
	if got != 16 {
		t.Fatalf("weighted_max single child: got %d, want 16", got)
	}
}

func TestComputeSeverity_WeightedMax_CapsAtScaleMax(t *testing.T) {
	t.Parallel()
	// max=25, N=10 -> 25 * (1 + log10(10)) = 50 -> cap to 25.
	scores := make([]risk.ChildScore, 10)
	for i := range scores {
		scores[i] = risk.ChildScore{Likelihood: 5, Impact: 5}
	}
	got, err := risk.ComputeSeverity(risk.SeverityFunctionWeightedMax, scores)
	if err != nil {
		t.Fatalf("ComputeSeverity: %v", err)
	}
	if got != 25 {
		t.Fatalf("weighted_max cap: got %d, want 25", got)
	}
}

func TestComputeSeverity_Sum(t *testing.T) {
	t.Parallel()
	scores := []risk.ChildScore{
		{Likelihood: 2, Impact: 3}, // 6
		{Likelihood: 2, Impact: 3}, // 6
		{Likelihood: 2, Impact: 3}, // 6
	}
	got, err := risk.ComputeSeverity(risk.SeverityFunctionSum, scores)
	if err != nil {
		t.Fatalf("ComputeSeverity: %v", err)
	}
	if got != 18 {
		t.Fatalf("sum severity: got %d, want 18", got)
	}
}

func TestComputeSeverity_Sum_CapsAtScaleMax(t *testing.T) {
	t.Parallel()
	scores := []risk.ChildScore{
		{Likelihood: 5, Impact: 5}, // 25
		{Likelihood: 3, Impact: 3}, // 9
	}
	got, err := risk.ComputeSeverity(risk.SeverityFunctionSum, scores)
	if err != nil {
		t.Fatalf("ComputeSeverity: %v", err)
	}
	if got != 25 {
		t.Fatalf("sum cap: got %d, want 25", got)
	}
}

func TestComputeSeverity_EmptyChildren(t *testing.T) {
	t.Parallel()
	_, err := risk.ComputeSeverity(risk.SeverityFunctionMax, nil)
	if !errors.Is(err, risk.ErrEmptyChildren) {
		t.Fatalf("expected ErrEmptyChildren, got %v", err)
	}
}

func TestComputeSeverity_UnknownFunction(t *testing.T) {
	t.Parallel()
	_, err := risk.ComputeSeverity("not_a_real_one", []risk.ChildScore{{Likelihood: 1, Impact: 1}})
	if !errors.Is(err, risk.ErrUnknownSeverityFunction) {
		t.Fatalf("expected ErrUnknownSeverityFunction, got %v", err)
	}
}

// ----- DeriveGridCell -----

func TestDeriveGridCell_KnownPoints(t *testing.T) {
	t.Parallel()
	cases := []struct {
		s, l, i int
	}{
		{25, 5, 5},
		{20, 5, 4},
		{18, 5, 4}, // ceil(sqrt(18))=5; ceil(18/5)=4
		{15, 4, 4}, // ceil(sqrt(15))=4; ceil(15/4)=4
		{12, 4, 3}, // ceil(sqrt(12))=4; ceil(12/4)=3
		{9, 3, 3},
		{1, 1, 1},
		{0, 1, 1},
		{26, 5, 5}, // capped to 25 then derived
	}
	for _, c := range cases {
		gotL, gotI := risk.DeriveGridCell(c.s)
		if gotL != c.l || gotI != c.i {
			t.Errorf("DeriveGridCell(%d): got (%d,%d), want (%d,%d)", c.s, gotL, gotI, c.l, c.i)
		}
	}
}

// ----- AggregationKey -----

func TestAggregationKey_Stable(t *testing.T) {
	t.Parallel()
	a := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	b := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	c := uuid.MustParse("33333333-3333-3333-3333-333333333333")

	// Same title + same set in different order -> same key.
	k1 := risk.AggregationKey("title-a", []uuid.UUID{a, b, c})
	k2 := risk.AggregationKey("title-a", []uuid.UUID{c, a, b})
	if k1 != k2 {
		t.Fatalf("aggregation key order-dependent: %s vs %s", k1, k2)
	}
	if len(k1) != 64 {
		t.Fatalf("expected hex sha256 (64 chars), got %d: %s", len(k1), k1)
	}

	// Different title -> different key.
	k3 := risk.AggregationKey("title-b", []uuid.UUID{a, b, c})
	if k3 == k1 {
		t.Fatalf("different titles produced same key")
	}

	// Different child set -> different key.
	k4 := risk.AggregationKey("title-a", []uuid.UUID{a, b})
	if k4 == k1 {
		t.Fatalf("different child set produced same key")
	}
}

// ----- IsAggregableMethodology -----

func TestIsAggregableMethodology(t *testing.T) {
	t.Parallel()
	if !risk.IsAggregableMethodology(dbx.RiskMethodologyNist80030) {
		t.Fatalf("nist_800_30 should be aggregable")
	}
	if !risk.IsAggregableMethodology(dbx.RiskMethodologyQualitative5x5) {
		t.Fatalf("qualitative_5x5 should be aggregable")
	}
	if risk.IsAggregableMethodology(dbx.RiskMethodologyFair) {
		t.Fatalf("fair should not be aggregable in v1")
	}
	if risk.IsAggregableMethodology(dbx.RiskMethodologyCisRam) {
		t.Fatalf("cis_ram should not be aggregable in v1")
	}
	if risk.IsAggregableMethodology(dbx.RiskMethodologyIso27005) {
		t.Fatalf("iso_27005 should not be aggregable in v1")
	}
}
