// clientauth_limit.go throttles client authentication on
// `/oauth/revoke` and `/oauth/introspect` (#2041).
//
// Both endpoints are unauthenticated at the HTTP layer and run an
// argon2id client-secret verify for whatever client_id the caller
// presents, so without a limit they are a CPU-exhaustion and
// secret-guessing surface. `/oauth/token` already limits per client_id
// (P0-188-9); this applies the same tokenBucketLimiter here, plus a
// per-IP bucket so one source cannot spread its guesses across many
// client_ids.
//
// Both buckets are keyed on what the caller PRESENTS, checked before
// any DB lookup: an unknown client_id is throttled exactly like a
// registered one, so the limiter cannot be used to enumerate which
// client_ids exist. The per-IP bucket is checked first, so a throttled
// source cannot grow the per-client map with fresh client_ids.
package oauth

import (
	"net/http"
	"time"
)

const (
	// DefaultClientAuthRatePerMin is the default per-client_id limit on
	// /oauth/revoke and /oauth/introspect. It matches the token
	// endpoint's DefaultTokenRatePerMin.
	DefaultClientAuthRatePerMin = DefaultTokenRatePerMin

	// DefaultClientAuthIPRatePerMin is the default per-source-IP limit.
	// It sits above the per-client limit so one host serving a couple of
	// clients is not throttled before either client is.
	DefaultClientAuthIPRatePerMin = 2 * DefaultClientAuthRatePerMin

	// MaxClientIDBytes bounds a presented client_id. oauthclient issues
	// UUIDs (36 bytes); anything past this is refused with 401
	// invalid_client before it becomes a limiter key, so an attacker
	// cannot fill the per-client map with megabyte keys.
	MaxClientIDBytes = 256

	// MaxFormBytes caps the form body on /oauth/token, /oauth/revoke and
	// /oauth/introspect. The largest legitimate field is a JWT of a few
	// KB; net/http's own default cap is 10 MB.
	MaxFormBytes = 64 << 10
)

// clientAuthLimiter pairs a per-client_id and a per-IP token bucket.
type clientAuthLimiter struct {
	perClient *tokenBucketLimiter
	perIP     *tokenBucketLimiter
	clientIP  func(*http.Request) string
}

// newClientAuthLimiter builds the limiter. Non-positive rates fall
// back to the defaults; a nil clientIP falls back to the direct TCP
// peer (never a raw X-Forwarded-For).
func newClientAuthLimiter(clientRate, ipRate int, clientIP func(*http.Request) string, now func() time.Time) *clientAuthLimiter {
	if clientRate <= 0 {
		clientRate = DefaultClientAuthRatePerMin
	}
	if ipRate <= 0 {
		ipRate = DefaultClientAuthIPRatePerMin
	}
	if clientIP == nil {
		clientIP = peerAddr
	}
	if now == nil {
		now = time.Now
	}
	return &clientAuthLimiter{
		perClient: newTokenBucketLimiter(clientRate, now),
		perIP:     newTokenBucketLimiter(ipRate, now),
		clientIP:  clientIP,
	}
}

// allow reports whether a client-authenticated request may proceed to
// the secret verify. When it may not, it has already written the
// response (401 for an over-long client_id, else 429 with
// Retry-After) and the caller must return. Requests presenting no
// client_id (the self-revocation bearer path) never reach argon2 and
// are not counted.
func (l *clientAuthLimiter) allow(w http.ResponseWriter, r *http.Request) bool {
	clientID := presentedClientID(r)
	if clientID == "" {
		return true
	}
	if len(clientID) > MaxClientIDBytes {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client",
			"client authentication failed")
		return false
	}
	if !l.perIP.Allow(l.clientIP(r)) {
		writeRateLimited(w, l.perIP.WindowSeconds())
		return false
	}
	if !l.perClient.Allow(clientID) {
		writeRateLimited(w, l.perClient.WindowSeconds())
		return false
	}
	return true
}

// presentedClientID returns the client_id the request authenticates
// as, with the exact precedence authenticateInspector and the revoke
// authenticate use: a Basic header with both parts set, else the
// client_id form param. Any drift here would let a request reach
// Verify uncounted.
func presentedClientID(r *http.Request) string {
	if id, secret, ok := r.BasicAuth(); ok && id != "" && secret != "" {
		return id
	}
	return r.FormValue("client_id")
}

// writeRateLimited writes the 429 the token endpoint uses.
func writeRateLimited(w http.ResponseWriter, retryAfter int) {
	retryAfterSeconds(w, retryAfter)
	writeOAuthError(w, http.StatusTooManyRequests, "invalid_request",
		"rate limit exceeded; retry after the indicated window")
}

// peerAddr is the default client-IP resolver: the direct TCP peer.
// Deployments behind a proxy wire auth.ClientIP (TRUSTED_PROXY_CIDRS)
// instead; this package cannot import internal/api/auth (it imports
// us).
func peerAddr(r *http.Request) string {
	ip, _ := requestIP(r).(string)
	return ip
}
