package drift

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestGainsAndFlips_SameWindow pins #2063: gains and losses come from the
// same snapshot window, so a panel built from them cannot pair a net gain
// with a "nothing changed" loss list.
func TestGainsAndFlips_SameWindow(t *testing.T) {
	t.Parallel()
	a, b, c, d, e := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	day := func(n int) time.Time { return time.Date(2026, 11, n, 0, 0, 0, 0, time.UTC) }
	snaps := []Snapshot{
		{SnapshotDate: day(1), PassingControlIDs: []uuid.UUID{a, e}},
		{SnapshotDate: day(2), PassingControlIDs: []uuid.UUID{a, b, e}},
		// a dips and recovers: a gain, mirroring a flip that rose and fell.
		{SnapshotDate: day(3), PassingControlIDs: []uuid.UUID{b, c, d}},
		{SnapshotDate: day(4), PassingControlIDs: []uuid.UUID{a, b, c, d}},
	}

	gains := computeGains(snaps)
	gotGains := map[uuid.UUID]bool{}
	for _, g := range gains {
		gotGains[g.ControlID] = true
	}
	if len(gains) != 4 || !gotGains[a] || !gotGains[b] || !gotGains[c] || !gotGains[d] {
		t.Fatalf("gains = %v, want a, b, c, d", gains)
	}

	flips := computeFlips(snaps)
	if len(flips) != 1 || flips[0].ControlID != e || !flips[0].LastPassing.Equal(day(2)) {
		t.Fatalf("flips = %v, want e last passing day 2", flips)
	}
}

func TestComputeGains_FewerThanTwoSnapshots(t *testing.T) {
	t.Parallel()
	if got := computeGains(nil); got != nil {
		t.Fatalf("nil snaps: got %v", got)
	}
	one := []Snapshot{{PassingControlIDs: []uuid.UUID{uuid.New()}}}
	if got := computeGains(one); got != nil {
		t.Fatalf("one snap: got %v", got)
	}
}

func TestComputeGains_NoChange(t *testing.T) {
	t.Parallel()
	a := uuid.New()
	snaps := []Snapshot{
		{PassingControlIDs: []uuid.UUID{a}},
		{PassingControlIDs: []uuid.UUID{a}},
	}
	if got := computeGains(snaps); len(got) != 0 {
		t.Fatalf("got %v, want none", got)
	}
	if got := computeFlips(snaps); len(got) != 0 {
		t.Fatalf("flips %v, want none", got)
	}
}
