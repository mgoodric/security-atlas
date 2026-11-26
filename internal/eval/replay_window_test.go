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

	row, err := (&Engine{}).computeRow(context.Background(), nil, meta, records, nil, nil, uuid.New(), TriggerReplay, at)
	if err != nil {
		t.Fatalf("computeRow: %v", err)
	}
	if row.freshnessStatus != FreshnessFresh || row.evidenceCountInWindow != 1 || row.result != ResultPass {
		t.Fatalf("replay of T = %s/%s in_window=%d, want pass/fresh in_window=1",
			row.result, row.freshnessStatus, row.evidenceCountInWindow)
	}

	// Live: the instant is the wall clock, so the same record is stale and
	// out of window.
	row, err = (&Engine{}).computeRow(context.Background(), nil, meta, records, nil, nil, uuid.New(), TriggerManual, wall)
	if err != nil {
		t.Fatalf("computeRow: %v", err)
	}
	if row.freshnessStatus != FreshnessStale || row.evidenceCountInWindow != 0 {
		t.Fatalf("live = %s in_window=%d, want stale in_window=0", row.freshnessStatus, row.evidenceCountInWindow)
	}
}

// #3037: an exception's [effective_from, expires_at) window is judged at the
// evaluation instant. It was in force at T and has lapsed by the wall clock,
// so a replay of T is excepted and a live evaluation is not.
func TestComputeRow_ReplayExceptionWindowAnchorsOnInstant(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	wall := at.Add(10 * 24 * time.Hour)
	records := []allRecord{{observedAt: at.Add(-time.Hour), result: ResultFail, ingestedAt: at.Add(-time.Hour), id: "e1"}}
	meta := controlMeta{id: uuid.New(), freshnessClass: "monthly"}
	from := at.Add(-24 * time.Hour)
	exceptions := []activeException{{predicate: []byte(`{}`), effectiveFrom: &from, expiresAt: at.Add(24 * time.Hour)}}

	row, err := (&Engine{}).computeRow(context.Background(), nil, meta, records, exceptions, nil, uuid.New(), TriggerReplay, at)
	if err != nil {
		t.Fatalf("computeRow: %v", err)
	}
	if row.result != ResultExcepted {
		t.Fatalf("replay of T = %s, want excepted", row.result)
	}

	row, err = (&Engine{}).computeRow(context.Background(), nil, meta, records, exceptions, nil, uuid.New(), TriggerManual, wall)
	if err != nil {
		t.Fatalf("computeRow: %v", err)
	}
	if row.result != ResultFail {
		t.Fatalf("live = %s, want fail", row.result)
	}
}

// #3038: live evaluation keeps only rows ingested by its instant, the bound
// a replay of that instant applies in SQL.
func TestIngestedBy_KeepsRowsAtOrBeforeHorizon(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	records := []allRecord{
		{id: "before", ingestedAt: at.Add(-time.Second)},
		{id: "at", ingestedAt: at},
		{id: "after", ingestedAt: at.Add(time.Microsecond)},
	}
	got := ingestedBy(records, at)
	if len(got) != 2 || got[0].id != "before" || got[1].id != "at" {
		t.Fatalf("ingestedBy = %+v, want [before at]", got)
	}
	if len(records) != 3 || records[2].id != "after" {
		t.Fatalf("ingestedBy changed its input: %+v", records)
	}
}
