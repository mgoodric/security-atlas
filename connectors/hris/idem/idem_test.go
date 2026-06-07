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
