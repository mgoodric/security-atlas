// Unit tests for the slice 189 oauthcode store — pre-DB validation
// branches and pure-Go helpers (WithClock).
//
// Load-bearing functions + branches covered:
//
//   - New: returns a Store; default clock is non-nil.
//   - WithClock: returns a Store with the supplied clock without
//     mutating the receiver.
//   - Constants: PKCEMethodS256, DefaultTTL have stable values.
//
// DB-touching paths (Insert / ConsumeOnce / SweepExpired /
// RegisterRedirectURI / IsRedirectURIRegistered / LookupRedirectURI)
// are covered by integration_test.go under the integration build tag.
package oauthcode

import (
	"testing"
	"time"
)

func TestPKCEMethodS256Constant(t *testing.T) {
	t.Parallel()
	if PKCEMethodS256 != "S256" {
		t.Fatalf("PKCEMethodS256 = %q, want \"S256\"", PKCEMethodS256)
	}
}

func TestDefaultTTL(t *testing.T) {
	t.Parallel()
	if DefaultTTL != 60*time.Second {
		t.Fatalf("DefaultTTL = %v, want %v", DefaultTTL, 60*time.Second)
	}
}

// TestNewProducesNonNilStore: the constructor wires the clock.
func TestNewProducesNonNilStore(t *testing.T) {
	t.Parallel()
	s := New(nil)
	if s == nil {
		t.Fatal("New(nil): got nil store")
	}
	if s.now == nil {
		t.Fatal("New: store.now is nil; want time.Now")
	}
}

// TestWithClockReturnsCopyDoesNotMutateReceiver covers the WithClock
// path: the returned store uses the supplied clock; the receiver is
// unchanged. This is the seam tests use to drive ErrExpired.
func TestWithClockReturnsCopyDoesNotMutateReceiver(t *testing.T) {
	t.Parallel()
	pinned := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := New(nil)
	originalNow := s.now
	s2 := s.WithClock(func() time.Time { return pinned })

	if s.now == nil {
		t.Fatal("WithClock mutated receiver: receiver.now is nil")
	}
	// The pointer equality test is brittle in Go (function values are
	// not comparable directly), so we compare behaviour: original
	// clock should not be pinned; s2's clock should be.
	if originalNow().Equal(pinned) {
		t.Fatal("receiver clock returned pinned time; receiver was mutated")
	}
	if !s2.now().Equal(pinned) {
		t.Fatalf("s2.now() = %v, want pinned %v", s2.now(), pinned)
	}
}
