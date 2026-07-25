// Package staleness is the PRODUCER of `evidence.staleness` notifications
// (slice 439). It is a read-only consumer of the slice-016 freshness read
// model: a scheduled rollup (reusing the slice-076 metrics/scheduler cadence
// pattern) reads every control's derived freshness per tenant, classifies each
// into a stale / approaching / fresh band, and writes in-app notifications
// (the slice-029 `notifications` store) — a per-control alert when a control
// crosses the staleness threshold, plus a weekly digest summarizing the
// tenant's stale + approaching-stale posture.
//
// The already-merged delivery channels (slice 445 email, slice 543
// Slack/webhook, slice 582/583 digest scheduler) CONSUME the
// `evidence.staleness` rows this package writes. This package closes the
// producer gap — before slice 439 the platform COMPUTED staleness but nothing
// told the operator.
//
// HONESTY DISCIPLINE (canvas §1.6 anti-pattern; P0-439-1): this is NOT
// "continuous monitoring." The recompute interval and the weekly digest
// cadence are named explicitly in the notification copy (see copy.go) and in
// the operator docs. Polling dressed as real-time alerting is banned.
//
// Constitutional invariants honored:
//   - #2 (ingestion/evaluation separation): this package READS the freshness
//     read model; it never writes `evidence_records`. Its only writes are
//     notification rows (slice-029) + the slice-439 idempotency ledger.
//   - #6 (tenant isolation via RLS): the rollup runs per-tenant under
//     `app.current_tenant`; the cross-tenant leak (threat-model I) is proven
//     absent by the tenant-isolation integration test.
package staleness

import "time"

// Band is the staleness classification of one control's freshest evidence
// relative to its valid_until horizon. The three-band model (slice 439
// JUDGMENT D2) gives the operator an early-warning "approaching" signal BEFORE
// evidence actually goes stale — the whole point of the slice is to stop the
// operator being surprised.
type Band int

const (
	// BandFresh: evidence is comfortably within its freshness window — more
	// than ApproachingWindow remaining before valid_until. No notification.
	BandFresh Band = iota
	// BandApproaching: evidence is still fresh but valid_until is within
	// ApproachingWindow from now — the early-warning band. Surfaced in the
	// weekly digest's "approaching" count; does NOT fire a per-control alert
	// (an alert fires only on the actual threshold crossing — BandStale).
	BandApproaching
	// BandStale: evidence is at/over its freshness threshold (valid_until has
	// passed, or there is no evidence at all). Fires a per-control alert AND
	// counts in the digest's "stale" total.
	BandStale
)

// String renders the band as a stable lowercase token for notification
// payloads + the kindfilter event taxonomy. Stable across versions (wire
// shape).
func (b Band) String() string {
	switch b {
	case BandApproaching:
		return "approaching"
	case BandStale:
		return "stale"
	default:
		return "fresh"
	}
}

// DefaultApproachingWindow is the width of the "approaching stale" early-
// warning band (slice 439 JUDGMENT D2). 14 days: a control whose evidence
// expires within two weeks is flagged as approaching-stale so the operator has
// a reasonable runway to refresh it before it actually goes stale. Chosen as a
// fixed absolute window (not a percentage of the freshness class) so the
// warning runway is predictable regardless of class — a quarterly control and
// a monthly control both warn 14 days out. Revisit once real operators report
// whether 14 days is too noisy (quarterly controls) or too tight (monthly).
const DefaultApproachingWindow = 14 * 24 * time.Hour

// Cell is the minimal per-control freshness fact the classifier needs. It is a
// projection of freshness.ControlFreshness so the classifier stays a pure
// function with no DB dependency (AC-13 — pure-Go unit testable).
type Cell struct {
	// ValidUntil is the freshness horizon (latest_observed_at +
	// eval.FreshnessMaxAge(class)). Nil when the control has NO evidence at
	// all — which is stale by definition (the freshness read model's own
	// rule: a control with no evidence is not currently fresh).
	ValidUntil *time.Time
	// IsStale is the freshness read model's own stale verdict
	// (valid_until < refreshed_at). The classifier trusts it for the
	// BandStale decision so the "stale" definition lives in exactly one
	// place (internal/freshness.deriveFreshness), never re-derived here.
	IsStale bool
}

// Classify places one freshness cell into a band relative to `now` and the
// approaching window. PURE — deterministic given (cell, now, window).
//
//   - Stale (read model says so, or no valid_until horizon): BandStale.
//   - Otherwise, valid_until within `window` from now: BandApproaching.
//   - Otherwise: BandFresh.
//
// The stale verdict is taken from the read model (cell.IsStale) so the
// canonical "stale" definition is never forked. The approaching band is the
// only NEW judgment this function introduces.
func Classify(cell Cell, now time.Time, window time.Duration) Band {
	if cell.IsStale || cell.ValidUntil == nil {
		return BandStale
	}
	// Still fresh per the read model. Is it within the early-warning runway?
	// valid_until <= now+window means it expires inside the window.
	if !cell.ValidUntil.After(now.Add(window)) {
		return BandApproaching
	}
	return BandFresh
}
