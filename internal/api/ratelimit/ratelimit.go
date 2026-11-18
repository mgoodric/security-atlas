// Package ratelimit is the keyed token bucket the HTTP handlers share
// (#2497): evidence push (per credential), the OAuth token, revoke and
// introspect endpoints (per client_id and per source IP), the admin demo
// seed/teardown (per source IP) and manual control evaluation (per
// principal).
//
// A Limiter holds one bucket per key. Each bucket refills one token every
// interval up to burst tokens, and a new key starts full. The algorithm is
// GCRA (the generic cell rate algorithm), which admits exactly what that
// token bucket admits while storing a single timestamp per key: the
// "theoretical arrival time" (TAT). A request is admitted when the TAT is at
// most burst-1 intervals ahead of now, and pushes the TAT one interval
// further. A refused request changes nothing.
//
// Memory is bounded by idle-key eviction. A key whose TAT has passed has a
// full bucket, so it carries no state a fresh key would not and is dropped.
// The sweep runs when a new key arrives and the map has reached PruneFloor
// entries; the next threshold is twice the surviving count, so a map of live
// keys is not rescanned on every insert.
//
// Limits are per process. Each replica keeps its own buckets, so across N
// replicas a key can be admitted up to N times the configured limit. That is
// the accepted bound; a shared limiter is separate work.
//
// Keys are whatever the caller passes. IP keys come from ratekey.IP, which
// buckets IPv6 on the /64.
package ratelimit

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

// PruneFloor is the smallest map size that triggers an idle-key sweep.
const PruneFloor = 4096

// Limiter is a keyed token bucket. The zero value is not usable; build one
// with New, PerMinute or PerSecond.
type Limiter struct {
	mu        sync.Mutex
	interval  time.Duration // time one token takes to refill
	tolerance time.Duration // how far ahead of now a TAT may run: (burst-1) intervals
	tat       map[string]time.Time
	pruneAt   int
	now       func() time.Time
}

// New builds a limiter that refills one token per interval and holds up to
// burst tokens. burst may be fractional (a bucket that never reaches one
// token admits nothing). A nil now uses the wall clock. It panics on a
// non-positive interval or burst: callers apply their own defaults first.
func New(interval time.Duration, burst float64, now func() time.Time) *Limiter {
	if interval <= 0 || burst <= 0 {
		panic("ratelimit: interval and burst must be positive")
	}
	if now == nil {
		now = time.Now
	}
	return &Limiter{
		interval:  interval,
		tolerance: time.Duration((burst - 1) * float64(interval)),
		tat:       map[string]time.Time{},
		pruneAt:   PruneFloor,
		now:       now,
	}
}

// PerMinute builds a limiter admitting n requests per key per minute, all of
// which may arrive at once (burst n).
func PerMinute(n int, now func() time.Time) *Limiter {
	if n <= 0 {
		panic("ratelimit: PerMinute rate must be positive")
	}
	return New(time.Minute/time.Duration(n), float64(n), now)
}

// PerSecond builds a limiter refilling rate tokens per second up to burst.
func PerSecond(rate, burst float64, now func() time.Time) *Limiter {
	if rate <= 0 {
		panic("ratelimit: PerSecond rate must be positive")
	}
	return New(time.Duration(float64(time.Second)/rate), burst, now)
}

// Allow takes one token for key. When the bucket is empty it returns false
// and how long until the next token, and takes nothing.
func (l *Limiter) Allow(key string) (ok bool, wait time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	tat, seen := l.tat[key]
	if !seen {
		l.maybePrune(now)
	}
	if !seen || tat.Before(now) {
		tat = now
	}
	if wait := tat.Sub(now) - l.tolerance; wait > 0 {
		return false, wait
	}
	l.tat[key] = tat.Add(l.interval)
	return true, 0
}

// Len reports how many keys the limiter holds.
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.tat)
}

// maybePrune drops keys whose bucket has refilled. Caller holds l.mu.
func (l *Limiter) maybePrune(now time.Time) {
	if len(l.tat) < l.pruneAt {
		return
	}
	for k, t := range l.tat {
		if !t.After(now) {
			delete(l.tat, k)
		}
	}
	l.pruneAt = max(PruneFloor, 2*len(l.tat))
}

// RetryAfterSeconds renders a wait as a Retry-After value: whole seconds,
// rounded up, never below 1.
func RetryAfterSeconds(wait time.Duration) int {
	return max(1, int((wait+time.Second-1)/time.Second))
}

// SetRetryAfter sets the Retry-After header (RFC 9110 §10.2.3) to seconds.
func SetRetryAfter(w http.ResponseWriter, seconds int) {
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
}
