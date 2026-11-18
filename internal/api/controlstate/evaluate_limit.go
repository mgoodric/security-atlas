// evaluate_limit.go — the per-principal limit on POST
// /v1/controls/{id}/evaluate (#2479).
//
// Single-flight bounds the work per control to one evaluation at a time,
// but one caller can still walk every control they own, or re-run one as
// soon as it finishes. This limit caps how many evaluations one principal
// (credstore.PrincipalOf) starts per minute, on top of the global limiter.
// The whole minute's budget may arrive at once. The bucket is
// internal/api/ratelimit (#2497), which is per process: across N replicas a
// principal can start up to N times the limit.
package controlstate

import (
	"time"

	"github.com/mgoodric/security-atlas/internal/api/ratelimit"
)

// DefaultEvaluateRatePerMin is the default number of manual evaluations one
// principal may start per minute. ATLAS_EVALUATE_RATE_PER_MIN overrides it.
const DefaultEvaluateRatePerMin = 6

// newEvaluateLimiter builds the per-principal limiter. A non-positive rate
// falls back to DefaultEvaluateRatePerMin; a nil now uses the wall clock.
func newEvaluateLimiter(ratePerMin int, now func() time.Time) *ratelimit.Limiter {
	if ratePerMin <= 0 {
		ratePerMin = DefaultEvaluateRatePerMin
	}
	return ratelimit.PerMinute(ratePerMin, now)
}
