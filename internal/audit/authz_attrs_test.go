package audit

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// #2157: the authz hydrator places an evidence record in a population with
// the same time bounds a sample draw uses (ListPopulationEvidenceIDs):
// inside [start, end] and at or before frozen_at.
func TestPopulationWindowCovers(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)
	frozen := time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC)
	ts := func(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }
	live := dbx.Population{TimeWindowStart: ts(start), TimeWindowEnd: ts(end)}
	froze := live
	froze.FrozenAt = ts(frozen)
	at := func(t time.Time) *time.Time { return &t }

	cases := []struct {
		name string
		pop  dbx.Population
		at   *time.Time
		want bool
	}{
		{"inside", live, at(start.AddDate(0, 1, 0)), true},
		{"on the start", live, at(start), true},
		{"on the end", live, at(end), true},
		{"before the start", live, at(start.Add(-time.Second)), false},
		{"after the end", live, at(end.Add(time.Second)), false},
		{"at the freeze", froze, at(frozen), true},
		{"after the freeze, inside the window", froze, at(frozen.Add(time.Second)), false},
		{"no observation time asked", froze, nil, true},
	}
	for _, tc := range cases {
		if got := populationWindowCovers(tc.pop, tc.at); got != tc.want {
			t.Errorf("%s: covers = %v; want %v", tc.name, got, tc.want)
		}
	}
}
