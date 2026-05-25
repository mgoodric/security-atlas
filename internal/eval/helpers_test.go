// Unit tests for the pure helpers in internal/eval:
//
//   - IsNotFound / IsBadScopePredicate (query.go) — the HTTP-status routing
//     classifiers. Every handler that surfaces an eval error MUST funnel
//     through these; a regression would silently turn a 404 into a 500.
//   - FreshnessMaxAge (state.go) — the exported canvas-§2.3 mapping that
//     sibling packages (slice 016 freshness-drift) reuse. Validating the
//     known classes here pins the contract.
//
// Branches deliberately left to integration:
//   - Engine/Replay/EvaluateAll — exercised by internal/eval/integration_test.go
//     against real Postgres + scope cells; the in-memory rego eval path is
//     covered there.
//   - consumer.go (NATS subscriber) — exercised by the
//     evidence-ingest-to-eval end-to-end integration test; reproducing that
//     stack in a unit test would re-implement NATS.
//
// Slice 279 — coverage lift target. Pre-lift merged %: ~52% (after the CI
// list-extension adds eval to the integration job). These tests close the
// pure-helper gap and the FreshnessMaxAge accessor — small surface, but
// 100% of the public classifier API.

package eval

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestIsNotFound_SentinelErrControlNotFound(t *testing.T) {
	t.Parallel()
	if !IsNotFound(ErrControlNotFound) {
		t.Fatal("IsNotFound(ErrControlNotFound) = false; want true")
	}
}

func TestIsNotFound_PgxNoRows(t *testing.T) {
	t.Parallel()
	if !IsNotFound(pgx.ErrNoRows) {
		t.Fatal("IsNotFound(pgx.ErrNoRows) = false; want true")
	}
}

func TestIsNotFound_WrappedSentinel(t *testing.T) {
	t.Parallel()
	wrapped := fmt.Errorf("eval: %w", ErrControlNotFound)
	if !IsNotFound(wrapped) {
		t.Fatal("IsNotFound on wrapped sentinel must follow errors.Is")
	}
}

func TestIsNotFound_UnrelatedError(t *testing.T) {
	t.Parallel()
	if IsNotFound(errors.New("network down")) {
		t.Fatal("unrelated error must NOT match")
	}
}

func TestIsNotFound_NilError(t *testing.T) {
	t.Parallel()
	if IsNotFound(nil) {
		t.Fatal("nil error must not be classified as not-found")
	}
}

func TestIsBadScopePredicate_SentinelErrBadScopePredicate(t *testing.T) {
	t.Parallel()
	if !IsBadScopePredicate(ErrBadScopePredicate) {
		t.Fatal("IsBadScopePredicate(ErrBadScopePredicate) = false; want true")
	}
}

func TestIsBadScopePredicate_WrappedSentinel(t *testing.T) {
	t.Parallel()
	wrapped := fmt.Errorf("eval: %w", ErrBadScopePredicate)
	if !IsBadScopePredicate(wrapped) {
		t.Fatal("wrapped sentinel must be classified")
	}
}

func TestIsBadScopePredicate_StringPrefixFallback(t *testing.T) {
	t.Parallel()
	// scope.Evaluate returns plain fmt.Errorf("scope: ...") values without
	// a sentinel; the classifier matches the "scope: " prefix as a
	// fallback so a malformed predicate never surfaces as a 500.
	if !IsBadScopePredicate(errors.New("scope: malformed predicate")) {
		t.Fatal("scope: prefix must be classified as bad-scope-predicate")
	}
}

func TestIsBadScopePredicate_UnrelatedError(t *testing.T) {
	t.Parallel()
	if IsBadScopePredicate(errors.New("network down")) {
		t.Fatal("unrelated error must NOT match")
	}
}

func TestIsBadScopePredicate_NilError(t *testing.T) {
	t.Parallel()
	if IsBadScopePredicate(nil) {
		t.Fatal("nil error must not be classified as bad-scope-predicate")
	}
}

func TestFreshnessMaxAge_KnownClasses(t *testing.T) {
	t.Parallel()
	// Each enrolled class in freshnessMaxAgeTable must return a finite
	// duration AND have `known=true`. The class list is the canvas-§2.3
	// model; a regression here would silently broaden or narrow the
	// freshness window.
	for _, class := range []string{"daily", "weekly", "monthly", "quarterly"} {
		class := class
		t.Run(class, func(t *testing.T) {
			t.Parallel()
			d, known := FreshnessMaxAge(class)
			if !known {
				t.Fatalf("FreshnessMaxAge(%q) known=false; want true (in-table class)", class)
			}
			if d <= 0 {
				t.Fatalf("FreshnessMaxAge(%q) duration=%s; want positive", class, d)
			}
		})
	}
}

func TestFreshnessMaxAge_OrderingDailyShorterThanQuarterly(t *testing.T) {
	t.Parallel()
	daily, _ := FreshnessMaxAge("daily")
	quarterly, _ := FreshnessMaxAge("quarterly")
	if daily >= quarterly {
		t.Fatalf("daily(%s) must be shorter than quarterly(%s)", daily, quarterly)
	}
}

func TestFreshnessMaxAge_UnknownClassFallsBackButReportsUnknown(t *testing.T) {
	t.Parallel()
	d, known := FreshnessMaxAge("hourly")
	if known {
		t.Fatal("FreshnessMaxAge(hourly) known=true; want false (not in table)")
	}
	if d <= 0 {
		t.Fatalf("FreshnessMaxAge(hourly) duration=%s; want a positive fallback", d)
	}
	// Fallback should equal the monthly default per the comment in state.go.
	monthly, _ := FreshnessMaxAge("monthly")
	if d != monthly {
		t.Fatalf("hourly fallback (%s) != monthly default (%s)", d, monthly)
	}
}

func TestFreshnessMaxAge_EmptyClassFallsBack(t *testing.T) {
	t.Parallel()
	d, known := FreshnessMaxAge("")
	if known {
		t.Fatal(`FreshnessMaxAge("") known=true; want false`)
	}
	if d <= 0 {
		t.Fatal("empty class fallback must be positive")
	}
}

func TestFreshnessMaxAge_ReturnsTimeDuration(t *testing.T) {
	t.Parallel()
	// Compile-time guard: the return type is time.Duration. A regression
	// to e.g. int64 days would surface here. We assert by calling a
	// time.Duration-only method (Hours()) — that fails to compile if the
	// signature drifts.
	d, _ := FreshnessMaxAge("daily")
	if d.Hours() <= 0 {
		t.Fatalf("daily duration has non-positive hours: %v", d)
	}
}

// ===== Scheduler / Notifier constructor smoke tests =====
//
// The constructors close over pgxpool.Pool + logger; we can exercise the
// nil-logger fallback path (which substitutes a discard logger) without a
// real DB. Run() / SweepOnce() / NATS handlers all require live infra and
// stay in integration.

func TestNewScheduler_NilLoggerFallsBackToDiscard(t *testing.T) {
	t.Parallel()
	factory := func() *Engine { return nil } // not called in this path
	s := NewScheduler(nil, factory, nil)
	if s == nil {
		t.Fatal("NewScheduler returned nil")
	}
	if s.logger == nil {
		t.Fatal("nil logger arg must be replaced with a discard logger")
	}
}

func TestNewEngineFactory_ReturnsClosure(t *testing.T) {
	t.Parallel()
	// The factory closes over the pool — we can verify it returns a
	// non-nil closure without ever invoking it (calling it would dial the
	// pool, which is nil here).
	f := NewEngineFactory(nil)
	if f == nil {
		t.Fatal("NewEngineFactory returned nil closure")
	}
}
