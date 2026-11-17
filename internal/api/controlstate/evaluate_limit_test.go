package controlstate

// #2479 — the per-principal evaluate limiter, pure Go with a fake clock.

import (
	"testing"
	"time"
)

func TestPrincipalLimiter_BurstThenRefill(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	l := newPrincipalLimiter(6, func() time.Time { return now })

	for i := 0; i < 6; i++ {
		if wait := l.allow("p"); wait != 0 {
			t.Fatalf("request %d refused (wait %v); the whole minute's budget may arrive at once", i, wait)
		}
	}
	if wait := l.allow("p"); wait != 10*time.Second {
		t.Fatalf("7th request wait = %v; want 10s", wait)
	}
	// A refused request does not push the next slot further out.
	if wait := l.allow("p"); wait != 10*time.Second {
		t.Fatalf("8th request wait = %v; want 10s", wait)
	}
	now = now.Add(10 * time.Second)
	if wait := l.allow("p"); wait != 0 {
		t.Fatalf("after 10s wait = %v; want admitted", wait)
	}
	if wait := l.allow("p"); wait == 0 {
		t.Fatal("second request after 10s admitted; only one slot refilled")
	}
}

func TestPrincipalLimiter_PrincipalsAreIndependent(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	l := newPrincipalLimiter(1, func() time.Time { return now })
	if l.allow("a") != 0 || l.allow("b") != 0 {
		t.Fatal("first request per principal must be admitted")
	}
	if l.allow("a") == 0 {
		t.Fatal("a's second request admitted at 1/min")
	}
}

func TestPrincipalLimiter_PrunesIdleEntries(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	l := newPrincipalLimiter(6, func() time.Time { return now })
	for _, p := range []string{"a", "b", "c"} {
		l.allow(p)
	}
	now = now.Add(2 * time.Minute)
	l.allow("d")
	if len(l.tat) != 1 {
		t.Fatalf("entries after prune = %d; want 1 (only d)", len(l.tat))
	}
}

func TestPrincipalLimiter_NonPositiveRateUsesDefault(t *testing.T) {
	t.Parallel()
	for _, rate := range []int{0, -3} {
		l := newPrincipalLimiter(rate, nil)
		if want := time.Minute / DefaultEvaluateRatePerMin; l.interval != want {
			t.Fatalf("rate %d: interval = %v; want %v", rate, l.interval, want)
		}
	}
}

func TestRetryAfterSeconds(t *testing.T) {
	t.Parallel()
	cases := map[time.Duration]int{
		time.Millisecond:              1,
		time.Second:                   1,
		time.Second + time.Nanosecond: 2,
		10 * time.Second:              10,
	}
	for in, want := range cases {
		if got := retryAfterSeconds(in); got != want {
			t.Fatalf("retryAfterSeconds(%v) = %d; want %d", in, got, want)
		}
	}
}
