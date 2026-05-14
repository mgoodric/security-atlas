// Package eval is the EVALUATION STAGE of the security-atlas evidence
// pipeline (canvas §4.3). It is a read-only consumer of the append-only
// evidence ledger (`evidence_records`, slice 013): it reads evidence,
// applies each control's evidence queries, computes `(control × scope_cell ×
// time) → {pass, fail, na, inconclusive}` plus a freshness status, and
// APPENDS the derived state to `control_evaluations` (slice 012's own table,
// migration _027).
//
// Constitutional invariant #2 — ingestion and evaluation are separated
// stages — is enforced structurally: this package's only writer
// (Store.appendEvaluation) has exactly one INSERT target, control_evaluations.
// It imports no ingestion-side write path. The evidence ledger is also
// append-only at the RLS layer (slice 013: no UPDATE/DELETE policy under
// FORCE), so even a bug here cannot mutate the source-of-truth record.
// Because state is derived purely from the immutable ledger, point-in-time
// replay (AC-7) is always possible.
//
// state.go holds the PURE evaluation logic — deterministic functions with no
// I/O. Given the same evidence they always produce the same result; that
// determinism is what AC-3 (idempotency) and AC-7 (replay) rest on. Wall
// clock enters only as an explicit `now` parameter for the freshness-window
// cutoff; it never leaks into the pass/fail result itself.
package eval

import "time"

// ResultPass / Fail / NA / Inconclusive mirror the `evidence_result` Postgres
// enum. Kept as plain strings so the pure logic has no DB dependency.
const (
	ResultPass         = "pass"
	ResultFail         = "fail"
	ResultNA           = "na"
	ResultInconclusive = "inconclusive"
)

// FreshnessFresh / Stale / NoEvidence mirror the `freshness_status` CHECK
// vocabulary on control_evaluations.
const (
	FreshnessFresh      = "fresh"
	FreshnessStale      = "stale"
	FreshnessNoEvidence = "no_evidence"
)

// defaultFreshnessMaxAge is the fallback window — 90 days, the `monthly`
// class. It matches the NOT NULL DEFAULT 'monthly' on evidence_records so a
// control that omits freshness_class evaluates consistently with its
// evidence rows.
const defaultFreshnessMaxAge = 90 * 24 * time.Hour

// freshnessMaxAgeTable is the canvas §2.3 freshness model: class → max
// acceptable evidence age. realtime 24h · daily 7d · weekly 30d · monthly
// 90d · quarterly 120d · annual 400d.
var freshnessMaxAgeTable = map[string]time.Duration{
	"realtime":  24 * time.Hour,
	"daily":     7 * 24 * time.Hour,
	"weekly":    30 * 24 * time.Hour,
	"monthly":   90 * 24 * time.Hour,
	"quarterly": 120 * 24 * time.Hour,
	"annual":    400 * 24 * time.Hour,
}

// freshnessMaxAge returns the max acceptable evidence age for a freshness
// class. An unknown or empty class (e.g. a bundle that declares "hourly",
// which the evidence enum lacks) falls back to the monthly default; `ok` is
// always true so callers do not have to special-case the fallback, but the
// distinction is available for logging.
func freshnessMaxAge(class string) (time.Duration, bool) {
	if d, ok := freshnessMaxAgeTable[class]; ok {
		return d, true
	}
	return defaultFreshnessMaxAge, true
}

// inWindowRecord is one evidence record's result that fell INSIDE the
// freshness window. computeResult only ever sees in-window records — the
// freshness filter (inWindowRecords) is applied first. This is the type-level
// expression of anti-criterion P0-2: out-of-window evidence cannot reach the
// result computation.
type inWindowRecord struct {
	result string
}

// allRecord is one evidence record before the freshness filter — the full
// ledger slice for a (control, scope_cell). Carries observed_at because
// computeFreshness needs the freshest timestamp regardless of window.
type allRecord struct {
	observedAt time.Time
	result     string
}

// inWindowRecords filters a full ledger slice down to the records whose
// observed_at is within `class`'s max-age window relative to `now`. The
// window edge is inclusive: observed_at == cutoff is in-window.
func inWindowRecords(all []allRecord, class string, now time.Time) []inWindowRecord {
	maxAge, _ := freshnessMaxAge(class)
	cutoff := now.Add(-maxAge)
	out := make([]inWindowRecord, 0, len(all))
	for _, r := range all {
		if !r.observedAt.Before(cutoff) {
			out = append(out, inWindowRecord{result: r.result})
		}
	}
	return out
}

// computeResult rolls a set of in-window evidence results into a single
// control state. The precedence, strictest first:
//
//   - zero records          → inconclusive  (absence of evidence is NOT failure)
//   - any `fail`            → fail
//   - any `pass`            → pass          (the control demonstrably operated)
//   - any `inconclusive`    → inconclusive
//   - otherwise (all `na`)  → na
//
// Deterministic: the result depends only on the multiset of result values,
// not on order, count, or wall clock.
func computeResult(records []inWindowRecord) string {
	if len(records) == 0 {
		return ResultInconclusive
	}
	var sawFail, sawPass, sawInconclusive bool
	for _, r := range records {
		switch r.result {
		case ResultFail:
			sawFail = true
		case ResultPass:
			sawPass = true
		case ResultInconclusive:
			sawInconclusive = true
		}
	}
	switch {
	case sawFail:
		return ResultFail
	case sawPass:
		return ResultPass
	case sawInconclusive:
		return ResultInconclusive
	default:
		return ResultNA
	}
}

// computeFreshness classifies a control's evidence freshness relative to its
// freshness class:
//
//   - zero records                       → no_evidence
//   - freshest observed_at within window → fresh
//   - freshest observed_at past window   → stale
//
// `stale` is orthogonal to the pass/fail result: a control can be pass+stale
// (the freshest passing evidence is old) or pass+fresh.
func computeFreshness(all []allRecord, class string, now time.Time) string {
	if len(all) == 0 {
		return FreshnessNoEvidence
	}
	maxAge, _ := freshnessMaxAge(class)
	cutoff := now.Add(-maxAge)
	var freshest time.Time
	for _, r := range all {
		if r.observedAt.After(freshest) {
			freshest = r.observedAt
		}
	}
	if freshest.Before(cutoff) {
		return FreshnessStale
	}
	return FreshnessFresh
}

// latestObservedAt returns the freshest observed_at across a ledger slice, or
// the zero time when the slice is empty. Used to populate
// control_evaluations.last_observed_at.
func latestObservedAt(all []allRecord) time.Time {
	var latest time.Time
	for _, r := range all {
		if r.observedAt.After(latest) {
			latest = r.observedAt
		}
	}
	return latest
}
