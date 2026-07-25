package backup

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunnerFiresImmediatelyThenStops(t *testing.T) {
	t.Parallel()
	var calls int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runner(ctx, time.Hour, nil, "test", func(context.Context) error {
			atomic.AddInt32(&calls, 1)
			return nil
		})
		close(done)
	}()
	// The immediate fire happens synchronously before the ticker loop; give
	// the goroutine a moment, then cancel so the loop returns.
	deadline := time.After(2 * time.Second)
	for atomic.LoadInt32(&calls) == 0 {
		select {
		case <-deadline:
			t.Fatal("runner did not fire immediately")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not stop on ctx cancel")
	}
	if got := atomic.LoadInt32(&calls); got < 1 {
		t.Fatalf("expected >=1 sweep, got %d", got)
	}
}

func TestRunnerSwallowsSweepError(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runner(ctx, time.Hour, nil, "test", func(context.Context) error {
			return errors.New("boom") // must NOT kill the loop
		})
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not stop after a sweep error")
	}
}

func TestAlertConfigFromEnv(t *testing.T) {
	t.Parallel()
	// Explicit alert tenant wins.
	env := map[string]string{
		"ATLAS_BACKUP_ALERT_TENANT":    "tenant-A",
		"ATLAS_BOOTSTRAP_TENANT":       "tenant-B",
		"ATLAS_BACKUP_ALERT_RECIPIENT": "admin-1",
	}
	tid, rec := AlertConfigFromEnv(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if tid != "tenant-A" || rec != "admin-1" {
		t.Fatalf("explicit alert tenant: got %q/%q", tid, rec)
	}

	// Falls back to bootstrap tenant.
	env2 := map[string]string{
		"ATLAS_BOOTSTRAP_TENANT":       "tenant-B",
		"ATLAS_BACKUP_ALERT_RECIPIENT": "admin-2",
	}
	tid, rec = AlertConfigFromEnv(func(k string) (string, bool) { v, ok := env2[k]; return v, ok })
	if tid != "tenant-B" || rec != "admin-2" {
		t.Fatalf("bootstrap fallback: got %q/%q", tid, rec)
	}

	// Unconfigured -> empty (alerter goes log-only).
	tid, rec = AlertConfigFromEnv(func(string) (string, bool) { return "", false })
	if tid != "" || rec != "" {
		t.Fatalf("unconfigured: got %q/%q", tid, rec)
	}
}

func TestNotificationAlerterInertWhenUnconfigured(t *testing.T) {
	t.Parallel()
	// nil pool is safe to pass because the early return fires before any DB
	// access when tenant/recipient are empty.
	alert := NewNotificationAlerter(nil, "", "", nil)
	alert(context.Background(), "backup failed: dump") // must not panic / touch DB
}

func TestIsS3NotFound(t *testing.T) {
	t.Parallel()
	if !isS3NotFound(errors.New("operation error S3: GetObject, NoSuchKey: ...")) {
		t.Error("NoSuchKey should be not-found")
	}
	if !isS3NotFound(errors.New("https response error StatusCode: 404")) {
		t.Error("404 should be not-found")
	}
	if isS3NotFound(nil) {
		t.Error("nil is not not-found")
	}
	if isS3NotFound(errors.New("AccessDenied")) {
		t.Error("AccessDenied is not not-found")
	}
}
