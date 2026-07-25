package idem

import (
	"testing"
	"time"
)

func TestWorkerLifecycleKey_StableWithinHour(t *testing.T) {
	t.Parallel()
	a := WorkerLifecycleKey("rippling", "w1", time.Date(2026, 6, 7, 12, 5, 0, 0, time.UTC))
	b := WorkerLifecycleKey("rippling", "w1", time.Date(2026, 6, 7, 12, 55, 0, 0, time.UTC))
	if a != b {
		t.Fatalf("same hour should dedupe: %q != %q", a, b)
	}
}

func TestWorkerLifecycleKey_DiffersAcrossHour(t *testing.T) {
	t.Parallel()
	a := WorkerLifecycleKey("rippling", "w1", time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))
	b := WorkerLifecycleKey("rippling", "w1", time.Date(2026, 6, 7, 13, 0, 0, 0, time.UTC))
	if a == b {
		t.Fatal("different hour should differ")
	}
}

func TestWorkerLifecycleKey_DiffersAcrossHRISAndWorker(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	if WorkerLifecycleKey("rippling", "w1", at) == WorkerLifecycleKey("bamboohr", "w1", at) {
		t.Error("different hris should differ")
	}
	if WorkerLifecycleKey("rippling", "w1", at) == WorkerLifecycleKey("rippling", "w2", at) {
		t.Error("different worker should differ")
	}
}

func TestWorkerLifecycleKey_HexLength(t *testing.T) {
	t.Parallel()
	if got := WorkerLifecycleKey("rippling", "w1", time.Now()); len(got) != 64 {
		t.Errorf("key length = %d; want 64 (sha256 hex)", len(got))
	}
}

func TestManagerHierarchyKey_StableWithinHour(t *testing.T) {
	t.Parallel()
	a := ManagerHierarchyKey("rippling", "w1", time.Date(2026, 6, 7, 12, 5, 0, 0, time.UTC))
	b := ManagerHierarchyKey("rippling", "w1", time.Date(2026, 6, 7, 12, 55, 0, 0, time.UTC))
	if a != b {
		t.Fatalf("same hour should dedupe: %q != %q", a, b)
	}
}

func TestManagerHierarchyKey_DiffersAcrossHourHRISAndWorker(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	if ManagerHierarchyKey("rippling", "w1", at) == ManagerHierarchyKey("rippling", "w1", at.Add(time.Hour)) {
		t.Error("different hour should differ")
	}
	if ManagerHierarchyKey("rippling", "w1", at) == ManagerHierarchyKey("bamboohr", "w1", at) {
		t.Error("different hris should differ")
	}
	if ManagerHierarchyKey("rippling", "w1", at) == ManagerHierarchyKey("rippling", "w2", at) {
		t.Error("different worker should differ")
	}
}

// TestManagerHierarchyKey_DistinctFromLifecycleKey: the two HRIS kinds must NOT
// collide in the ledger for the same (hris, worker, hour).
func TestManagerHierarchyKey_DistinctFromLifecycleKey(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	if ManagerHierarchyKey("rippling", "w1", at) == WorkerLifecycleKey("rippling", "w1", at) {
		t.Error("hierarchy and lifecycle keys must differ (distinct kind prefix)")
	}
}

func TestManagerHierarchyKey_HexLength(t *testing.T) {
	t.Parallel()
	if got := ManagerHierarchyKey("rippling", "w1", time.Now()); len(got) != 64 {
		t.Errorf("key length = %d; want 64 (sha256 hex)", len(got))
	}
}
