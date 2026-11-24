package eval

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// #1699: a replay of T measures freshness from T, not from the wall clock.
// The record was fresh at T; ten days later it is past the daily class's
// 7-day window, and a replay of T must still say what the platform said at T.
func TestComputeRow_ReplayWindowAnchorsOnInstant(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	wall := at.Add(10 * 24 * time.Hour)
	records := []allRecord{{observedAt: at.Add(-time.Hour), result: ResultPass, ingestedAt: at.Add(-time.Hour), id: "e1"}}
	meta := controlMeta{id: uuid.New(), freshnessClass: "daily"}

	row, err := (&Engine{}).computeRow(context.Background(), nil, meta, records, nil, nil, uuid.New(), TriggerReplay, wall, at)
	if err != nil {
		t.Fatalf("computeRow: %v", err)
	}
	if row.freshnessStatus != FreshnessFresh || row.evidenceCountInWindow != 1 || row.result != ResultPass {
		t.Fatalf("replay of T = %s/%s in_window=%d, want pass/fresh in_window=1",
			row.result, row.freshnessStatus, row.evidenceCountInWindow)
	}

	// Live: the instant is the wall clock, so the same record is stale and
	// out of window.
	row, err = (&Engine{}).computeRow(context.Background(), nil, meta, records, nil, nil, uuid.New(), TriggerManual, wall, wall)
	if err != nil {
		t.Fatalf("computeRow: %v", err)
	}
	if row.freshnessStatus != FreshnessStale || row.evidenceCountInWindow != 0 {
		t.Fatalf("live = %s in_window=%d, want stale in_window=0", row.freshnessStatus, row.evidenceCountInWindow)
	}
}
