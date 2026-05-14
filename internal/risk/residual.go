// residual.go — slice 020 residual-risk derivation math.
//
// Pure functions, no DB, no I/O — every function here is a deterministic
// transform of its inputs so the residual formula is unit-testable in
// isolation (the integration tests exercise the DB + NATS wiring separately).
//
// The canvas §6.2 formula:
//
//	control_effectiveness = weight_design     * design_score
//	                      + weight_operation  * operational_score
//	                      + weight_coverage   * coverage_score
//
//	residual_score = inherent_score * (1 - weighted_control_effectiveness)
//
// where `weighted_control_effectiveness` is the mean of the per-control
// `control_effectiveness` values across every control linked to the risk. A
// risk with no linked controls has no effectiveness to subtract, so its
// residual equals its inherent score (AC-7).
//
// Constitutional invariant #2: nothing here reads or writes evidence. The
// component scores are supplied by the caller — `operational_score` comes
// from slice 012's evaluation ledger (rolling pass rate), `coverage_score`
// from slice 017's applicability set intersected with passing cells,
// `design_score` is the human-set value on the link row.
package risk

import "math"

// EffectivenessComponents is the slice 020 per-control effectiveness input:
// the three component scores plus the three weights (all [0,1]). DesignScore
// is the human-set value on the risk_control_links row; OperationalScore and
// CoverageScore are derived at read time.
type EffectivenessComponents struct {
	DesignScore      float64
	OperationalScore float64
	CoverageScore    float64
	WeightDesign     float64
	WeightOperation  float64
	WeightCoverage   float64
}

// ControlEffectiveness computes the canvas §6.2 weighted control-effectiveness
// score for one linked control. The result is clamped to [0,1]: with every
// component and weight in [0,1] the raw value is already bounded by
// (wD+wO+wC), so a weight set that sums to >1 is the only way to exceed 1 —
// the clamp keeps a mis-configured weight set from producing a negative
// residual. DB CHECK constraints bound each input to [0,1] independently;
// the clamp is the in-Go defense-in-depth peer.
func ControlEffectiveness(c EffectivenessComponents) float64 {
	raw := c.WeightDesign*c.DesignScore +
		c.WeightOperation*c.OperationalScore +
		c.WeightCoverage*c.CoverageScore
	return clamp01(raw)
}

// WeightedEffectiveness is the mean of the per-control effectiveness values
// across every control linked to a risk. The mean (not the sum) is the right
// aggregation: linking a second equally-effective control should not push
// effectiveness past 1.0 and drive residual negative. An empty slice returns
// 0 — a risk with no linked controls has zero aggregate effectiveness, so its
// residual equals its inherent score (AC-7).
func WeightedEffectiveness(perControl []float64) float64 {
	if len(perControl) == 0 {
		return 0
	}
	sum := 0.0
	for _, e := range perControl {
		sum += e
	}
	return clamp01(sum / float64(len(perControl)))
}

// ResidualScore applies the canvas §6.2 residual formula:
//
//	residual = inherent * (1 - weighted_effectiveness)
//
// `weightedEffectiveness` is clamped to [0,1] defensively so the result is
// always in [0, inherent]: a fully-effective control set drives residual to
// 0; a zero-effectiveness set (or no controls) leaves residual at inherent.
// Residual never exceeds inherent and never goes negative — controls only
// ever reduce risk in this model, never amplify it.
func ResidualScore(inherent, weightedEffectiveness float64) float64 {
	residual := inherent * (1 - clamp01(weightedEffectiveness))
	if residual < 0 {
		return 0
	}
	if residual > inherent {
		return inherent
	}
	return residual
}

// clamp01 bounds x to the closed interval [0,1].
func clamp01(x float64) float64 {
	if math.IsNaN(x) || x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}
