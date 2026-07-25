package webhook

import (
	"testing"
	"time"
)

// Pure-Go unit coverage for the summary-field normalizers (CLAUDE.md Q-2
// convention). These mirror incidents.normalize* exactly; the table exercises the
// status/urgency coercion branches and the parseTime empty/bad/good paths that the
// HTTP-level tests do not all reach. No Postgres, no build tag, t.Parallel.

func TestNormalizeStatus_Table(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"acknowledged":   "acknowledged",
		"ACKNOWLEDGED":   "acknowledged",
		"  resolved  ":   "resolved",
		"resolved":       "resolved",
		"triggered":      "triggered",
		"":               "triggered",
		"something-else": "triggered",
	}
	for in, want := range cases {
		if got := normalizeStatus(in); got != want {
			t.Errorf("normalizeStatus(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestNormalizeUrgency_Table(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"low":   "low",
		"LOW":   "low",
		" low ": "low",
		"high":  "high",
		"":      "high",
		"weird": "high",
	}
	for in, want := range cases {
		if got := normalizeUrgency(in); got != want {
			t.Errorf("normalizeUrgency(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestParseTime_Table(t *testing.T) {
	t.Parallel()
	// Empty → zero time.
	if got := parseTime(""); !got.IsZero() {
		t.Errorf("parseTime(empty) = %v; want zero", got)
	}
	if got := parseTime("   "); !got.IsZero() {
		t.Errorf("parseTime(blank) = %v; want zero", got)
	}
	// Bad → zero time.
	if got := parseTime("not-a-timestamp"); !got.IsZero() {
		t.Errorf("parseTime(bad) = %v; want zero", got)
	}
	// Good RFC3339 → parsed, coerced to UTC.
	got := parseTime("2026-06-09T14:25:00Z")
	want := time.Date(2026, 6, 9, 14, 25, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("parseTime(good) = %v; want %v", got, want)
	}
	if got.Location() != time.UTC {
		t.Errorf("parseTime did not coerce to UTC: %v", got.Location())
	}
}
