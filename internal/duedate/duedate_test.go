package duedate

import (
	"testing"
	"time"
)

func TestStatus(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	at := func(m time.Month, d, h, min int) time.Time { return time.Date(2026, m, d, h, min, 0, 0, time.UTC) }
	cases := []struct {
		name string
		due  time.Time
		want string
	}{
		{"yesterday's last minute is overdue", at(11, 13, 23, 59), Overdue},
		{"due today, past its hour, is not overdue yet", at(11, 14, 0, 0), DueSoon},
		{"the last day of the window is due soon", at(11, 28, 23, 0), DueSoon},
		{"the day after the window is upcoming", at(11, 29, 0, 0), Upcoming},
		{"a non-UTC offset is read as its UTC day", time.Date(2026, 11, 13, 20, 0, 0, 0, time.FixedZone("x", -5*3600)), DueSoon},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Status(tc.due, now, 14); got != tc.want {
				t.Fatalf("Status(%s) = %q, want %q", tc.due, got, tc.want)
			}
		})
	}
}
