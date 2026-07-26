// Unit tests for the slice-751 exception-status aggregate projection — the
// pure half of the deterministic exceptions rollup the board-narrative
// exception section grounds on. The RLS-scoped SQL half is covered by the
// integration tier (integration_test.go).
//
// These matter more than their size suggests: every number the AI-drafted
// exception section is permitted to state comes out of this function, so a bug
// here is a bug the numeric-verification gate cannot catch — the gate checks
// the draft against the aggregate, not the aggregate against reality.

package board

import (
	"testing"
	"time"
)

func TestExceptionSummary(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		agg  exceptionAggRow
		want ExceptionSummary
	}{
		{
			name: "empty register is an honest zero, not an error",
			agg:  exceptionAggRow{},
			want: ExceptionSummary{ActiveCount: 0, PastDueCount: 0, OldestActiveAgeDays: 0},
		},
		{
			name: "counts pass through; age is days since the oldest start",
			agg: exceptionAggRow{
				ActiveCount:           4,
				PastDueCount:          1,
				OldestActiveStartedAt: now.AddDate(0, 0, -210),
			},
			want: ExceptionSummary{ActiveCount: 4, PastDueCount: 1, OldestActiveAgeDays: 210},
		},
		{
			name: "active exceptions but none past due",
			agg: exceptionAggRow{
				ActiveCount:           2,
				PastDueCount:          0,
				OldestActiveStartedAt: now.AddDate(0, 0, -30),
			},
			want: ExceptionSummary{ActiveCount: 2, PastDueCount: 0, OldestActiveAgeDays: 30},
		},
		{
			name: "partial day rounds down (a waiver started 12h ago is 0 days old)",
			agg: exceptionAggRow{
				ActiveCount:           1,
				OldestActiveStartedAt: now.Add(-12 * time.Hour),
			},
			want: ExceptionSummary{ActiveCount: 1, OldestActiveAgeDays: 0},
		},
		{
			name: "future effective_from clamps to 0, never a negative age",
			agg: exceptionAggRow{
				ActiveCount:           1,
				OldestActiveStartedAt: now.AddDate(0, 0, 14),
			},
			want: ExceptionSummary{ActiveCount: 1, OldestActiveAgeDays: 0},
		},
		{
			name: "null MIN (zero start) does not report the age since year zero",
			agg: exceptionAggRow{
				ActiveCount:           3,
				PastDueCount:          3,
				OldestActiveStartedAt: time.Time{},
			},
			want: ExceptionSummary{ActiveCount: 3, PastDueCount: 3, OldestActiveAgeDays: 0},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := exceptionSummary(tc.agg, now)
			if got != tc.want {
				t.Errorf("exceptionSummary() = %+v, want %+v", got, tc.want)
			}
		})
	}
}
