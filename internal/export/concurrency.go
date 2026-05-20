// Slice 145 — per-(tenant, user) export concurrency cap.
//
// Surfaced 2026-05-18 during retro-STRIDE on slice 135 (data-export
// library, merged). Slice 135 P0-A8 caps rows per export but does NOT
// cap concurrent exports per (tenant, user): a buggy client (or an
// authenticated attacker) firing N concurrent /export requests would
// saturate the per-tenant pgxpool — each export streams for minutes,
// degrading every other endpoint in that tenant. This package adds the
// missing mitigation as a tight, dependency-free semaphore.
//
// # Design
//
//   - One [Limiter] per process, accessed via [DefaultLimiter]. The cap
//     is read once from `ATLAS_EXPORT_MAX_CONCURRENT_PER_USER` at first
//     use (default 2 — see slice 145 D2). Tests construct fresh
//     limiters via [NewLimiter] for deterministic caps.
//
//   - One [chan struct{}] (buffered semaphore) per (tenant_id, user_id)
//     key, lazy-created on first [Limiter.Acquire]. Channels never
//     shrink — leaving them in place is cheaper than a per-key GC pass
//     and the key cardinality is bounded by the operator set per
//     tenant. Memory ceiling: ~50 bytes per active operator-tenant
//     pair (a channel header + map entry).
//
//   - [Limiter.Acquire] is non-blocking: if the semaphore is full it
//     returns [ErrCapExceeded] immediately. Callers MUST translate
//     this sentinel into the HTTP 429 + `Retry-After: 30` shape (slice
//     145 P0-HARDEN-3). This is intentionally NOT a queueing
//     limiter — queueing would convert the DoS into a latency tax on
//     every other request in the pool. The caller's retry MUST come
//     from outside this process.
//
//   - [Limiter.Acquire] returns a release function that callers MUST
//     `defer` immediately after the acquire returns nil. The release
//     is idempotent (calling it twice is a no-op, not a slot leak)
//     so the defer is safe even if the caller also has an explicit
//     release path (slice 145 P0-A9 — every acquired slot must
//     release on defer, including panic / error paths).
//
// # Cross-tenant isolation
//
// The key is (tenant_id, user_id) — NOT just user_id. A super_admin
// running concurrent exports across five tenants is NOT throttled by
// cap=2 in any single tenant. This is the slice 145 P0-HARDEN-2
// requirement: the cap mitigates the DoS surface at the granularity
// where the DoS lives (per-tenant pgxpool), not at the user-identity
// level.
//
// # Goroutine-leak posture
//
// Acquire either returns ErrCapExceeded (no slot taken, no release
// needed) or returns nil + a release fn. The release fn is closed over
// the channel and the once.Do guard; calling it sends one value back
// onto the buffered channel, freeing exactly one slot. The semaphore
// channel is never closed (we don't know when a key is "done" — see
// "never shrink" above). No goroutine is ever started inside this
// package; the limiter is a pure synchronous primitive over a chan.

package export

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"

	"github.com/google/uuid"
)

// DefaultMaxConcurrentPerUser is the slice 145 D2 default cap. Chosen
// so that two simultaneous forensic exports under the same operator
// are permitted (the realistic upper bound for one human running
// audit-handoff work) while a third request is refused. 2 is also
// already meaningful pgxpool pressure: a single export holds a
// connection for the duration of the streaming write, so 2 in-flight
// is already 20% of a default 10-connection pool.
//
// Operators who deliberately want more parallelism can raise the cap
// via the `ATLAS_EXPORT_MAX_CONCURRENT_PER_USER` env var. The cap is
// per-process (not per-key); raising it raises the limit for every
// (tenant, user) pair simultaneously.
const DefaultMaxConcurrentPerUser = 2

// envMaxConcurrentPerUser is the operator-tuning env var. Resolved
// once at first [DefaultLimiter] use; subsequent changes do NOT
// re-read the env (matching the rest of the platform's env-as-startup
// pattern).
const envMaxConcurrentPerUser = "ATLAS_EXPORT_MAX_CONCURRENT_PER_USER"

// ErrCapExceeded is the sentinel error [Limiter.Acquire] returns when
// the per-(tenant, user) cap is already in use. HTTP handlers translate
// this into a 429 with `Retry-After: 30` and a JSON body explaining
// the limit (slice 145 P0-HARDEN-3 + P0-A10). The sentinel is a
// constant value so callers compare with errors.Is.
var ErrCapExceeded = errors.New("export: per-(tenant, user) concurrent-export cap exceeded")

// Limiter is the per-(tenant, user) export concurrency semaphore. The
// zero value is NOT usable; construct via [NewLimiter] or use the
// process-wide singleton via [DefaultLimiter].
//
// Safe for concurrent use across goroutines. Acquires are non-blocking;
// see package docs for the DoS-mitigation rationale.
type Limiter struct {
	cap int

	mu    sync.Mutex
	slots map[limiterKey]chan struct{}
}

// limiterKey is the composite key used to bucket inflight exports.
// Defined as a value type so it can be a map key directly.
type limiterKey struct {
	TenantID uuid.UUID
	UserID   string
}

// NewLimiter constructs a Limiter with the given capacity. Capacity
// values <= 0 are clamped to 1 (a 0-capacity semaphore would deadlock
// every export). Used by tests; production code uses [DefaultLimiter].
func NewLimiter(capacity int) *Limiter {
	if capacity < 1 {
		capacity = 1
	}
	return &Limiter{
		cap:   capacity,
		slots: make(map[limiterKey]chan struct{}),
	}
}

// Acquire reserves one in-flight slot for the (tenant, user) key. On
// success it returns a release function the caller MUST defer
// immediately. On cap-exceeded it returns nil + [ErrCapExceeded] and
// the caller MUST NOT call any release.
//
// Acquire is non-blocking: a full semaphore returns the sentinel
// immediately rather than queueing. See package docs for the
// rationale.
//
// The release function is idempotent — calling it twice is a no-op
// (not a double-free) so callers may both defer the release and use
// an explicit release path inside the handler. The release is closed
// over a [sync.Once] guarding exactly one channel receive.
func (l *Limiter) Acquire(tenantID uuid.UUID, userID string) (release func(), err error) {
	key := limiterKey{TenantID: tenantID, UserID: userID}

	l.mu.Lock()
	ch, ok := l.slots[key]
	if !ok {
		ch = make(chan struct{}, l.cap)
		l.slots[key] = ch
	}
	l.mu.Unlock()

	select {
	case ch <- struct{}{}:
		// Slot acquired. Build the once-guarded release.
		var once sync.Once
		return func() {
			once.Do(func() {
				// Buffered receive — always succeeds because we
				// know we put exactly one value in for this slot.
				<-ch
			})
		}, nil
	default:
		// Cap exceeded — no slot taken, no release needed.
		return nil, fmt.Errorf("tenant=%s user=%s cap=%d: %w",
			tenantID, userID, l.cap, ErrCapExceeded)
	}
}

// Cap returns the limiter's per-key capacity. Used by tests + by the
// HTTP handler's 429 response body so the operator sees the
// configured value (not just a generic "limit exceeded").
func (l *Limiter) Cap() int {
	return l.cap
}

// ===== Process-wide default limiter =====
//
// The HTTP handler uses one Limiter for the whole process. Resolved
// once at first use via sync.Once so test code that injects its own
// limiter via NewLimiter is not affected by env-var read order.

var (
	defaultLimiterOnce sync.Once
	defaultLimiter     *Limiter
)

// DefaultLimiter returns the process-wide singleton Limiter. The cap
// is taken from the `ATLAS_EXPORT_MAX_CONCURRENT_PER_USER` env var at
// first call; subsequent calls return the cached instance.
//
// Invalid env values (non-integer, <= 0) fall back to
// [DefaultMaxConcurrentPerUser] silently — the export endpoint is not
// the right place to fail-fast on a typo'd operator env var (the
// startup banner is). The default-2 fallback preserves the
// "concurrency cap is on" invariant.
func DefaultLimiter() *Limiter {
	defaultLimiterOnce.Do(func() {
		cap := DefaultMaxConcurrentPerUser
		if raw := os.Getenv(envMaxConcurrentPerUser); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 {
				cap = n
			}
		}
		defaultLimiter = NewLimiter(cap)
	})
	return defaultLimiter
}
