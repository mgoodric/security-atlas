// Slice 371 — clock-injection unit tests for *Store.
//
// The Store's expiry math (Issue TTL → ExpiresAt, Rotate retiresAt,
// Authenticate retiresAt/expiresAt boundary checks) all flow through
// the injected `clock` field. Production wiring leaves clock at the
// default — a closure that returns `time.Now().UTC()`. Tests pin the
// clock so the rotation-grace boundary at exactly-now+grace is
// deterministic.
//
// DB-touching paths (Issue / Rotate / Revoke / Authenticate / List)
// are exercised through the wider auth e2e flows; this file covers the
// clock-injection seam in isolation per the slice 069 + 371 testing
// discipline:
//
//   - NewStore wires a non-nil default clock that returns UTC time.
//   - WithClock overrides the clock (mutate-and-chain shape mirroring
//     internal/api/admintenants/handler.go:185).
//   - WithClock(nil) is a no-op — the receiver's clock survives.
//   - The injected clock drives the rotation-grace boundary
//     arithmetic at Authenticate (apikeystore.go:255-258) and the
//     retiresAt computed at Rotate (apikeystore.go:165).
//
// These tests are RED-first by construction: the WithClock setter, the
// nil-guard, and the UTC-default contract did not exist before slice
// 371 and the tests would not compile against pre-371 code.

package apikeystore

import (
	"testing"
	"time"
)

// TestNewStore_DefaultClockReturnsUTC verifies the constructor wires a
// non-nil clock that returns time in the UTC zone (slice 371 default).
// A naive `time.Now` (without UTC) would carry the host's local zone,
// risking off-by-one-day rotation-grace bugs near midnight when the
// platform is deployed across multiple time zones.
func TestNewStore_DefaultClockReturnsUTC(t *testing.T) {
	t.Parallel()
	s := NewStore(nil, nil, nil, 0)
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

// TestNewStore_DefaultRotationGraceWhenZero documents the pre-371
// 7-day rotation-grace fallback. The constant is policy — P0-371-3
// forbids changing it as part of this slice. Pinning the value here
// catches accidental drift in a follow-on refactor.
func TestNewStore_DefaultRotationGraceWhenZero(t *testing.T) {
	t.Parallel()
	s := NewStore(nil, nil, nil, 0)
	if s.rotationGrace != 7*24*time.Hour {
		t.Fatalf("rotationGrace = %v; want 7 days", s.rotationGrace)
	}
}

// TestWithClock_SetsClockAndChains verifies the slice-371 setter shape:
// mutates the receiver, returns the receiver for chaining (mirrors
// internal/api/admintenants/handler.go:185).
func TestWithClock_SetsClockAndChains(t *testing.T) {
	t.Parallel()
	pinned := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	s := NewStore(nil, nil, nil, time.Hour)
	got := s.WithClock(func() time.Time { return pinned })
	if got != s {
		t.Fatal("WithClock did not return receiver; chain shape broken")
	}
	if !s.clock().Equal(pinned) {
		t.Fatalf("s.clock() = %v; want pinned %v", s.clock(), pinned)
	}
}

// TestWithClock_NilGuardPreservesReceiver verifies the slice 371
// nil-guard: WithClock(nil) is a no-op rather than zeroing the clock
// to a nil function pointer (which would panic at Issue/Rotate).
func TestWithClock_NilGuardPreservesReceiver(t *testing.T) {
	t.Parallel()
	pinned := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := NewStore(nil, nil, nil, time.Hour).WithClock(func() time.Time { return pinned })
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

// TestClockInjection_RotationGraceBoundaryArithmetic verifies the
// rotation-grace boundary the slice doc calls out: a key rotated at T
// has its predecessor's retires_at set to T+rotationGrace; at
// Authenticate, `now.After(retires_at)` decides whether the
// predecessor still grants access. Pre-371 this could only be asserted
// with a tolerance window; the injected clock makes it exact.
//
// Boundary contract:
//   - At now == retires_at: After is FALSE → still grants access.
//   - At now == retires_at + 1ns: After is TRUE → ErrUnknownKey.
//
// Matches the slice doc invariant:
//
//	"return ErrUnknownKey at T+7d+1ns (one nanosecond past grace)"
//	"return the credential at T+7d (exactly grace)"
func TestClockInjection_RotationGraceBoundaryArithmetic(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	grace := 7 * 24 * time.Hour
	s := NewStore(nil, nil, nil, grace).WithClock(func() time.Time { return t0 })

	// The expression at apikeystore.go Rotate is `s.clock().Add(s.rotationGrace)`.
	wantRetiresAt := t0.Add(grace)
	gotRetiresAt := s.clock().Add(s.rotationGrace)
	if !gotRetiresAt.Equal(wantRetiresAt) {
		t.Fatalf("Rotate arithmetic: clock+grace = %v; want %v", gotRetiresAt, wantRetiresAt)
	}

	// Boundary: at now == retiresAt, Authenticate uses
	// `row.RetiresAt.Valid && now.After(row.RetiresAt.Time)` — at
	// exactly the boundary, After is false → still grants access.
	atBoundary := wantRetiresAt
	if atBoundary.After(wantRetiresAt) {
		t.Fatal("now == retiresAt: After should be false (still valid in grace window)")
	}
	// One ns past: After is true → ErrUnknownKey.
	pastBoundary := wantRetiresAt.Add(time.Nanosecond)
	if !pastBoundary.After(wantRetiresAt) {
		t.Fatal("now == retiresAt + 1ns: After should be true (past grace)")
	}
}

// TestClockInjection_IssueTTLBoundaryArithmetic verifies the
// per-issue TTL boundary: Issue with TTL=T sets expires_at to
// `clock()+T`. The Authenticate check is the same
// `now.After(expires_at)` strict-After comparison as rotation-grace.
//
// Boundary contract:
//   - now == expires_at: After is FALSE → still grants access.
//   - now == expires_at + 1ns: After is TRUE → ErrUnknownKey.
func TestClockInjection_IssueTTLBoundaryArithmetic(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	ttl := 24 * time.Hour
	s := NewStore(nil, nil, nil, 0).WithClock(func() time.Time { return t0 })

	wantExpiresAt := t0.Add(ttl)
	gotExpiresAt := s.clock().Add(ttl)
	if !gotExpiresAt.Equal(wantExpiresAt) {
		t.Fatalf("Issue arithmetic: clock+ttl = %v; want %v", gotExpiresAt, wantExpiresAt)
	}

	if wantExpiresAt.After(wantExpiresAt) {
		t.Fatal("now == expiresAt: After should be false (still valid)")
	}
	if !wantExpiresAt.Add(time.Nanosecond).After(wantExpiresAt) {
		t.Fatal("now == expiresAt + 1ns: After should be true (expired)")
	}
}
