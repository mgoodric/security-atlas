package policy

import (
	"testing"
	"time"
)

// #2736: a policy never acknowledged is due AckGraceDays after it took
// effect, due soon through that day and overdue from the next; a lapsed
// ack is overdue from the instant it lapsed.
func TestPendingDue2736(t *testing.T) {
	t.Parallel()
	effective := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	graceEnds := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC) // effective + 14 days
	acked := time.Date(2025, 10, 1, 9, 0, 0, 0, time.UTC)
	lapses := acked.Add(AcknowledgmentFreshness)
	recent := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)

	never := Pending{EffectiveDate: &effective}
	lapsed := Pending{EffectiveDate: &effective, LastAcknowledgedAt: &acked}
	// An ack of an older version that has not lapsed counts as none.
	unlapsed := Pending{EffectiveDate: &effective, LastAcknowledgedAt: &recent}

	for _, c := range []struct {
		name  string
		p     Pending
		now   time.Time
		state string
		due   *time.Time
	}{
		{"never, the day it took effect", never, effective, AckDueSoon, &graceEnds},
		{"never, the last instant before the grace day", never, graceEnds.Add(-time.Nanosecond), AckDueSoon, &graceEnds},
		{"never, the end of the grace day", never, graceEnds.Add(24*time.Hour - time.Nanosecond), AckDueSoon, &graceEnds},
		{"never, the day after the grace day", never, graceEnds.AddDate(0, 0, 1), AckOverdue, &graceEnds},
		{"never, two years on", never, effective.AddDate(2, 0, 0), AckOverdue, &graceEnds},
		{"unlapsed old-version ack, in the grace period", unlapsed, graceEnds, AckDueSoon, &graceEnds},
		{"unlapsed old-version ack, after the grace period", unlapsed, graceEnds.AddDate(0, 0, 1), AckOverdue, &graceEnds},
		{"lapsed, the instant it lapsed", lapsed, lapses, AckOverdue, &lapses},
		{"no effective date", Pending{}, graceEnds.AddDate(1, 0, 0), AckDueSoon, nil},
	} {
		due, state := c.p.Due(c.now)
		if state != c.state || (due == nil) != (c.due == nil) || (due != nil && !due.Equal(*c.due)) {
			t.Errorf("%s: Due = %v, %q; want %v, %q", c.name, due, state, c.due, c.state)
		}
	}
}

// A non-UTC effective date is read as its UTC calendar date.
func TestPendingDue2736_UTCDay(t *testing.T) {
	t.Parallel()
	effective := time.Date(2026, 3, 1, 0, 0, 0, 0, time.FixedZone("UTC-8", -8*3600))
	due, _ := Pending{EffectiveDate: &effective}.Due(effective)
	if want := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC); due == nil || !due.Equal(want) {
		t.Errorf("due = %v; want %s", due, want)
	}
}
