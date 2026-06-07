package ucfcoverage

// rollup.go — slice 482: per-requirement coverage-strength rollup +
// confidence-band classification.
//
// The canvas §3.2 headline promise: "if your evidence covers SCF:IAC-22
// with strength 1.0, and ISO27001:A.9.4.2 → SCF:IAC-22 with strength
// 0.8, the ISO requirement is covered at 0.8, and the UI surfaces the
// gap." This file generalizes that worked example to the multi-anchor
// case and assigns the gap a named confidence band.
//
// The formula (JUDGMENT — see docs/audit-log/482-coverage-strength-
// rollup-decisions.md):
//
//	per-anchor coverage term = edge_strength × anchor_coverage
//	requirement coverage_strength = MAX over anchors of (per-anchor term)
//
// where anchor_coverage is the best (max) of the tenant's evaluated
// effectiveness over every RLS-scoped control anchored on that anchor,
// and only when that control's framework_version is in FrameworkScope
// (mirrors slice 256's `applyCoverage` per-control rule). The "best
// satisfying path" (MAX, not MIN/sum) keeps the canvas weakest-link
// shape — a single anchor at 0.8 yields 0.8 — while letting a stronger
// alternate mapping raise the score rather than the weakest one capping
// it. The single-anchor case reduces EXACTLY to the canvas example.
//
// All functions here are pure (no DB, no context) so the formula +
// band-threshold branches get fast table-driven unit coverage with no
// Postgres (slice 353 Q-2 pure-Go convention; AC-8).

// ConfidenceBand is the named bucket a numeric coverage_strength falls
// into. The labels are a JUDGMENT call recorded in the decisions log and
// flagged "Revisit once in use" — real auditor feedback tunes the
// thresholds.
type ConfidenceBand string

const (
	// BandUncovered: no tenant-evaluated coverage contributes at all
	// (no in-scope control with effectiveness data on any anchor). This
	// is the threat-model-I default a foreign-tenant requirement must
	// resolve to — empty, NOT another tenant's value.
	BandUncovered ConfidenceBand = "uncovered"
	// BandWeak: some coverage exists but the gap is large.
	BandWeak ConfidenceBand = "weak"
	// BandPartial: meaningful but incomplete coverage.
	BandPartial ConfidenceBand = "partial"
	// BandStrong: the requirement is well covered.
	BandStrong ConfidenceBand = "strong"
)

// Band threshold cut points (JUDGMENT — decisions log, low confidence).
// A score of exactly 0 (or no contributing path) is uncovered; the
// remaining (0,1] range is split into three bands. The cut points
// (0.5 / 0.8) pattern-match the canvas §3.2 example: the worked-example
// 0.8 reads as the floor of "strong" — an ISO requirement covered at 0.8
// is strongly (not perfectly) covered, and the UI still "surfaces the
// gap" via the 0.2 shortfall shown alongside the band.
const (
	bandWeakCeil    = 0.5 // (0, 0.5)   → weak
	bandPartialCeil = 0.8 // [0.5, 0.8) → partial
	// [0.8, 1.0] → strong
)

// anchorCoverage is one anchor's contribution to a requirement rollup:
// the STRM edge strength from the requirement to the anchor, and the
// best tenant-evaluated coverage of that anchor (edge_strength is NOT
// pre-multiplied here; RollupCoverageStrength does the multiply so the
// inputs stay independently testable).
//
// HasCoverage distinguishes "an in-scope control with effectiveness data
// exists on this anchor" (true) from "no such control" (false). A false
// HasCoverage anchor contributes nothing to the rollup — it never drags
// the MAX down to 0, and a requirement whose anchors are ALL false
// resolves to uncovered (band), not 0.0-but-covered.
type anchorCoverage struct {
	edgeStrength float64
	anchorCover  float64
	hasCoverage  bool
}

// RollupCoverageStrength computes the requirement-level coverage_strength
// over its anchors using the best-satisfying-path formula. Returns
// (score, hasAnyCoverage). When no anchor contributes (every anchor has
// hasCoverage=false, or the slice is empty), it returns (0, false) — the
// caller maps that to the uncovered band, NOT a covered-at-0.0 value
// (threat-model I: a foreign-tenant requirement must read uncovered, not
// a real 0).
func rollupCoverageStrength(anchors []anchorCoverage) (float64, bool) {
	best := 0.0
	any := false
	for _, a := range anchors {
		if !a.hasCoverage {
			continue
		}
		any = true
		term := clamp01(a.edgeStrength) * clamp01(a.anchorCover)
		if term > best {
			best = term
		}
	}
	if !any {
		return 0, false
	}
	return best, true
}

// classifyBand maps a (score, hasAnyCoverage) pair to a ConfidenceBand.
// hasAnyCoverage=false always yields uncovered regardless of score (the
// score is 0 in that case). A genuine 0.0 score WITH coverage (e.g. an
// in-scope control whose 30-day pass rate is exactly 0) classifies as
// weak, not uncovered — "covered but failing" is a real, distinct state
// from "not covered at all" (the slice 256 P0-256-1 distinction, carried
// up to the rollup).
func classifyBand(score float64, hasAnyCoverage bool) ConfidenceBand {
	if !hasAnyCoverage {
		return BandUncovered
	}
	switch {
	case score < bandWeakCeil:
		return BandWeak
	case score < bandPartialCeil:
		return BandPartial
	default:
		return BandStrong
	}
}

// clamp01 bounds a float to [0,1] so an out-of-range DB value (a strength
// stored slightly above 1.0 by float rounding, say) can't produce a
// rollup outside the documented range.
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
