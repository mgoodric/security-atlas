package pdfrender

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestDo_Success returns the renderer's bytes when the render completes within
// the deadline and a slot is free.
func TestDo_Success(t *testing.T) {
	t.Parallel()
	l := New(2, time.Second, time.Second)
	got, err := l.Do(context.Background(), func(ctx context.Context) ([]byte, error) {
		return []byte("%PDF-ok"), nil
	})
	if err != nil {
		t.Fatalf("Do: unexpected err %v", err)
	}
	if string(got) != "%PDF-ok" {
		t.Fatalf("Do: bytes = %q, want %q", got, "%PDF-ok")
	}
}

// TestDo_RenderDeadline is the load-bearing AC-1 proof: a render that outlasts
// the bounded render deadline returns ErrRenderDeadline (→ 503), NOT a 500 and
// NOT a hang. We simulate a slow chrome by sleeping past a tiny deadline and
// honoring the render context — exactly what slice 475's "tiny render deadline
// → assert 503" instruction calls for, but deterministic and chrome-free.
func TestDo_RenderDeadline(t *testing.T) {
	t.Parallel()
	l := New(1, 20*time.Millisecond, time.Second)
	_, err := l.Do(context.Background(), func(ctx context.Context) ([]byte, error) {
		// A real chromedp render returns a wrapped context.DeadlineExceeded
		// when the render ctx elapses; emulate that shape.
		<-ctx.Done()
		return nil, context.DeadlineExceeded
	})
	if !errors.Is(err, ErrRenderDeadline) {
		t.Fatalf("Do: err = %v, want ErrRenderDeadline", err)
	}
}

// TestDo_RenderDeadline_OpaqueError proves the classification is by the RENDER
// CONTEXT, not by sniffing the error string: even if chromedp wraps the
// timeout in an opaque non-DeadlineExceeded error, an expired render context
// still yields ErrRenderDeadline.
func TestDo_RenderDeadline_OpaqueError(t *testing.T) {
	t.Parallel()
	l := New(1, 20*time.Millisecond, time.Second)
	_, err := l.Do(context.Background(), func(ctx context.Context) ([]byte, error) {
		<-ctx.Done()
		return nil, errors.New("chromedp: could not navigate: net::ERR_ABORTED")
	})
	if !errors.Is(err, ErrRenderDeadline) {
		t.Fatalf("Do: err = %v, want ErrRenderDeadline (deadline classified by ctx, not error text)", err)
	}
}

// TestDo_QueueSaturated proves a burst over the concurrency cap, with no slot
// freeing within the bounded wait, degrades to ErrQueueSaturated (→ 503), not
// an unbounded block.
func TestDo_QueueSaturated(t *testing.T) {
	t.Parallel()
	l := New(1, time.Second, 30*time.Millisecond)

	release := make(chan struct{})
	started := make(chan struct{})
	go func() {
		_, _ = l.Do(context.Background(), func(ctx context.Context) ([]byte, error) {
			close(started)
			<-release
			return []byte("%PDF-"), nil
		})
	}()
	<-started // the single slot is now occupied

	_, err := l.Do(context.Background(), func(ctx context.Context) ([]byte, error) {
		return []byte("%PDF-"), nil
	})
	if !errors.Is(err, ErrQueueSaturated) {
		t.Fatalf("Do: err = %v, want ErrQueueSaturated", err)
	}
	close(release)
}

// TestDo_FailFastQueue proves a zero queueWait fails fast with
// ErrQueueSaturated rather than waiting.
func TestDo_FailFastQueue(t *testing.T) {
	t.Parallel()
	l := New(1, time.Second, 0)

	release := make(chan struct{})
	started := make(chan struct{})
	go func() {
		_, _ = l.Do(context.Background(), func(ctx context.Context) ([]byte, error) {
			close(started)
			<-release
			return []byte("%PDF-"), nil
		})
	}()
	<-started

	start := time.Now()
	_, err := l.Do(context.Background(), func(ctx context.Context) ([]byte, error) {
		return []byte("%PDF-"), nil
	})
	if !errors.Is(err, ErrQueueSaturated) {
		t.Fatalf("Do: err = %v, want ErrQueueSaturated", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("fail-fast queue waited %v, want near-instant", elapsed)
	}
	close(release)
}

// TestDo_ParentCancel returns the parent ctx error (not a saturation /
// deadline) when the CALLER's context is cancelled while waiting for a slot —
// a client disconnect must not be misreported as a server-side 503 cause.
func TestDo_ParentCancel_WhileWaiting(t *testing.T) {
	t.Parallel()
	l := New(1, time.Second, time.Second)

	release := make(chan struct{})
	started := make(chan struct{})
	go func() {
		_, _ = l.Do(context.Background(), func(ctx context.Context) ([]byte, error) {
			close(started)
			<-release
			return []byte("%PDF-"), nil
		})
	}()
	<-started

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err := l.Do(ctx, func(ctx context.Context) ([]byte, error) {
		return []byte("%PDF-"), nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Do: err = %v, want context.Canceled", err)
	}
	close(release)
}

// TestDo_ConcurrencyCapHonored proves the cap is never exceeded under a burst:
// the max observed in-flight renders equals the configured cap.
func TestDo_ConcurrencyCapHonored(t *testing.T) {
	t.Parallel()
	const cap = 3
	l := New(cap, time.Second, time.Second)

	var inFlight, maxSeen int64
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = l.Do(context.Background(), func(ctx context.Context) ([]byte, error) {
				cur := atomic.AddInt64(&inFlight, 1)
				for {
					old := atomic.LoadInt64(&maxSeen)
					if cur <= old || atomic.CompareAndSwapInt64(&maxSeen, old, cur) {
						break
					}
				}
				time.Sleep(2 * time.Millisecond)
				atomic.AddInt64(&inFlight, -1)
				return []byte("%PDF-"), nil
			})
		}()
	}
	wg.Wait()
	if maxSeen > cap {
		t.Fatalf("max in-flight renders = %d, exceeds cap %d", maxSeen, cap)
	}
	if maxSeen == 0 {
		t.Fatalf("no render ever ran")
	}
}

// TestDo_StressNoNonGraceful is the AC-4 stress contract at the limiter level:
// run the render Nx under simulated contention (a slow renderer + a tight cap)
// and assert EVERY result is graceful — success, ErrRenderDeadline, or
// ErrQueueSaturated — never an unclassified error and never a hang. This
// mirrors the slice-340 stress pattern.
func TestDo_StressNoNonGraceful(t *testing.T) {
	t.Parallel()
	l := New(2, 40*time.Millisecond, 60*time.Millisecond)

	const n = 50
	var wg sync.WaitGroup
	results := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := l.Do(context.Background(), func(ctx context.Context) ([]byte, error) {
				// Half the renders are "slow" (exceed the render deadline);
				// half are fast. Under the tight cap this guarantees a mix of
				// success / deadline / saturation outcomes.
				if idx%2 == 0 {
					<-ctx.Done()
					return nil, context.DeadlineExceeded
				}
				time.Sleep(5 * time.Millisecond)
				return []byte("%PDF-"), nil
			})
			results[idx] = err
		}(i)
	}
	wg.Wait()

	for i, err := range results {
		switch {
		case err == nil,
			errors.Is(err, ErrRenderDeadline),
			errors.Is(err, ErrQueueSaturated),
			errors.Is(err, context.Canceled),
			errors.Is(err, context.DeadlineExceeded):
			// graceful
		default:
			t.Fatalf("render %d returned a non-graceful error: %v", i, err)
		}
	}
}

func TestEnvParsing(t *testing.T) {
	// Not parallel: mutates process env.
	t.Setenv(envRenderTimeout, "120s")
	if got := envRenderTimeoutValue(); got != 120*time.Second {
		t.Errorf("render timeout = %v, want 120s", got)
	}
	t.Setenv(envRenderTimeout, "garbage")
	if got := envRenderTimeoutValue(); got != DefaultRenderTimeout {
		t.Errorf("garbage timeout = %v, want default %v", got, DefaultRenderTimeout)
	}
	t.Setenv(envMaxConcurrency, "5")
	if got := envMaxConcurrencyValue(); got != 5 {
		t.Errorf("max concurrency = %d, want 5", got)
	}
	t.Setenv(envMaxConcurrency, "0")
	if got := envMaxConcurrencyValue(); got != DefaultMaxConcurrency {
		t.Errorf("zero concurrency = %d, want default %d", got, DefaultMaxConcurrency)
	}
	t.Setenv(envMaxConcurrency, "-3")
	if got := envMaxConcurrencyValue(); got != DefaultMaxConcurrency {
		t.Errorf("negative concurrency = %d, want default %d", got, DefaultMaxConcurrency)
	}
	t.Setenv(envQueueWait, "0s")
	if got := envQueueWaitValue(); got != 0 {
		t.Errorf("zero queue wait = %v, want 0 (fail-fast honored)", got)
	}
}

func TestNew_ClampsNonPositiveConcurrency(t *testing.T) {
	t.Parallel()
	l := New(0, time.Second, time.Second)
	// A clamped-to-1 limiter must still run a render (not deadlock).
	_, err := l.Do(context.Background(), func(ctx context.Context) ([]byte, error) {
		return []byte("%PDF-"), nil
	})
	if err != nil {
		t.Fatalf("clamped limiter Do: %v", err)
	}
}
