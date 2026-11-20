package api

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// backgroundGroup tracks work a handler starts and does not wait for
// (#2688). It is a sync.WaitGroup that can be closed: once close has run,
// Go no longer adds to the group, so a handler that outlives a timed-out
// srv.Shutdown cannot call Add while wait is inside wg.Wait, which
// sync.WaitGroup forbids (#2748). The mutex makes the closed check and the
// Add one step; an atomic flag alone would leave a gap between them.
type backgroundGroup struct {
	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
	// logger reports work started after close. Nil uses slog.Default.
	logger *slog.Logger
}

// Go runs f in its own goroutine. Before close, the goroutine is tracked
// and wait waits for it. After close, f still runs, detached and logged:
// the process is shutting down and may exit before it finishes.
func (b *backgroundGroup) Go(f func()) {
	b.mu.Lock()
	if !b.closed {
		b.wg.Add(1)
		b.mu.Unlock()
		go func() {
			defer b.wg.Done()
			f()
		}()
		return
	}
	b.mu.Unlock()
	logger := b.logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Warn("api: background work started after shutdown began; running untracked")
	go f()
}

// wait closes the group and waits for the work it tracks, or until ctx is
// done, whichever is first. It shares RunHTTP's shutdown grace, so a stuck
// re-evaluation cannot hold the process open past it; it reports that case
// rather than hiding it.
func (b *backgroundGroup) wait(ctx context.Context) error {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	done := make(chan struct{})
	go func() {
		b.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("api: background work still running at shutdown: %w", ctx.Err())
	}
}
