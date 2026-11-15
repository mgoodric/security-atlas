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

	// CoveragePct is the program coverage figure the board pack reports: the
	// unweighted mean of the scoped frameworks' requirement coverage, from
	// the Brief's Coverage (#2210). The draft may state it only as
	// board.FormatPct renders it ("41.9", "75"), the pack's own rendering.
	// Not reported when ScopedFrameworkCount is 0.
	CoveragePct float64 `json:"coverage_pct"`
	// ScopedFrameworkCount is how many frameworks the mean is over.
	ScopedFrameworkCount int `json:"scoped_framework_count"`
	// FreshnessPct is the tenant-wide evidence freshness rate. It is not
	// coverage and the prompt names it so.
	FreshnessPct int `json:"evidence_freshness_rate_pct"`

	// Delta is the net change in passing controls over the 30-day window;
	// FlippedOutCount is how many controls drifted OUT of passing and
	// StartedPassingCount how many started passing (#2195). All three from the
	// Brief's drift summary. Gains minus losses is not Delta in general.
	// StartedPassingCount is nil for a brief stored before it existed.
	Delta               int  `json:"delta"`
	FlippedOutCount     int  `json:"flipped_out_count"`
	StartedPassingCount *int `json:"started_passing_count,omitempty"`

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

	// ===== slice 751 exception-status-section fields =====
	// Populated only for the exception_status_summary section
	// (exceptionOnly=true), projected verbatim from the deterministic
	// board.Brief.Exceptions aggregate. These three integers are the ENTIRE
	// numeric surface the exception section is permitted to state — there is no
	// derived or computed exception number anywhere in this package, so a claim
	// the aggregate did not produce cannot be grounded.
	ExceptionsActive       int `json:"exceptions_active,omitempty"`
	ExceptionsPastDue      int `json:"exceptions_past_due,omitempty"`
	OldestExceptionAgeDays int `json:"oldest_exception_age_days,omitempty"`

	// Excerpts are the bounded, tenant-owned control/evidence excerpts the
	// model may cite (guardrail 1's "cited evidence excerpts", P0-440-8). Each
	// carries a canonical UUID the model cites verbatim.
	Excerpts []Excerpt `json:"excerpts"`

	// ===== slice 501 section discriminators (unexported — not serialized) =====
	// riskSeverity marks a risk-posture rollup so AllowedNumbers emits the
	// risk-section fields; driftOnly marks a drift-activity rollup;
	// exceptionOnly marks an exception-status rollup (slice 751). All default
	// false — a zero-value Rollup is a coverage rollup, preserving the slice-440
	// behavior exactly.
	riskSeverity  bool
	driftOnly     bool
	exceptionOnly bool
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
// when the Brief carries no framework posture (nothing to summarize), or no
// Coverage: a brief assembled before #2210 holds only the tenant-wide
// freshness figure, which is not coverage and must not ground a coverage
// claim.
//
// The coverage and freshness numbers are the Brief's Coverage (the board
// pack's figure, #2210); the drift numbers are its drift summary. The
// excerpts come from the caller (the Store's bounded, RLS-scoped
// control/evidence read).
func RollupFromBrief(b board.Brief, excerpts []Excerpt) (Rollup, error) {
	if len(b.Frameworks) == 0 || b.Coverage == nil {
		return Rollup{}, ErrNoBriefData
	}
	names := make([]string, 0, len(b.Frameworks))
	for _, fw := range b.Frameworks {
		names = append(names, fw.Name)
	}
	bounded := excerpts
	if len(bounded) > maxCitedExcerpts {
		bounded = bounded[:maxCitedExcerpts]
	}
	return Rollup{
		PeriodEnd:            b.PeriodEnd,
		CoveragePct:          b.Coverage.CoveragePct,
		ScopedFrameworkCount: b.Coverage.ScopedFrameworkCount,
		FreshnessPct:         b.Coverage.EvidenceFreshnessRatePct,
		Delta:                b.Drift.Delta,
		FlippedOutCount:      b.Drift.FlippedOutCount,
		StartedPassingCount:  b.Drift.StartedPassingCount,
		WindowDays:           b.Drift.WindowDays,
		FrameworkCount:       len(b.Frameworks),
		FrameworkNames:       names,
		Excerpts:             bounded,
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
// Counts (framework count, flipped-out count, window days, delta magnitude)
// live in one set: a number is valid if it equals ANY ground-truth value. This
// is deliberately permissive on WHICH field a number maps to and strict on
// WHETHER the value exists in the pre-computation at all. The coverage section
// adds two checks on top (verifyCoverageNumbers, #2210): a decimal must be the
// coverage figure as rendered, and a percentage must be a percentage field.
func (r Rollup) AllowedNumbers() map[int]bool {
	// Risk-posture section: only the risk integers are ground truth.
	if r.riskSeverity {
		return map[int]bool{
			r.RiskCount:             true,
			r.WorstResidualSeverity: true,
			r.OldestRiskAgeDays:     true,
		}
	}
	// Exception-status section (slice 751): only the three board-grade
	// exception integers from board.Brief.Exceptions are ground truth. Nothing
	// else in the Rollup is reachable, so a coverage percentage or a risk
	// severity accidentally carried in the struct can never validate a number in
	// an exception draft.
	if r.exceptionOnly {
		return map[int]bool{
			r.ExceptionsActive:       true,
			r.ExceptionsPastDue:      true,
			r.OldestExceptionAgeDays: true,
		}
	}
	// Drift-activity section: only the drift integers are ground truth.
	if r.driftOnly {
		allow := map[int]bool{
			r.FlippedOutCount: true,
			r.WindowDays:      true,
			abs(r.Delta):      true,
			r.Delta:           true,
		}
		r.allowStartedPassing(allow)
		return allow
	}
	// Coverage section (a zero-value Rollup is a coverage rollup). The
	// coverage figure is an integer here only when the pack renders it as one
	// ("75"); a decimal rendering ("41.9") is admitted by coverageDecimal
	// instead. With no scoped framework neither the figure nor the scoped
	// count is a number the draft may state (#2210).
	allow := map[int]bool{
		r.FreshnessPct:    true,
		r.FlippedOutCount: true,
		r.WindowDays:      true,
		r.FrameworkCount:  true,
		abs(r.Delta):      true,
	}
	// The literal signed delta is also allowed (a draft may write "-3").
	allow[r.Delta] = true
	if r.ScopedFrameworkCount > 0 {
		allow[r.ScopedFrameworkCount] = true
		if n, ok := r.coverageInt(); ok {
			allow[n] = true
		}
	}
	r.allowStartedPassing(allow)
	return allow
}

// coverageText is the coverage figure as the board pack renders it.
func (r Rollup) coverageText() string { return board.FormatPct(r.CoveragePct) }

// coverageInt is the coverage figure when the pack renders it as a whole
// number.
func (r Rollup) coverageInt() (int, bool) {
	n, err := strconv.Atoi(r.coverageText())
	return n, err == nil
}

// coverageDecimal is the one decimal token a coverage draft may contain: the
// coverage figure as the pack renders it, when that rendering has a decimal
// point. Empty otherwise.
func (r Rollup) coverageDecimal() string {
	if r.ScopedFrameworkCount == 0 {
		return ""
	}
	if _, ok := r.coverageInt(); ok {
		return ""
	}
	return r.coverageText()
}

// allowedPercents is the set of integers a coverage draft may write as a
// percentage: the evidence freshness rate, and the coverage figure when it is
// a whole number and some framework is scoped. A count followed by "%", or
// "0%" for a tenant with no scoped framework, is not a percentage the rollup
// produced (#2210).
func (r Rollup) allowedPercents() map[int]bool {
	allow := map[int]bool{r.FreshnessPct: true}
	if r.ScopedFrameworkCount > 0 {
		if n, ok := r.coverageInt(); ok {
			allow[n] = true
		}
	}
	return allow
}

// verifyDraftNumbers is guardrail 5 for this rollup's section. The coverage
// section runs the stricter coverage check; every other section runs the
// shared library over its AllowedNumbers.
func (r Rollup) verifyDraftNumbers(text string) bool {
	if r.riskSeverity || r.driftOnly || r.exceptionOnly {
		return VerifyNumbers(text, r.AllowedNumbers(), r.PeriodEnd)
	}
	return verifyCoverageNumbers(text, r)
}

// allowStartedPassing adds the started-passing count to a drift-bearing
// section's allowed set, when the brief recorded one. A brief stored before
// #2195 has none, and the gate then admits no number for it.
func (r Rollup) allowStartedPassing(allow map[int]bool) {
	if r.StartedPassingCount != nil {
		allow[*r.StartedPassingCount] = true
	}
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
