// #1913 — throttling for POST /auth/local/login.
//
// Every login attempt runs a 64 MiB argon2id verify (#1907), so an
// unthrottled endpoint is both a password-guessing surface and a CPU and
// memory sink. Two token buckets run before any of that work:
//
//   - per client IP (clientIP, which honors TRUSTED_PROXY_CIDRS): caps the
//     argon2id cost one source can impose;
//   - per account, keyed on tenant + lowercased email as submitted: caps
//     guesses against one account however many sources they come from.
//
// The account key is built from the request, never from a lookup, so an
// unknown email is throttled exactly like a known one and a 429 says
// nothing about whether the account exists.
//
// Known limit (decisions log 1913): browser logins reach this endpoint
// through the web BFF, which does not forward the client IP yet (#2040),
// so they all share the BFF's per-IP bucket.
package auth

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	loginIPBurst          = 60
	loginIPPerMinute      = 60
	loginAccountBurst     = 10
	loginAccountPerMinute = 5

	// loginBucketPruneFloor is the bucket count at which a limiter first
	// sweeps out buckets that have refilled; the next sweep waits until
	// the map doubles, so the sweep is amortized O(1) per request.
	loginBucketPruneFloor = 4096
)

// loginLimiter is an in-memory token bucket keyed on a string. v1 is a
// single-process platform; the OAuth token endpoint's limiter makes the
// same trade-off.
type loginLimiter struct {
	mu        sync.Mutex
	burst     float64
	perSecond float64
	buckets   map[string]*loginBucket
	pruneAt   int
	now       func() time.Time
}

type loginBucket struct {
	tokens float64
	last   time.Time
}

func newLoginLimiter(burst, perMinute int, now func() time.Time) *loginLimiter {
	return &loginLimiter{
		burst:     float64(burst),
		perSecond: float64(perMinute) / 60,
		buckets:   make(map[string]*loginBucket),
		pruneAt:   loginBucketPruneFloor,
		now:       now,
	}
}

// Allow consumes one token for key. When none is left it returns false and
// the whole seconds until one is.
func (l *loginLimiter) Allow(key string) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.buckets[key]
	if !ok {
		l.maybePrune(now)
		b = &loginBucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens = l.refilled(b, now)
	b.last = now
	if b.tokens < 1 {
		// The epsilon keeps float error from rounding 1.0000001s up to 2.
		return false, max(1, int(math.Ceil((1-b.tokens)/l.perSecond-1e-9)))
	}
	b.tokens--
	return true, 0
}

func (l *loginLimiter) refilled(b *loginBucket, now time.Time) float64 {
	return math.Min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.perSecond)
}

// maybePrune drops buckets that have refilled to burst: they hold no
// state a fresh bucket would not.
func (l *loginLimiter) maybePrune(now time.Time) {
	if len(l.buckets) < l.pruneAt {
		return
	}
	for k, b := range l.buckets {
		if l.refilled(b, now) >= l.burst {
			delete(l.buckets, k)
		}
	}
	l.pruneAt = max(loginBucketPruneFloor, 2*len(l.buckets))
}

// loginThrottle pairs the per-IP and per-account limiters.
type loginThrottle struct {
	ip      *loginLimiter
	account *loginLimiter
}

func newLoginThrottle(now func() time.Time) *loginThrottle {
	return &loginThrottle{
		ip:      newLoginLimiter(loginIPBurst, loginIPPerMinute, now),
		account: newLoginLimiter(loginAccountBurst, loginAccountPerMinute, now),
	}
}

// loginAccountKey is the per-account bucket key. GetUserByEmail matches
// case-insensitively, so case variants of one email share a bucket.
func loginAccountKey(tenantID uuid.UUID, email string) string {
	return tenantID.String() + "\x00" + strings.ToLower(strings.TrimSpace(email))
}

// writeLoginThrottled answers 429 with Retry-After.
func writeLoginThrottled(w http.ResponseWriter, retryAfter int) {
	w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	writeAuthError(w, http.StatusTooManyRequests, "too many login attempts")
}
