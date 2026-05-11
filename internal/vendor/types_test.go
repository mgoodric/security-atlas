package vendor

import (
	"testing"
	"time"
)

// TestIsOverdueAsOf — AC-4. The derived overdue flag must hold three contract
// behaviors:
//  1. NULL last_review_date => overdue (never reviewed)
//  2. last_review + cadence < cutoff => overdue
//  3. last_review + cadence >= cutoff => NOT overdue
//
// The duration approximations in ReviewCadence.Duration (30/91/182/365 days)
// are tuned so this Go path agrees with the SQL CASE for all realistic
// cutoffs; the integration test exercises the SQL path separately.
func TestIsOverdueAsOf(t *testing.T) {
	parse := func(s string) time.Time {
		t.Helper()
		ts, err := time.Parse("2006-01-02", s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return ts
	}
	ptr := func(t time.Time) *time.Time { return &t }

	tests := []struct {
		name        string
		lastReview  *time.Time
		cadence     ReviewCadence
		cutoff      time.Time
		wantOverdue bool
	}{
		{
			name:        "never_reviewed_is_overdue",
			lastReview:  nil,
			cadence:     CadenceAnnual,
			cutoff:      parse("2026-05-11"),
			wantOverdue: true,
		},
		{
			name:        "annual_within_window",
			lastReview:  ptr(parse("2026-01-01")),
			cadence:     CadenceAnnual,
			cutoff:      parse("2026-05-11"),
			wantOverdue: false,
		},
		{
			name:        "annual_past_window",
			lastReview:  ptr(parse("2024-01-01")),
			cadence:     CadenceAnnual,
			cutoff:      parse("2026-05-11"),
			wantOverdue: true,
		},
		{
			name:        "quarterly_just_under",
			lastReview:  ptr(parse("2026-02-15")),
			cadence:     CadenceQuarterly,
			cutoff:      parse("2026-05-11"),
			wantOverdue: false,
		},
		{
			name:        "quarterly_overdue",
			lastReview:  ptr(parse("2025-12-01")),
			cadence:     CadenceQuarterly,
			cutoff:      parse("2026-05-11"),
			wantOverdue: true,
		},
		{
			name:        "monthly_overdue",
			lastReview:  ptr(parse("2026-04-01")),
			cadence:     CadenceMonthly,
			cutoff:      parse("2026-05-11"),
			wantOverdue: true,
		},
		{
			name:        "biannual_within_window",
			lastReview:  ptr(parse("2026-01-01")),
			cadence:     CadenceBiannual,
			cutoff:      parse("2026-05-11"),
			wantOverdue: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := Vendor{
				LastReviewDate: tc.lastReview,
				ReviewCadence:  tc.cadence,
			}
			if got := v.IsOverdueAsOf(tc.cutoff); got != tc.wantOverdue {
				t.Fatalf("IsOverdueAsOf(%v) = %v; want %v",
					tc.cutoff.Format(time.DateOnly), got, tc.wantOverdue)
			}
		})
	}
}

// TestCriticality_Valid + TestReviewCadence_Valid — anti-criterion: reject
// unknown enum strings at the API boundary, not silently coerce.
func TestCriticality_Valid(t *testing.T) {
	for _, c := range AllCriticalities {
		if !c.Valid() {
			t.Fatalf("%q should be valid", c)
		}
	}
	for _, bad := range []Criticality{"", "LOW", "critical", "unknown"} {
		if bad.Valid() {
			t.Fatalf("%q should be invalid", bad)
		}
	}
}

func TestReviewCadence_Valid(t *testing.T) {
	for _, c := range AllCadences {
		if !c.Valid() {
			t.Fatalf("%q should be valid", c)
		}
	}
	for _, bad := range []ReviewCadence{"", "weekly", "decadal"} {
		if bad.Valid() {
			t.Fatalf("%q should be invalid", bad)
		}
	}
}
