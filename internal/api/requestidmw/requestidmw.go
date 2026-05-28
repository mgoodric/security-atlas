// Package requestidmw provides a chi-compatible request-ID middleware and
// the context plumbing the slice 367 generic-5xx helper uses to correlate
// access logs, slog error lines, and client responses.
//
// Why a custom middleware (rather than chi/middleware.RequestID): we want
// to (a) reject malformed inbound headers (canonical UUID only — the
// chi default trusts whatever the client sends) and (b) expose the ID via
// a typed context key so handlers don't reach into chi-internal keys.
//
// Slice 367 — security audit M-2 (CWE-209).
package requestidmw

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

// HeaderName is the response (and accepted-inbound) header name. Mirrors
// the W3C draft + AWS / GCP / Azure load-balancer convention.
const HeaderName = "X-Request-Id"

// ctxKey is a typed key so callers cannot collide with arbitrary string
// keys upstream. The zero value is the canonical key — only one is needed.
type ctxKey struct{}

// WithRequestID returns a child context carrying id under the package's
// private key. Exported so tests and the httperr helper can populate the
// context in environments where the middleware isn't wired (the helper
// has to work even on a handler that runs without the middleware — e.g.
// integration tests that call handlers directly).
func WithRequestID(parent context.Context, id string) context.Context {
	return context.WithValue(parent, ctxKey{}, id)
}

// RequestIDFromContext returns the request ID set by Middleware (or
// WithRequestID), or empty string if none has been set. Callers that
// need to fall back should generate their own ID rather than relying
// on a sentinel value.
func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(ctxKey{}).(string)
	return v
}

// Middleware is the chi-compatible request-ID middleware. It runs before
// any handler that needs a request ID for slog correlation.
//
// Behaviour:
//
//   - If the inbound request carries an X-Request-Id header AND it parses
//     as a UUID, reuse it. This preserves IDs minted by upstream load
//     balancers / proxies that already inserted them.
//
//   - Otherwise, generate a fresh UUIDv4. Malformed inbound headers are
//     DISCARDED, not echoed — a hostile client cannot smuggle 4KB of
//     log content (or newline-injection sequences, or SQL-shaped strings)
//     into the audit trail via the header.
//
// The ID is set on both the request context (for downstream handlers
// and the httperr helper) and the response's X-Request-Id header (for
// the client and downstream log-aggregation tools).
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := sanitiseInbound(r.Header.Get(HeaderName))
		if id == "" {
			id = uuid.NewString()
		}
		w.Header().Set(HeaderName, id)
		next.ServeHTTP(w, r.WithContext(WithRequestID(r.Context(), id)))
	})
}

// sanitiseInbound returns the supplied id if it parses as a UUID, else
// empty string (meaning "generate a fresh one"). Centralising the check
// keeps the trust boundary clear and lets us tighten it later (e.g. to
// require UUIDv4 specifically) without touching the middleware body.
func sanitiseInbound(raw string) string {
	if raw == "" {
		return ""
	}
	// Length-cap the trust boundary: even uuid.Parse will reject input
	// > 38 bytes quickly, but the explicit cap makes the intent obvious
	// and bounds the parse-time for adversarial input.
	if len(raw) > 64 {
		return ""
	}
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return ""
	}
	// Re-emit the canonical string so we don't propagate quirky-but-valid
	// inputs ({}-wrapped, URN-prefixed) into log lines.
	return parsed.String()
}
