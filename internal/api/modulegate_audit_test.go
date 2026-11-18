package api

import (
	"testing"
	"time"
)

func TestGatedAttemptAuditorDedupesPerWindow(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_800_000_000, 0)
	a := newGatedAttemptAuditor(nil)
	a.now = func() time.Time { return now }

	if !a.first("t\x00u\x00questionnaires.enabled") {
		t.Fatal("first attempt not recorded")
	}
	if a.first("t\x00u\x00questionnaires.enabled") {
		t.Fatal("repeat inside the window recorded")
	}
	if !a.first("t\x00other\x00questionnaires.enabled") {
		t.Fatal("another actor's attempt deduped")
	}
	if !a.first("t\x00u\x00risk.enabled") {
		t.Fatal("another switch's attempt deduped")
	}
	now = now.Add(gatedAttemptWindow)
	if !a.first("t\x00u\x00questionnaires.enabled") {
		t.Fatal("attempt after the window not recorded")
	}
}

// Past the cap the map is pruned or reset, never left to grow, and a reset
// records again rather than skip.
func TestGatedAttemptAuditorCap(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_800_000_000, 0)
	a := newGatedAttemptAuditor(nil)
	a.now = func() time.Time { return now }
	for i := 0; i < gatedAttemptCap; i++ {
		a.first(string(rune(i)) + "k")
	}
	if !a.first("new") {
		t.Fatal("attempt past the cap not recorded")
	}
	if len(a.seen) > gatedAttemptCap {
		t.Fatalf("seen has %d entries; cap %d", len(a.seen), gatedAttemptCap)
	}
}
