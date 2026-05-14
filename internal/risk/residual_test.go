// Unit tests for slice 020 residual-risk derivation math (residual.go).
//
// Pure functions — no DB, no build tag, runs under plain `go test`. The
// integration tests (residual_integration_test.go, api/risks) exercise the
// DB + NATS wiring; these isolate the canvas §6.2 formula itself.
package risk_test

import (
	"math"
	"testing"

	"github.com/mgoodric/security-atlas/internal/risk"
)

const eps = 1e-9

func approx(t *testing.T, got, want float64, label string) {
	t.Helper()
	if math.Abs(got-want) > eps {
		t.Fatalf("%s: got %v, want %v", label, got, want)
	}
}

// ----- ControlEffectiveness (AC-3, ISC-9, ISC-10) -----

func TestControlEffectiveness_CanvasFormula(t *testing.T) {
	// canvas §6.2: wD*design + wO*operational + wC*coverage.
	// 0.3*1.0 + 0.5*0.4 + 0.2*1.0 = 0.3 + 0.2 + 0.2 = 0.7
	got := risk.ControlEffectiveness(risk.EffectivenessComponents{
		DesignScore:      1.0,
		OperationalScore: 0.4,
		CoverageScore:    1.0,
		WeightDesign:     0.3,
		WeightOperation:  0.5,
		WeightCoverage:   0.2,
	})
	approx(t, got, 0.7, "ISC-9 control_effectiveness")
}

func TestControlEffectiveness_AllZeroComponents(t *testing.T) {
	got := risk.ControlEffectiveness(risk.EffectivenessComponents{
		WeightDesign: 0.3, WeightOperation: 0.5, WeightCoverage: 0.2,
	})
	approx(t, got, 0.0, "zero components")
}

func TestControlEffectiveness_ClampsAtOne(t *testing.T) {
	// ISC-10: a weight set summing past 1 with full scores cannot push
	// effectiveness past 1.0 — the clamp holds.
	got := risk.ControlEffectiveness(risk.EffectivenessComponents{
		DesignScore: 1.0, OperationalScore: 1.0, CoverageScore: 1.0,
		WeightDesign: 1.0, WeightOperation: 1.0, WeightCoverage: 1.0,
	})
	approx(t, got, 1.0, "ISC-10 clamp at 1")
}

func TestControlEffectiveness_FullyEffective(t *testing.T) {
	got := risk.ControlEffectiveness(risk.EffectivenessComponents{
		DesignScore: 1.0, OperationalScore: 1.0, CoverageScore: 1.0,
		WeightDesign: 0.3, WeightOperation: 0.5, WeightCoverage: 0.2,
	})
	approx(t, got, 1.0, "fully effective control")
}

// ----- WeightedEffectiveness (ISC-12, ISC-13) -----

func TestWeightedEffectiveness_MeanAcrossControls(t *testing.T) {
	// ISC-12: aggregate is the MEAN, not the sum — two 0.6 controls stay 0.6.
	got := risk.WeightedEffectiveness([]float64{0.6, 0.6})
	approx(t, got, 0.6, "ISC-12 mean of two equal controls")

	got = risk.WeightedEffectiveness([]float64{0.2, 0.8})
	approx(t, got, 0.5, "ISC-12 mean of 0.2 and 0.8")
}

func TestWeightedEffectiveness_EmptyIsZero(t *testing.T) {
	// ISC-13: no linked controls -> zero aggregate effectiveness.
	got := risk.WeightedEffectiveness(nil)
	approx(t, got, 0.0, "ISC-13 empty slice")
	got = risk.WeightedEffectiveness([]float64{})
	approx(t, got, 0.0, "ISC-13 zero-length slice")
}

func TestWeightedEffectiveness_ClampsAtOne(t *testing.T) {
	got := risk.WeightedEffectiveness([]float64{1.0, 1.0, 1.0})
	approx(t, got, 1.0, "mean of three fully-effective controls")
}

// ----- ResidualScore (AC-7, ISC-11, ISC-14, ISC-15) -----

func TestResidualScore_CanvasFormula(t *testing.T) {
	// ISC-11: residual = inherent * (1 - weighted_effectiveness).
	// inherent 16 (likelihood 4 x impact 4), weighted_effectiveness 0.7
	// -> 16 * 0.3 = 4.8
	got := risk.ResidualScore(16, 0.7)
	approx(t, got, 4.8, "ISC-11 residual formula")
}

func TestResidualScore_NoControlsEqualsInherent(t *testing.T) {
	// ISC-13 / AC-7: zero effectiveness -> residual equals inherent.
	got := risk.ResidualScore(16, 0.0)
	approx(t, got, 16, "ISC-13 residual equals inherent")
}

func TestResidualScore_FullyEffectiveDropsToZero(t *testing.T) {
	got := risk.ResidualScore(25, 1.0)
	approx(t, got, 0.0, "fully effective -> residual 0")
}

func TestResidualScore_NeverNegative(t *testing.T) {
	// ISC-14: an over-1 effectiveness (defensively clamped) never drives
	// residual below 0.
	got := risk.ResidualScore(16, 1.5)
	approx(t, got, 0.0, "ISC-14 residual never negative")
}

func TestResidualScore_NeverExceedsInherent(t *testing.T) {
	// ISC-15: a negative effectiveness (defensively clamped to 0) never
	// pushes residual above inherent — controls only reduce risk.
	got := risk.ResidualScore(16, -0.5)
	approx(t, got, 16, "ISC-15 residual never exceeds inherent")
}

func TestResidualScore_PartialEffectiveness(t *testing.T) {
	got := risk.ResidualScore(20, 0.25)
	approx(t, got, 15, "20 * 0.75")
}
