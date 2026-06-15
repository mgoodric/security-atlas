package boardnarrative

import (
	"sort"
	"strconv"

	"github.com/mgoodric/security-atlas/internal/board"
)

// Rollup is the DETERMINISTIC pre-computation for the control-coverage-summary
// section — the ground truth the model grounds on (guardrail 1) AND the ground
// truth every number in the draft is checked against (guardrail 5). It is a
// pure projection of the existing board.Brief (the frozen monthly-brief data
// path) plus the bounded set of tenant-owned controls/evidence the operator can
// cite. NO LLM, no inference — a pure function of the Brief + the candidate
// excerpts.
//
// The defining property: every NUMBER the section is permitted to state appears
// here as an allowed value (AllowedNumbers). The numeric-verification gate
// parses the numbers out of the model draft and rejects the draft if ANY number
// is not in this set. The model cannot introduce a number the deterministic
// pre-computation did not produce — that is what makes a fabricated statistic
// impossible to surface to the board.
type Rollup struct {
	// PeriodEnd labels the rollup (YYYY-MM-DD), echoed from the Brief.
	PeriodEnd string `json:"period_end"`

	// CoveragePct / FreshnessPct are the program-wide posture numbers from the
	// Brief (identical across frameworks in v1 — board decisions log D3). These
	// are the headline numbers the coverage section reports.
	CoveragePct  int `json:"coverage_pct"`
	FreshnessPct int `json:"freshness_pct"`

	// Delta is the signed 30-day control-drift count; FlippedOutCount is how
	// many controls drifted OUT of passing in the window. Both from the Brief's
	// drift summary.
	Delta           int `json:"delta"`
	FlippedOutCount int `json:"flipped_out_count"`

	// WindowDays is the drift lookback window (30 for the monthly brief).
	WindowDays int `json:"window_days"`

	// FrameworkCount is the number of frameworks the program runs against —
	// a citable count the section may state.
	FrameworkCount int `json:"framework_count"`

	// FrameworkNames are the framework names (for the prompt context; not a
	// number, so not part of AllowedNumbers).
	FrameworkNames []string `json:"framework_names"`

	// ===== slice 501 risk-posture-section fields =====
	// Populated only for the risk_posture_summary section (riskSeverity=true).
	// RiskCount is how many open risks are aging in the register;
	// WorstResidualSeverity is the worst residual severity rounded to an int;
	// OldestRiskAgeDays is the oldest risk age in days.
	RiskCount             int `json:"risk_count,omitempty"`
	WorstResidualSeverity int `json:"worst_residual_severity,omitempty"`
	OldestRiskAgeDays     int `json:"oldest_risk_age_days,omitempty"`

	// Excerpts are the bounded, tenant-owned control/evidence excerpts the
	// model may cite (guardrail 1's "cited evidence excerpts", P0-440-8). Each
	// carries a canonical UUID the model cites verbatim.
	Excerpts []Excerpt `json:"excerpts"`

	// ===== slice 501 section discriminators (unexported — not serialized) =====
	// riskSeverity marks a risk-posture rollup so AllowedNumbers emits the
	// risk-section fields; driftOnly marks a drift-activity rollup. Both default
	// false — a zero-value Rollup is a coverage rollup, preserving the slice-440
	// behavior exactly.
	riskSeverity bool
	driftOnly    bool
}

// Excerpt is one bounded, tenant-owned piece of citable material behind the
// coverage numbers. The model may cite ONLY excerpt ids (the grounding gate);
// a cited id outside this set is a fabrication even if it happens to name
// another tenant-owned row.
type Excerpt struct {
	ID   string       `json:"id"`
	Kind CitationKind `json:"kind"`
	// Title is a short human label (control title); Excerpt is a bounded slice
	// of supporting text. Both are tenant-supplied and treated as UNTRUSTED
	// prompt input — the guardrails (post-generation validation) are the
	// defense, not input trust (threat-model T / prompt-injection note).
	Title   string `json:"title"`
	Excerpt string `json:"excerpt"`
}

// RollupFromBrief projects a board.Brief into the coverage-section Rollup,
// attaching the bounded citable excerpts. Pure — no IO. Returns ErrNoBriefData
// when the Brief carries no framework posture (nothing to summarize).
//
// The coverage / freshness / delta numbers are taken from the FIRST framework
// posture row (they are program-wide and identical across frameworks in v1 —
// board decisions log D3) plus the Brief's drift summary. The excerpts come
// from the caller (the Store's bounded, RLS-scoped control/evidence read).
func RollupFromBrief(b board.Brief, excerpts []Excerpt) (Rollup, error) {
	if len(b.Frameworks) == 0 {
		return Rollup{}, ErrNoBriefData
	}
	// Program-wide numbers (identical across frameworks in v1) — read the first.
	head := b.Frameworks[0]
	names := make([]string, 0, len(b.Frameworks))
	for _, fw := range b.Frameworks {
		names = append(names, fw.Name)
	}
	bounded := excerpts
	if len(bounded) > maxCitedExcerpts {
		bounded = bounded[:maxCitedExcerpts]
	}
	return Rollup{
		PeriodEnd:       b.PeriodEnd,
		CoveragePct:     head.CoveragePct,
		FreshnessPct:    head.FreshnessPct,
		Delta:           b.Drift.Delta,
		FlippedOutCount: b.Drift.FlippedOutCount,
		WindowDays:      b.Drift.WindowDays,
		FrameworkCount:  len(b.Frameworks),
		FrameworkNames:  names,
		Excerpts:        bounded,
	}, nil
}

// AllowedNumbers is the set of integer values the section is permitted to
// state — the union of every numeric rollup field. The numeric-verification
// gate (verifyNumbers) checks every number parsed from the draft against this
// set; a number outside it auto-rejects the draft (guardrail 5).
//
// The set is built from the ABSOLUTE values of the signed fields too: a delta
// of -3 may be written "3 controls drifted out" or "down 3 points" or "-3", and
// all three are honest renderings of the same ground-truth magnitude. Matching
// on absolute value lets the model phrase the direction in prose (which the
// shape template + prose are responsible for) while still pinning the
// MAGNITUDE to ground truth. The sign is carried by the words "drifted out" /
// "down", not by the digit — so we accept the magnitude and let tone/shape
// govern the direction word.
//
// Percent-shaped numbers (coverage, freshness) and counts (framework count,
// flipped-out count, window days, delta magnitude) all live in one set: a
// number is valid if it equals ANY ground-truth value. This is deliberately
// permissive on WHICH field a number maps to (the model may legitimately state
// "84% coverage and 84%..." — same value, different sentence) and strict on
// WHETHER the value exists in the pre-computation at all.
func (r Rollup) AllowedNumbers() map[int]bool {
	// Risk-posture section: only the risk integers are ground truth.
	if r.riskSeverity {
		return map[int]bool{
			r.RiskCount:             true,
			r.WorstResidualSeverity: true,
			r.OldestRiskAgeDays:     true,
		}
	}
	// Drift-activity section: only the drift integers are ground truth.
	if r.driftOnly {
		return map[int]bool{
			r.FlippedOutCount: true,
			r.WindowDays:      true,
			abs(r.Delta):      true,
			r.Delta:           true,
		}
	}
	// Coverage section (slice-440 default — a zero-value Rollup is a coverage
	// rollup, so the behavior is byte-for-byte the slice-440 set).
	allow := map[int]bool{
		r.CoveragePct:     true,
		r.FreshnessPct:    true,
		r.FlippedOutCount: true,
		r.WindowDays:      true,
		r.FrameworkCount:  true,
		abs(r.Delta):      true,
	}
	// The literal signed delta is also allowed (a draft may write "-3").
	allow[r.Delta] = true
	return allow
}

// allowedExcerptIDs builds the grounding set for citation validation: every
// excerpt id mapped to its kind. This is EXACTLY the set of ids the prompt put
// in front of the model, so it is exactly the set the model is permitted to
// cite (the grounding gate — guardrail 4).
func (r Rollup) allowedExcerptIDs() map[string]CitationKind {
	out := make(map[string]CitationKind, len(r.Excerpts))
	for _, e := range r.Excerpts {
		out[e.ID] = e.Kind
	}
	return out
}

// sortExcerpts orders excerpts deterministically (by kind then id) so the
// prompt — and therefore the audit record and any golden test — is stable
// across runs.
func sortExcerpts(xs []Excerpt) {
	sort.Slice(xs, func(i, j int) bool {
		if xs[i].Kind != xs[j].Kind {
			return xs[i].Kind < xs[j].Kind
		}
		return xs[i].ID < xs[j].ID
	})
}

// itoa is a tiny strconv.Itoa wrapper kept local so the prompt + rollup files
// share one integer-formatting path.
func itoa(n int) string { return strconv.Itoa(n) }

// abs returns the absolute value of n.
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
