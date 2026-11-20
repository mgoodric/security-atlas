package api

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// #2688: RunHTTP waits for tracked background work after Shutdown, bounded
// by the shutdown grace.
func TestWaitBackground(t *testing.T) {
	t.Parallel()

	t.Run("returns once the work finishes", func(t *testing.T) {
		t.Parallel()
		var wg sync.WaitGroup
		finished := false
		wg.Go(func() {
			time.Sleep(20 * time.Millisecond)
			finished = true
		})
		if err := waitBackground(context.Background(), &wg); err != nil {
			t.Fatalf("waitBackground: %v", err)
		}
		if !finished {
			t.Fatal("waitBackground returned before the work finished")
		}
	})

	t.Run("gives up at the deadline", func(t *testing.T) {
		t.Parallel()
		var wg sync.WaitGroup
		release := make(chan struct{})
		defer close(release)
		wg.Go(func() { <-release })
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		err := waitBackground(ctx, &wg)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("waitBackground = %v, want context.DeadlineExceeded", err)
		}
	})
}
