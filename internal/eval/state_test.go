// Unit tests for the pure evaluation logic — no database, no I/O. These
// functions are the deterministic core of the evaluation stage: given the
// same evidence slice they ALWAYS produce the same result (the property
// AC-3 idempotency and AC-7 replay both depend on).
package eval

import (
	"testing"
	"time"
)

// ===== ISC-11: freshnessMaxAge maps every class to canvas §2.3 max-age =====

func TestFreshnessMaxAge_CanvasTable(t *testing.T) {
	cases := []struct {
		class string
		want  time.Duration
	}{
		{"realtime", 24 * time.Hour},
		{"daily", 7 * 24 * time.Hour},
		{"weekly", 30 * 24 * time.Hour},
		{"monthly", 90 * 24 * time.Hour},
		{"quarterly", 120 * 24 * time.Hour},
		{"annual", 400 * 24 * time.Hour},
	}
	for _, c := range cases {
		got, ok := freshnessMaxAge(c.class)
		if !ok {
			t.Fatalf("freshnessMaxAge(%q): expected ok=true", c.class)
		}
		if got != c.want {
			t.Fatalf("freshnessMaxAge(%q) = %v, want %v", c.class, got, c.want)
		}
	}
}

func TestFreshnessMaxAge_UnknownClassFallsBackToMonthly(t *testing.T) {
	// A control bundle may declare freshness_class = "hourly" (the bundle
	// manifest allows it) which the canvas §2.3 evidence enum does not have.
	// An empty/absent class is also possible. Both fall back to the
	// monthly default — the same default the evidence_records column uses.
	got, ok := freshnessMaxAge("")
	if !ok {
		t.Fatalf("freshnessMaxAge(\"\"): expected ok=true (defaulted)")
	}
	if got != 90*24*time.Hour {
		t.Fatalf("freshnessMaxAge(\"\") = %v, want monthly default 90d", got)
	}
	got2, _ := freshnessMaxAge("hourly")
	if got2 != 90*24*time.Hour {
		t.Fatalf("freshnessMaxAge(\"hourly\") = %v, want monthly default 90d", got2)
	}
}

// ===== ISC-12..14: computeResult rollup =====

func TestComputeResult_ZeroRecordsIsInconclusive(t *testing.T) {
	// AC anti-criterion: a control with no in-window evidence is
	// `inconclusive`, NOT `fail`. Absence of evidence is not evidence of
	// failure.
	got := computeResult(nil)
	if got != "inconclusive" {
		t.Fatalf("computeResult(nil) = %q, want inconclusive", got)
	}
	got = computeResult([]inWindowRecord{})
	if got != "inconclusive" {
		t.Fatalf("computeResult([]) = %q, want inconclusive", got)
	}
}

func TestComputeResult_AnyFailIsFail(t *testing.T) {
	recs := []inWindowRecord{
		{result: "pass"},
		{result: "fail"},
		{result: "pass"},
	}
	if got := computeResult(recs); got != "fail" {
		t.Fatalf("computeResult with one fail = %q, want fail", got)
	}
}

func TestComputeResult_AllPassIsPass(t *testing.T) {
	recs := []inWindowRecord{
		{result: "pass"},
		{result: "pass"},
	}
	if got := computeResult(recs); got != "pass" {
		t.Fatalf("computeResult all pass = %q, want pass", got)
	}
}

func TestComputeResult_NaWithoutFailIsNa(t *testing.T) {
	// `na` records (the control does not apply to this observation) with no
	// fail and no pass collapse to `na`. A pass alongside na is still pass —
	// the control demonstrably operated.
	recs := []inWindowRecord{{result: "na"}, {result: "na"}}
	if got := computeResult(recs); got != "na" {
		t.Fatalf("computeResult all na = %q, want na", got)
	}
	recs = []inWindowRecord{{result: "na"}, {result: "pass"}}
	if got := computeResult(recs); got != "pass" {
		t.Fatalf("computeResult na+pass = %q, want pass", got)
	}
}

func TestComputeResult_InconclusiveRecordWithoutPassOrFail(t *testing.T) {
	recs := []inWindowRecord{{result: "inconclusive"}, {result: "na"}}
	if got := computeResult(recs); got != "inconclusive" {
		t.Fatalf("computeResult inconclusive+na = %q, want inconclusive", got)
	}
}

// ===== ISC-15..17: computeFreshness =====

func TestComputeFreshness_ZeroRecordsIsNoEvidence(t *testing.T) {
	now := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	got := computeFreshness(nil, "daily", now)
	if got != "no_evidence" {
		t.Fatalf("computeFreshness(nil) = %q, want no_evidence", got)
	}
}

func TestComputeFreshness_FreshestWithinWindowIsFresh(t *testing.T) {
	now := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	// daily class = 7d window. Freshest record is 2 days old → fresh.
	recs := []allRecord{
		{observedAt: now.Add(-2 * 24 * time.Hour)},
		{observedAt: now.Add(-10 * 24 * time.Hour)},
	}
	got := computeFreshness(recs, "daily", now)
	if got != "fresh" {
		t.Fatalf("computeFreshness 2d-old, daily = %q, want fresh", got)
	}
}

func TestComputeFreshness_FreshestPastWindowIsStale(t *testing.T) {
	now := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	// daily class = 7d window. Freshest record is 10 days old → stale.
	recs := []allRecord{
		{observedAt: now.Add(-10 * 24 * time.Hour)},
		{observedAt: now.Add(-40 * 24 * time.Hour)},
	}
	got := computeFreshness(recs, "daily", now)
	if got != "stale" {
		t.Fatalf("computeFreshness 10d-old, daily = %q, want stale", got)
	}
}

func TestComputeFreshness_ExactlyAtWindowEdgeIsFresh(t *testing.T) {
	now := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	// Record observed exactly 7d ago, daily class = 7d window. The edge is
	// inclusive — observed_at == cutoff is still fresh.
	recs := []allRecord{{observedAt: now.Add(-7 * 24 * time.Hour)}}
	got := computeFreshness(recs, "daily", now)
	if got != "fresh" {
		t.Fatalf("computeFreshness exactly-7d-old, daily = %q, want fresh", got)
	}
}

// ===== ISC-18: evaluation excludes wall-clock — same inputs, same result =====

func TestComputeResult_DeterministicAcrossCalls(t *testing.T) {
	recs := []inWindowRecord{{result: "pass"}, {result: "fail"}, {result: "na"}}
	first := computeResult(recs)
	for i := 0; i < 100; i++ {
		if got := computeResult(recs); got != first {
			t.Fatalf("computeResult not deterministic: call %d = %q, first = %q", i, got, first)
		}
	}
}

// ===== inWindowRecords: the freshness-window filter =====

func TestInWindowRecords_FiltersByCutoff(t *testing.T) {
	now := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	all := []allRecord{
		{observedAt: now.Add(-1 * 24 * time.Hour), result: "pass"},  // in
		{observedAt: now.Add(-5 * 24 * time.Hour), result: "fail"},  // in
		{observedAt: now.Add(-30 * 24 * time.Hour), result: "pass"}, // out (daily=7d)
	}
	in := inWindowRecords(all, "daily", now)
	if len(in) != 2 {
		t.Fatalf("inWindowRecords daily: got %d in-window, want 2", len(in))
	}
	// The out-of-window pass must NOT leak into the result computation —
	// this is anti-criterion P0-2.
	if computeResult(in) != "fail" {
		t.Fatalf("inWindowRecords let an out-of-window record affect result")
	}
}
