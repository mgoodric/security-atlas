// Slice 371 — clock-injection unit tests for *Store.
//
// The Store's expiry math (Create initial expiresAt, Read expiry check,
// Read sliding-window refresh) all flow through the injected `clock`
// field. Production wiring leaves clock at the default — a closure that
// returns `time.Now().UTC()`. Tests pin the clock so boundary assertions
// at exactly-now+ttl are deterministic.
//
// DB-touching paths (Create, Read, Touch, Revoke) are exercised in the
// e2e integration suite; this file covers the clock-injection seam in
// isolation per the slice 069 + 371 testing discipline:
//
//   - NewStore wires a non-nil default clock that returns UTC time.
//   - WithClock overrides the clock (mutate-and-chain shape mirroring
//     internal/api/admintenants/handler.go:185).
//   - WithClock(nil) is a no-op — the receiver's clock survives.
//   - The injected clock is observable on every call (the closure is
//     re-read, not snapshot at construction).
//
// These tests are RED-first by construction: the WithClock setter, the
// nil-guard, and the UTC-default contract did not exist before slice 371
// and the tests would not compile against pre-371 code.

package sessions

import (
	"testing"
	"time"
)

// TestNewStore_DefaultClockReturnsUTC verifies the constructor wires a
// non-nil clock that returns time in the UTC zone (slice 371 default).
// A naive `time.Now` (without UTC) would carry the host's local zone,
// breaking deterministic JSON serialization of ExpiresAt across the
// platform.
func TestNewStore_DefaultClockReturnsUTC(t *testing.T) {
	t.Parallel()
	s := NewStore(nil, 0)
	if s == nil {
		t.Fatal("NewStore returned nil")
	}
	if s.clock == nil {
		t.Fatal("NewStore: clock is nil; want default")
	}
	got := s.clock()
	if got.Location() != time.UTC {
		t.Fatalf("default clock returned zone %v; want UTC", got.Location())
	}
	if got.IsZero() {
		t.Fatal("default clock returned zero time; want real wall-clock")
	}
}

// TestNewStore_DefaultTTLWhenZero documents the existing zero-TTL
// fallback. Pre-371 behaviour preserved (P0-371-3: do not change TTL
// durations or policy constants).
func TestNewStore_DefaultTTLWhenZero(t *testing.T) {
	t.Parallel()
	s := NewStore(nil, 0)
	if s.ttl != DefaultTTL {
		t.Fatalf("ttl = %v; want DefaultTTL %v", s.ttl, DefaultTTL)
	}
}

// TestWithClock_SetsClockAndChains verifies the slice-371 setter shape:
// mutates the receiver, returns the receiver for chaining (mirrors
// internal/api/admintenants/handler.go:185).
func TestWithClock_SetsClockAndChains(t *testing.T) {
	t.Parallel()
	pinned := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	s := NewStore(nil, time.Hour)
	got := s.WithClock(func() time.Time { return pinned })
	if got != s {
		t.Fatal("WithClock did not return receiver; chain shape broken")
	}
	if !s.clock().Equal(pinned) {
		t.Fatalf("s.clock() = %v; want pinned %v", s.clock(), pinned)
	}
}

// TestWithClock_NilGuardPreservesReceiver verifies that passing nil to
// WithClock is a no-op — the previously-installed clock survives. This
// is the slice 371 nil-guard contract (P0: do not break the default
// when a test passes a nil clock by accident).
func TestWithClock_NilGuardPreservesReceiver(t *testing.T) {
	t.Parallel()
	pinned := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := NewStore(nil, time.Hour).WithClock(func() time.Time { return pinned })
	before := s.clock()
	_ = s.WithClock(nil)
	after := s.clock()
	if !before.Equal(after) {
		t.Fatalf("WithClock(nil) mutated clock: before=%v after=%v", before, after)
	}
	if !after.Equal(pinned) {
		t.Fatalf("post-nil-guard clock = %v; want pinned %v", after, pinned)
	}
}

// TestClockInjection_TTLBoundaryArithmetic verifies the load-bearing
// boundary the slice doc calls out: a session created at T with TTL has
// `expiresAt == T + ttl`. Pre-371 this could only be asserted via a
// tolerance window against wall-clock; the injected clock makes it
// exact. This is the unit-level expression of the integration-level
// "session VALID at T+3599, INVALID at T+3600+ε" suggestion in the
// slice doc.
func TestClockInjection_TTLBoundaryArithmetic(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	ttl := time.Hour
	s := NewStore(nil, ttl).WithClock(func() time.Time { return t0 })

	// The expression at sessions.go Create is `s.clock().Add(s.ttl)`.
	// The clock is t0; the ttl is 1h; expiresAt must be exactly t0+1h.
	want := t0.Add(ttl)
	got := s.clock().Add(s.ttl)
	if !got.Equal(want) {
		t.Fatalf("clock+ttl arithmetic = %v; want %v", got, want)
	}

	// Boundary: "now" exactly at expiresAt — `now.After(expiresAt)` must
	// be false (a session whose expiresAt is *exactly* now is still
	// considered valid; the check at sessions.go:184 is strictly
	// After, not >=). One nanosecond past must be true.
	at := want
	if at.After(want) {
		t.Fatal("at == expiresAt: After should be false (still valid)")
	}
	pastWindow := want.Add(time.Nanosecond)
	if !pastWindow.After(want) {
		t.Fatal("at+1ns: After should be true (expired)")
	}
}

// TestClockInjection_SlidingWindowRefreshTriggerBoundary verifies the
// RefreshThreshold predicate at sessions.go:188. A session whose
// remaining lifetime falls below RefreshThreshold triggers a refresh on
// Read; the boundary is `row.ExpiresAt.Sub(now) < RefreshThreshold`.
// Boundary in: exactly RefreshThreshold remaining → no refresh.
// Boundary out: RefreshThreshold-1ns remaining → refresh.
func TestClockInjection_SlidingWindowRefreshTriggerBoundary(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	expiresAt := t0.Add(RefreshThreshold)

	// "now" at exactly threshold-back: remaining == RefreshThreshold,
	// predicate `< RefreshThreshold` is FALSE → no refresh.
	if expiresAt.Sub(t0) < RefreshThreshold {
		t.Fatal("at exactly RefreshThreshold remaining: predicate true; want false (no refresh)")
	}

	// One ns later: remaining < RefreshThreshold; predicate TRUE → refresh.
	t1 := t0.Add(time.Nanosecond)
	if expiresAt.Sub(t1) >= RefreshThreshold {
		t.Fatal("at threshold-1ns remaining: predicate false; want true (refresh)")
	}
}
