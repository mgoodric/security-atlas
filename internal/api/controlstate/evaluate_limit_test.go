package controlstate

// #2479 — the per-principal evaluate limiter's configuration. The bucket
// itself is tested in internal/api/ratelimit; the 429 + Retry-After wiring in
// TestEvaluate_PerPrincipalRateLimit.

import (
	"testing"
	"time"
)

func TestEvaluateLimiter_Rate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct{ rate, want int }{
		{6, 6}, {2, 2}, {0, DefaultEvaluateRatePerMin}, {-3, DefaultEvaluateRatePerMin},
	} {
		l := newEvaluateLimiter(c.rate, func() time.Time { return now })
		for i := 0; i < c.want; i++ {
			if ok, _ := l.Allow("p"); !ok {
				t.Fatalf("rate %d: request %d refused; the whole minute's budget may arrive at once", c.rate, i)
			}
		}
		ok, wait := l.Allow("p")
		if want := time.Minute / time.Duration(c.want); ok || wait != want {
			t.Fatalf("rate %d: request %d = (%v, %v); want refused, %v", c.rate, c.want+1, ok, wait, want)
		}
	}
}
