// Package pdfrender provides a process-wide governor for the headless-Chrome
// (chromedp) PDF render path shared by internal/board and
// internal/questionnaire.
//
// Slice 475 — the PDF endpoints render via headless chromium. Under sustained
// load the render previously neither completed (200) within its deadline nor
// degraded cleanly to 503: a deadline-exceeded chromedp error fell through the
// handler's ErrChromeUnavailable check and surfaced as a 500 (or, worse, a
// request that hung past the client deadline). This package makes the render
// path degrade DETERMINISTICALLY:
//
//   - a single, shared concurrency cap (semaphore) so a burst of PDF requests
//     across BOTH packages cannot exhaust headless Chrome — the whole point of
//     a SHARED limiter is that chrome is one OS-level resource; a per-package
//     cap would let board+questionnaires bursting together exceed it (decisions
//     log D1);
//   - a bounded wait for a slot — over-cap callers wait up to a budget then are
//     rejected with ErrQueueSaturated (mapped to 503), never blocked forever;
//   - a bounded, env-tunable render deadline — a deadline-exceeded render is
//     reported as ErrRenderDeadline (mapped to 503), never a 500 / hang.
//
// The taxonomy (ErrQueueSaturated / ErrRenderDeadline / the renderer's own
// ErrChromeUnavailable) lets the handler emit a WARN that distinguishes "chrome
// absent" from "render deadline exceeded" from "render queue saturated"
// (AC-5).
//
// This is complementary to slices 340/341, which fixed chromedp CONNECTION
// SETUP (the WSURLReadTimeout watchdog). This package governs RENDER-TIME
// behavior and does not touch the connection-setup fix.
package pdfrender

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync"
	"time"
)

// Tunables. All three are overridable by env var so an operator on a
// resource-constrained or unusually loaded host can adjust without a rebuild
// (AC-2 / AC-3). Defaults are chosen for a single-VM self-host (CLAUDE.md
// deployment target):
//
//   - DefaultRenderTimeout (90s) is raised well above the ~30s that flaked on
//     a loaded CI runner so a healthy render completes with generous headroom
//     while still bounding a hang (decisions log D2). Override:
//     ATLAS_PDF_RENDER_TIMEOUT (Go duration, e.g. "120s").
//   - DefaultMaxConcurrency (2) caps simultaneous chrome renders so a burst
//     cannot spawn unbounded headless-chrome processes (decisions log D3).
//     Override: ATLAS_PDF_RENDER_MAX_CONCURRENCY (positive integer).
//   - DefaultQueueWait (15s) is how long an over-cap caller waits for a slot
//     before degrading to 503 (decisions log D4). Override:
//     ATLAS_PDF_RENDER_QUEUE_WAIT (Go duration, e.g. "20s"). A zero/negative
//     override means "do not wait" (fail fast when the cap is full).
const (
	DefaultRenderTimeout  = 90 * time.Second
	DefaultMaxConcurrency = 2
	DefaultQueueWait      = 15 * time.Second

	envRenderTimeout  = "ATLAS_PDF_RENDER_TIMEOUT"
	envMaxConcurrency = "ATLAS_PDF_RENDER_MAX_CONCURRENCY"
	envQueueWait      = "ATLAS_PDF_RENDER_QUEUE_WAIT"
)

var (
	// ErrRenderDeadline signals the bounded render deadline elapsed before the
	// render produced a PDF. The HTTP handler maps this to 503 (graceful
	// degradation) — NEVER a 500 or a hung request (AC-1).
	ErrRenderDeadline = errors.New("pdfrender: render deadline exceeded")

	// ErrQueueSaturated signals the concurrency cap is full and the bounded
	// wait for a slot elapsed. The HTTP handler maps this to 503 (AC-3).
	ErrQueueSaturated = errors.New("pdfrender: render queue saturated")
)

// Limiter is the process-wide render governor. The zero value is not usable;
// use the package-level Default or construct one with New.
type Limiter struct {
	sem           chan struct{}
	renderTimeout time.Duration
	queueWait     time.Duration
}

// New builds a Limiter from explicit values. maxConcurrency < 1 is clamped to
// 1 (a non-positive cap would deadlock every render). Exposed for tests; the
// production path uses Default.
func New(maxConcurrency int, renderTimeout, queueWait time.Duration) *Limiter {
	if maxConcurrency < 1 {
		maxConcurrency = 1
	}
	if renderTimeout <= 0 {
		renderTimeout = DefaultRenderTimeout
	}
	return &Limiter{
		sem:           make(chan struct{}, maxConcurrency),
		renderTimeout: renderTimeout,
		queueWait:     queueWait,
	}
}

var (
	defaultOnce sync.Once
	defaultLim  *Limiter
)

var defaultMu sync.RWMutex

// Default returns the process-wide shared Limiter, constructed once from the
// environment. Board and questionnaire renders share this single instance so
// the concurrency cap governs TOTAL chrome usage, not per-package usage
// (decisions log D1).
func Default() *Limiter {
	defaultMu.RLock()
	if defaultLim != nil {
		l := defaultLim
		defaultMu.RUnlock()
		return l
	}
	defaultMu.RUnlock()

	defaultOnce.Do(func() {
		defaultMu.Lock()
		if defaultLim == nil {
			defaultLim = New(
				envMaxConcurrencyValue(),
				envRenderTimeoutValue(),
				envQueueWaitValue(),
			)
		}
		defaultMu.Unlock()
	})
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultLim
}

// SetDefaultForTest swaps the process-wide limiter and returns a restore func.
// It exists so integration tests can deterministically drive the degradation
// paths (e.g. a tiny render deadline → ErrRenderDeadline → 503, or a 1-slot
// cap → ErrQueueSaturated → 503) without a real slow/contended chrome. NOT for
// production use.
func SetDefaultForTest(l *Limiter) (restore func()) {
	defaultMu.Lock()
	prev := defaultLim
	defaultLim = l
	defaultMu.Unlock()
	return func() {
		defaultMu.Lock()
		defaultLim = prev
		defaultMu.Unlock()
	}
}

// RenderTimeout is the bounded per-render deadline this limiter enforces.
func (l *Limiter) RenderTimeout() time.Duration { return l.renderTimeout }

// renderFunc is the signature of a single chromedp render. The deadline is
// pre-applied to ctx by Do; the renderer should respect ctx.
type renderFunc func(ctx context.Context) ([]byte, error)

// Do acquires a render slot (waiting up to queueWait), then runs fn under a
// context bounded by renderTimeout. It returns one of:
//
//   - the rendered bytes + nil on success;
//   - ErrQueueSaturated if no slot freed within queueWait;
//   - ErrRenderDeadline if fn did not finish within renderTimeout because the
//     render context's deadline elapsed;
//   - the parent ctx's error if the CALLER's context was cancelled while
//     waiting or rendering (client disconnect / upstream deadline);
//   - whatever non-deadline error fn returned otherwise (e.g. the renderer's
//     ErrChromeUnavailable, which the handler also maps to 503).
//
// The deadline classification is by the RENDER context: if fn returns an error
// AND the render context is past its deadline while the parent is not, the
// result is ErrRenderDeadline regardless of how chromedp wrapped the
// underlying context.DeadlineExceeded. This is the deterministic mapping that
// makes a slow/contended chrome degrade to 503 instead of 500 (AC-1).
func (l *Limiter) Do(ctx context.Context, fn renderFunc) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Acquire a slot with a bounded wait. A non-positive queueWait means
	// "fail fast" — try once, then saturate.
	if l.queueWait <= 0 {
		select {
		case l.sem <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			return nil, ErrQueueSaturated
		}
	} else {
		waitCtx, cancelWait := context.WithTimeout(ctx, l.queueWait)
		select {
		case l.sem <- struct{}{}:
			cancelWait()
		case <-waitCtx.Done():
			cancelWait()
			// Distinguish "the caller went away" from "we waited out the
			// queue budget". Only the latter is a 503-able saturation.
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, ErrQueueSaturated
		}
	}
	defer func() { <-l.sem }()

	renderCtx, cancel := context.WithTimeout(ctx, l.renderTimeout)
	defer cancel()

	buf, err := fn(renderCtx)
	if err == nil {
		return buf, nil
	}

	// Classify a deadline. If the parent caller was cancelled, surface that
	// (client disconnect / upstream deadline) untouched. Otherwise, if the
	// bounded render context elapsed, this is the graceful render-deadline
	// path → ErrRenderDeadline (503), NOT a 500.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if renderCtx.Err() != nil {
		return nil, ErrRenderDeadline
	}
	return nil, err
}

func envRenderTimeoutValue() time.Duration {
	if v := os.Getenv(envRenderTimeout); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return DefaultRenderTimeout
}

func envMaxConcurrencyValue() int {
	if v := os.Getenv(envMaxConcurrency); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			return n
		}
	}
	return DefaultMaxConcurrency
}

func envQueueWaitValue() time.Duration {
	if v := os.Getenv(envQueueWait); v != "" {
		// A valid duration (including 0 / negative → fail-fast) is honored.
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return DefaultQueueWait
}
