package sink

import (
	"context"
	"sync/atomic"

	"github.com/mgoodric/security-atlas/internal/audit/unifiedlog"
)

// defaultSink is the package-level singleton the per-domain audit-log
// INSERT sites call into. It is set ONCE by cmd/atlas/main.go at boot
// via SetDefault. Until set, EmitDefault is a no-op — production
// initialization order is preserved (main calls SetDefault before
// serving traffic) and tests get a no-op by default.
//
// The atomic.Pointer wrapper makes set/get race-free without a mutex on
// the hot path. The 9 INSERT call sites read .Load() once per Emit.
var defaultSink atomic.Pointer[Sink]

// SetDefault installs s as the singleton sink. Subsequent calls REPLACE
// the previous singleton (the caller is responsible for shutting down
// the prior one). Production: cmd/atlas calls this exactly once at boot.
//
// Passing nil clears the singleton. Useful for tests that explicitly
// want to assert no fan-out happened.
func SetDefault(s *Sink) {
	defaultSink.Store(s)
}

// Default returns the current singleton, or nil if unset. Most callers
// should use EmitDefault instead — it handles the nil-singleton case.
func Default() *Sink {
	return defaultSink.Load()
}

// EmitDefault calls Emit on the package-level singleton if one is set.
// When unset (no SetDefault yet, e.g., tests), this is a fast discard.
//
// This is the one-line affordance the 9 per-domain audit-log INSERT
// sites call AFTER a successful in-app INSERT. It is intentionally
// non-blocking + non-error-returning (P0-A2 / D8) so the call site can
// add the line at the end of its happy path without restructuring
// error handling.
func EmitDefault(ctx context.Context, entry unifiedlog.Entry) {
	s := defaultSink.Load()
	if s == nil {
		return
	}
	s.Emit(ctx, entry)
}
