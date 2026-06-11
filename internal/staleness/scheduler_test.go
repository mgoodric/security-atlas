package staleness

import (
	"testing"
	"time"
)

// Pure-Go: the weekly digest window is exactly Monday hour-09 UTC.
func TestIsWeeklyDigestWindow(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		ts   time.Time
		want bool
	}{
		{"monday 09:00 UTC", time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC), true},
		{"monday 09:59 UTC", time.Date(2026, 6, 1, 9, 59, 0, 0, time.UTC), true},
		{"monday 08:59 UTC", time.Date(2026, 6, 1, 8, 59, 0, 0, time.UTC), false},
		{"monday 10:00 UTC", time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC), false},
		{"tuesday 09:00 UTC", time.Date(2026, 6, 2, 9, 0, 0, 0, time.UTC), false},
		{"sunday 09:00 UTC", time.Date(2026, 5, 31, 9, 0, 0, 0, time.UTC), false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isWeeklyDigestWindow(tc.ts); got != tc.want {
				t.Errorf("isWeeklyDigestWindow(%v) = %v, want %v", tc.ts, got, tc.want)
			}
		})
	}
}

// Pure-Go: the recompute period + ISO-week keys are deterministic + stable.
func TestPeriodKeys(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 6, 1, 11, 30, 0, 0, time.UTC) // Monday
	// recompute period truncates to the 6h boundary measured from the zero
	// time (00/06/12/18 UTC); 11:30 falls in the 06:00 bucket.
	if got := recomputePeriodKey(ts); got != "20260601T0600Z" {
		t.Errorf("recomputePeriodKey = %q, want 20260601T0600Z", got)
	}
	// Two instants in the SAME 6h window (07:00 and 09:00 both in the 06:00
	// bucket) must bucket identically — the dedup-window property: a control
	// stale across ticks within one window alerts once.
	inWindowA := time.Date(2026, 6, 1, 7, 0, 0, 0, time.UTC)
	inWindowB := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	if a, b := recomputePeriodKey(inWindowA), recomputePeriodKey(inWindowB); a != b {
		t.Errorf("instants in the same 6h window must share a period key: %q vs %q", a, b)
	}
	// An instant in the NEXT window (13:00 in the 12:00 bucket) must differ.
	if a, c := recomputePeriodKey(inWindowA), recomputePeriodKey(time.Date(2026, 6, 1, 13, 0, 0, 0, time.UTC)); a == c {
		t.Errorf("instants in different 6h windows must differ: both %q", a)
	}
	if got := isoWeekKey(ts); got != "2026-W23" {
		t.Errorf("isoWeekKey = %q, want 2026-W23", got)
	}
	start, end := isoWeekBounds(ts)
	if start.Weekday() != time.Monday {
		t.Errorf("isoWeekBounds start weekday = %v, want Monday", start.Weekday())
	}
	if end.Sub(start) != 7*24*time.Hour {
		t.Errorf("isoWeek span = %v, want 168h", end.Sub(start))
	}
}
