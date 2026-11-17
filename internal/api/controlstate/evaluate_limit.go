// evaluate_limit.go — the per-principal limit on POST
// /v1/controls/{id}/evaluate (#2479).
//
// Single-flight bounds the work per control to one evaluation at a time,
// but one caller can still walk every control they own, or re-run one as
// soon as it finishes. This limit caps how many evaluations one principal
// (credstore.PrincipalOf) starts per minute, on top of the global limiter.
//
// It is per process: each replica keeps its own buckets, so across N
// replicas a principal can start up to N times the limit. That is the
// accepted bound; a shared limiter is a separate piece of work.
//
// The algorithm is GCRA (the generic cell rate algorithm): one timestamp per
// principal, the "theoretical arrival time". A request is admitted when the
// TAT is at most `burst` intervals ahead of now, and pushes the TAT one
// interval further. Entries whose TAT has passed carry no state and are
// pruned, so the map holds only principals that are mid-burst.
package controlstate

import (
	"sync"
	"time"
)

// DefaultEvaluateRatePerMin is the default number of manual evaluations one
// principal may start per minute. ATLAS_EVALUATE_RATE_PER_MIN overrides it.
const DefaultEvaluateRatePerMin = 6

// pruneEvery bounds how often allow sweeps expired entries.
const pruneEvery = time.Minute

// principalLimiter admits up to ratePerMin requests per principal per
// minute, all of which may arrive at once.
type principalLimiter struct {
	mu        sync.Mutex
	interval  time.Duration // time one request "costs"
	burst     time.Duration // how far ahead of now a TAT may run
	tat       map[string]time.Time
	lastPrune time.Time
	now       func() time.Time
}

// newPrincipalLimiter builds the limiter. A non-positive rate falls back to
// DefaultEvaluateRatePerMin; a nil now uses the wall clock.
func newPrincipalLimiter(ratePerMin int, now func() time.Time) *principalLimiter {
	if ratePerMin <= 0 {
		ratePerMin = DefaultEvaluateRatePerMin
	}
	if now == nil {
		now = time.Now
	}
	interval := time.Minute / time.Duration(ratePerMin)
	return &principalLimiter{
		interval: interval,
		burst:    time.Duration(ratePerMin-1) * interval,
		tat:      map[string]time.Time{},
		now:      now,
	}
}

// allow admits one request for principal. It returns 0 when admitted, else
// how long the caller must wait before the next request would be.
func (l *principalLimiter) allow(principal string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Sub(l.lastPrune) >= pruneEvery {
		for k, t := range l.tat {
			if !t.After(now) {
				delete(l.tat, k)
			}
		}
		l.lastPrune = now
	}
	tat, ok := l.tat[principal]
	if !ok || tat.Before(now) {
		tat = now
	}
	if wait := tat.Sub(now) - l.burst; wait > 0 {
		return wait
	}
	l.tat[principal] = tat.Add(l.interval)
	return 0
}

// retryAfterSeconds renders a wait as a Retry-After value: whole seconds,
// rounded up, never below 1.
func retryAfterSeconds(wait time.Duration) int {
	s := int((wait + time.Second - 1) / time.Second)
	if s < 1 {
		return 1
	}
	return s
}
