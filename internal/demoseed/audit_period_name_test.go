// audit_period_name_test.go — pure-Go coverage for the slice-680
// audit-period label↔range fix (ATLAS-033).
//
// Load-bearing function + branch covered:
//
//   - auditPeriodName (fixtures.go) — derives the "SOC 2 <year> Q<n>"
//     label from a period's OWN start date so the quarter label always
//     matches the date range. The prior seed labelled by loop index,
//     decoupling label from range. These table cases pin every quarter
//     boundary (Jan/Mar = Q1, Apr/Jun = Q2, Jul/Sep = Q3, Oct/Dec = Q4)
//     and a year boundary so a regression that reintroduces the
//     index-based label fails here (AC-4).
//
// No Postgres, no build tag — fast pure-Go branch coverage per the
// CLAUDE.md "Pure-Go pre-DB unit convention" (Q-2).

package demoseed

import (
	"testing"
	"time"
)

func TestAuditPeriodName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		start time.Time
		want  string
	}{
		{"jan is Q1", time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC), "SOC 2 2026 Q1"},
		{"mar is Q1", time.Date(2026, time.March, 31, 0, 0, 0, 0, time.UTC), "SOC 2 2026 Q1"},
		{"apr is Q2", time.Date(2026, time.April, 1, 0, 0, 0, 0, time.UTC), "SOC 2 2026 Q2"},
		{"jun is Q2", time.Date(2026, time.June, 30, 0, 0, 0, 0, time.UTC), "SOC 2 2026 Q2"},
		{"jul is Q3", time.Date(2025, time.July, 1, 0, 0, 0, 0, time.UTC), "SOC 2 2025 Q3"},
		{"sep is Q3", time.Date(2025, time.September, 30, 0, 0, 0, 0, time.UTC), "SOC 2 2025 Q3"},
		{"oct is Q4", time.Date(2025, time.October, 1, 0, 0, 0, 0, time.UTC), "SOC 2 2025 Q4"},
		{"dec is Q4 uses start year", time.Date(2025, time.December, 31, 0, 0, 0, 0, time.UTC), "SOC 2 2025 Q4"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := auditPeriodName(tc.start); got != tc.want {
				t.Errorf("auditPeriodName(%s) = %q; want %q",
					tc.start.Format("2006-01-02"), got, tc.want)
			}
		})
	}
}

// TestAuditPeriodNameMatchesQuarterOfRange is the regression guard for
// the exact ATLAS-033 contradiction: the label's quarter must equal the
// calendar quarter of the period START. We assert the property directly
// (rather than a fixed string) across all 12 months so any future change
// to the prefix still keeps the quarter honest.
func TestAuditPeriodNameMatchesQuarterOfRange(t *testing.T) {
	t.Parallel()
	for m := time.January; m <= time.December; m++ {
		start := time.Date(2026, m, 6, 0, 0, 0, 0, time.UTC)
		wantQ := (int(m)-1)/3 + 1
		got := auditPeriodName(start)
		// The label ends with "Q<wantQ>".
		suffix := "Q" + string(rune('0'+wantQ))
		if len(got) < len(suffix) || got[len(got)-len(suffix):] != suffix {
			t.Errorf("month %s: auditPeriodName=%q; want suffix %q (quarter must match start date)",
				m, got, suffix)
		}
	}
}
