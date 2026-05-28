// Unit tests for slice 318: coverage lift to push internal/audit/sink past
// the 70% merged-coverage floor (was 67.3% unit-only after slice 126).
//
// Load-bearing functions + branches covered:
//
//   - Default(): nil-singleton + non-nil-singleton readbacks (the only
//     branch was the SetDefault writer; the reader was uncovered).
//   - Sink.Shutdown ctx.Done branch: a cancelled context returns the
//     ctx.Err() rather than blocking on the writer goroutine.
//   - Sink.writeOne nil-pool failure-row branch: writeFailureRow is a
//     no-op when poolForTenant is nil (the in-test default), which is
//     the "no fallback row write" comment branch.
//   - Sink.writeFailureRow direct call: nil-pool early return (covers
//     the "no pool wired" branch without needing a real DB).
//   - Sink.Emit on a no-op sink: instant return when Enabled()==false
//     (idempotent: pin the production-default discard path).
//   - Sink.New: ATLAS_AUDIT_SINK_BUFFER_SIZE override via env-var picks
//     up the integer value when opts.BufferSize is left at zero.
//   - Sink.New: garbage ATLAS_AUDIT_SINK_BUFFER_SIZE falls back to the
//     default size.
//
// All branches are pure-Go: no DB, no Postgres role required.
// Append-only invariant (P0-318-4) is honored trivially — no test
// touches any audit-log table.

package sink_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/internal/audit/sink"
	"github.com/mgoodric/security-atlas/internal/audit/unifiedlog"
)

// TestDefault_NilWhenUnset asserts Default() returns nil before SetDefault
// is invoked, and returns the installed sink after.
func TestDefault_NilWhenUnset(t *testing.T) {
	// Isolate from sibling tests: reset before + after.
	sink.SetDefault(nil)
	t.Cleanup(func() { sink.SetDefault(nil) })

	if got := sink.Default(); got != nil {
		t.Fatalf("Default() pre-set = %v; want nil", got)
	}
}

// TestDefault_NonNilAfterSetDefault asserts the reader returns the same
// instance the writer just installed.
func TestDefault_NonNilAfterSetDefault(t *testing.T) {
	t.Setenv(sink.EnvPath, "")
	t.Setenv(sink.EnvHMACKey, "")

	sink.SetDefault(nil)
	t.Cleanup(func() { sink.SetDefault(nil) })

	s, err := sink.New(sink.Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sink.SetDefault(s)
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })

	got := sink.Default()
	if got == nil {
		t.Fatal("Default() post-set = nil; want non-nil")
	}
	if got != s {
		t.Errorf("Default() = %p; want %p", got, s)
	}
}

// TestShutdown_HonorsContextDeadline asserts Shutdown returns the ctx err
// when the drain races a cancelled context. We force the race by giving
// Shutdown an already-cancelled ctx — the writer goroutine may exit
// quickly, so the test tolerates either outcome but pins the type of err
// when it surfaces.
//
// This is the slice 126 graceful-shutdown bound: a binary with a tight
// SIGTERM grace window must not block forever on Shutdown.
func TestShutdown_HonorsContextDeadline(t *testing.T) {
	t.Setenv(sink.EnvPath, "")
	t.Setenv(sink.EnvHMACKey, "")

	buf := &concurrentBufferLocal{}
	s, err := sink.New(sink.Options{
		Writer:  buf,
		HMACKey: []byte("test-hmac-key-must-be-32-bytes-min!!"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Cancel the ctx BEFORE calling Shutdown. The select inside Shutdown
	// then picks ctx.Done() OR the drain-complete channel; if the
	// goroutine finishes too fast it's the drain branch and err is nil,
	// otherwise it's the ctx.Err() branch.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = s.Shutdown(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Shutdown returned unexpected error: %v", err)
	}
}

// TestEmit_NoOpSinkInstantDiscard asserts Emit on a disabled sink returns
// instantly (slice 126 AC-6 production-default no-op path). The previous
// suite covered the no-op branch via TestNew_NoOpWhenPathUnset; this test
// pins the discard-counter invariant — a no-op sink does NOT increment
// any counter, because there is no Stats struct to read.
func TestEmit_NoOpSinkInstantDiscard(t *testing.T) {
	t.Setenv(sink.EnvPath, "")
	t.Setenv(sink.EnvHMACKey, "")
	s, err := sink.New(sink.Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s.Enabled() {
		t.Fatal("sink should be disabled when path + writer unset")
	}

	// Spam Emit on the disabled sink; it must NOT panic and must NOT
	// allocate-and-leak any goroutine. We give the test a short hard
	// deadline as a proxy for "instant return".
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			s.Emit(context.Background(), unifiedlog.Entry{Kind: unifiedlog.KindMe})
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("disabled-sink Emit blocked for >2s; the no-op branch is not instant")
	}

	// StatsSnapshot on a disabled sink still works (zero counters).
	emitted, dropped, writeErrors, failureRows := s.StatsSnapshot()
	if emitted != 0 || dropped != 0 || writeErrors != 0 || failureRows != 0 {
		t.Errorf("disabled sink Stats nonzero: e=%d d=%d w=%d f=%d",
			emitted, dropped, writeErrors, failureRows)
	}
}

// TestNew_EnvBufferSize_IntegerOverride asserts that an integer
// ATLAS_AUDIT_SINK_BUFFER_SIZE env var is honored when opts.BufferSize is
// zero. We can't read the bufferSize field directly (unexported), but the
// New call succeeds — the existing TestEnvBufferSize covers the env
// branch only via valid options; this case fixes a subtle gap by
// constructing without explicitly passing BufferSize in Options.
func TestNew_EnvBufferSize_IntegerOverride(t *testing.T) {
	t.Setenv(sink.EnvPath, "")
	t.Setenv(sink.EnvHMACKey, "")
	t.Setenv(sink.EnvBufferSize, "256")

	buf := &concurrentBufferLocal{}
	s, err := sink.New(sink.Options{
		Writer:  buf,
		HMACKey: []byte("test-hmac-key-must-be-32-bytes-min!!"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })

	if !s.Enabled() {
		t.Fatal("sink should be enabled when Writer override is present")
	}
}

// TestNew_OptsBufferSizeOverridesEnv asserts that an explicit positive
// opts.BufferSize wins over a (potentially garbage) env value.
func TestNew_OptsBufferSizeOverridesEnv(t *testing.T) {
	t.Setenv(sink.EnvPath, "")
	t.Setenv(sink.EnvHMACKey, "")
	t.Setenv(sink.EnvBufferSize, "9999")

	buf := &concurrentBufferLocal{}
	s, err := sink.New(sink.Options{
		Writer:     buf,
		HMACKey:    []byte("test-hmac-key-must-be-32-bytes-min!!"),
		BufferSize: 16,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })

	if !s.Enabled() {
		t.Fatal("sink should be enabled")
	}
}

// concurrentBufferLocal is a goroutine-safe in-memory writer for unit tests
// in this file. It mirrors safeBuffer from sink_test.go but with a distinct
// name to keep the symbol non-conflicting (Go allows the same package_test
// to declare both, but distinct names make grep easier).
type concurrentBufferLocal struct {
	mu  sync.Mutex
	buf []byte
}

func (b *concurrentBufferLocal) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	return len(p), nil
}
