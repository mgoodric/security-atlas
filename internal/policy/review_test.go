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
	cases := []struct {
		name   string
		status string
		next   *time.Time
		want   string
	}{
		{"no date", StatePublished, nil, ""},
		{"draft with date", StateDraft, at(-day), ""},
		{"superseded with date", StateSuperseded, at(-day), ""},
		{"past", StatePublished, at(-time.Second), ReviewOverdue},
		{"now", StatePublished, at(0), ReviewDueSoon},
		{"in 10 days", StatePublished, at(10 * day), ReviewDueSoon},
		{"at 30 days", StatePublished, at(30 * day), ReviewDueSoon},
		{"past 30 days", StatePublished, at(30*day + time.Second), ReviewUpcoming},
	}
	for _, c := range cases {
		p := Policy{Status: c.status, NextReviewAt: c.next}
		if got := p.ReviewStatus(now); got != c.want {
			t.Errorf("%s: ReviewStatus = %q, want %q", c.name, got, c.want)
		}
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
