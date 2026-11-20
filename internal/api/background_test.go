package api

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// #2688: RunHTTP waits for tracked background work after Shutdown, bounded
// by the shutdown grace.
func TestBackgroundGroupWait(t *testing.T) {
	t.Parallel()

	t.Run("returns once the work finishes", func(t *testing.T) {
		t.Parallel()
		var b backgroundGroup
		finished := false
		b.Go(func() {
			time.Sleep(20 * time.Millisecond)
			finished = true
		})
		if err := b.wait(context.Background()); err != nil {
			t.Fatalf("wait: %v", err)
		}
		if !finished {
			t.Fatal("wait returned before the work finished")
		}
	})

	t.Run("gives up at the deadline", func(t *testing.T) {
		t.Parallel()
		var b backgroundGroup
		release := make(chan struct{})
		defer close(release)
		b.Go(func() { <-release })
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		err := b.wait(ctx)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("wait = %v, want context.DeadlineExceeded", err)
		}
	})
}

// #2748: a handler that outlives a timed-out Shutdown can call Go after
// wait has begun. That work must still run, but it must not be added to the
// group: sync.WaitGroup panics on an Add that races its Wait.
func TestBackgroundGroupGoAfterWait(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	b := backgroundGroup{logger: slog.New(slog.NewTextHandler(&logs, nil))}

	// Work that is still running when the grace ends keeps wait's helper
	// goroutine inside wg.Wait after wait returns.
	release := make(chan struct{})
	b.Go(func() { <-release })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := b.wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait = %v, want context.DeadlineExceeded", err)
	}

	ran := make(chan struct{})
	b.Go(func() { close(ran) })
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("work started after wait did not run")
	}
	if !strings.Contains(logs.String(), "after shutdown began") {
		t.Fatalf("late work was not logged; logs = %q", logs.String())
	}

	// The late work was not tracked: the group drains as soon as the
	// stuck work ends, with no stray Add holding it open.
	close(release)
	if err := b.wait(context.Background()); err != nil {
		t.Fatalf("second wait: %v", err)
	}
}

// #2748: Go and wait racing each other is safe. Run under -race, this
// fails on an Add concurrent with Wait, which the mutex rules out.
func TestBackgroundGroupGoRacesWait(t *testing.T) {
	t.Parallel()
	for range 200 {
		b := backgroundGroup{logger: slog.New(slog.DiscardHandler)}
		var ran atomic.Int32
		var callers sync.WaitGroup
		start := make(chan struct{})
		for range 8 {
			callers.Go(func() {
				<-start
				b.Go(func() { ran.Add(1) })
			})
		}
		close(start)
		if err := b.wait(context.Background()); err != nil {
			t.Fatalf("wait: %v", err)
		}
		callers.Wait()
		deadline := time.Now().Add(5 * time.Second)
		for ran.Load() != 8 {
			if time.Now().After(deadline) {
				t.Fatalf("ran %d of 8", ran.Load())
			}
			time.Sleep(time.Millisecond)
		}
	}
}
