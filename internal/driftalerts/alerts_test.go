package driftalerts

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/drift"
	"github.com/mgoodric/security-atlas/internal/freshness"
)

func TestDriftedControls_OnlyControlsLeavingPassing(t *testing.T) {
	stayed := uuid.New()
	left := uuid.New()
	added := uuid.New()

	got := driftedControls(&drift.Snapshot{PassingControlIDs: []uuid.UUID{stayed, left}}, drift.Snapshot{
		PassingControlIDs: []uuid.UUID{stayed, added},
	})
	if len(got) != 1 || got[0] != left {
		t.Fatalf("driftedControls = %v, want only %s", got, left)
	}
}

func TestStaleCrossings_NewlyStaleOnlyAndThreshold(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	newlyStale := uuid.New()
	alreadyStale := uuid.New()
	tooRecent := uuid.New()
	oldEnough := now.Add(-2 * time.Hour)
	recent := now.Add(-5 * time.Minute)

	got := staleCrossings(
		[]freshness.ControlFreshness{
			{ControlID: newlyStale, IsStale: false},
			{ControlID: alreadyStale, IsStale: true},
			{ControlID: tooRecent, IsStale: false},
		},
		[]freshness.ControlFreshness{
			{ControlID: newlyStale, IsStale: true, ValidUntil: &oldEnough},
			{ControlID: alreadyStale, IsStale: true, ValidUntil: &oldEnough},
			{ControlID: tooRecent, IsStale: true, ValidUntil: &recent},
		},
		now,
		time.Hour,
	)
	if len(got) != 1 || got[0].ControlID != newlyStale {
		t.Fatalf("staleCrossings = %+v, want only newly stale old-enough control", got)
	}
}

func TestDebounceBucket_HoldsWithinInterval(t *testing.T) {
	interval := 15 * time.Minute
	a := time.Date(2026, 7, 28, 12, 3, 0, 0, time.UTC)
	b := time.Date(2026, 7, 28, 12, 14, 59, 0, time.UTC)
	c := time.Date(2026, 7, 28, 12, 15, 0, 0, time.UTC)

	if debounceBucket(a, interval) != debounceBucket(b, interval) {
		t.Fatal("times inside the same debounce interval should share a key")
	}
	if debounceBucket(a, interval) == debounceBucket(c, interval) {
		t.Fatal("crossing the debounce interval should produce a fresh key")
	}
}
