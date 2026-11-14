package policy

import (
	"testing"
	"time"
)

func TestReviewStatus(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { v := now.Add(d); return &v }
	day := 24 * time.Hour
	// date is a review date stored as a date-only value: 00:00 UTC.
	date := func(days int) *time.Time {
		v := time.Date(2026, 11, 13+days, 0, 0, 0, 0, time.UTC)
		return &v
	}
	cases := []struct {
		name   string
		status string
		next   *time.Time
		want   string
	}{
		{"no date", StatePublished, nil, ""},
		{"draft with date", StateDraft, at(-day), ""},
		{"superseded with date", StateSuperseded, at(-day), ""},
		{"due yesterday", StatePublished, date(-1), ReviewOverdue},
		{"last second of yesterday", StatePublished, at(-12*time.Hour - time.Second), ReviewOverdue},
		// #2009: due today is not yet overdue, though 00:00 UTC has passed.
		{"due today", StatePublished, date(0), ReviewDueSoon},
		{"earlier today", StatePublished, at(-time.Second), ReviewDueSoon},
		{"now", StatePublished, at(0), ReviewDueSoon},
		{"in 10 days", StatePublished, at(10 * day), ReviewDueSoon},
		{"due in 30 days", StatePublished, date(30), ReviewDueSoon},
		{"late on day 30", StatePublished, at(30*day + 11*time.Hour), ReviewDueSoon},
		{"due in 31 days", StatePublished, date(31), ReviewUpcoming},
	}
	for _, c := range cases {
		p := Policy{Status: c.status, NextReviewAt: c.next}
		if got := p.ReviewStatus(now); got != c.want {
			t.Errorf("%s: ReviewStatus = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestReviewStatus_UTCDay pins the due day to UTC whatever zone the
// caller's clock is in (#2009).
func TestReviewStatus_UTCDay(t *testing.T) {
	t.Parallel()
	// 2026-11-13 20:00 EST is 2026-11-14 01:00 UTC: the UTC day after the
	// due day, though still the due day in New York.
	now := time.Date(2026, 11, 13, 20, 0, 0, 0, time.FixedZone("EST", -5*3600))
	due := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	p := Policy{Status: StatePublished, NextReviewAt: &due}
	if got := p.ReviewStatus(now); got != ReviewOverdue {
		t.Errorf("ReviewStatus = %q, want %q", got, ReviewOverdue)
	}
}

func TestPgTimestamptz(t *testing.T) {
	t.Parallel()
	if pgTimestamptz(nil).Valid {
		t.Error("nil should map to NULL")
	}
	in := time.Date(2027, 1, 15, 9, 0, 0, 0, time.FixedZone("x", 3600))
	got := pgTimestamptz(&in)
	if !got.Valid || !got.Time.Equal(in) || got.Time.Location() != time.UTC {
		t.Errorf("pgTimestamptz = %+v", got)
	}
}
