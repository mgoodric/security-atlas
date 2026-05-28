// Package requestidmw tests cover the slice 367 request-ID middleware.
//
// The middleware:
//
//  1. Reads X-Request-Id from the inbound request; uses it verbatim ONLY
//     when it parses as a UUID (otherwise generate a fresh one — never
//     trust an unbounded client-supplied string in the audit trail).
//  2. Sets the ID on the response's X-Request-Id header so the round-trip
//     completes for the client.
//  3. Stashes the ID in the request context so downstream handlers and
//     the httperr.WriteInternal helper pick it up automatically.
package requestidmw_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/requestidmw"
)

// TestMiddleware_GeneratesIDWhenAbsent — no X-Request-Id header on the
// request → middleware generates a UUID and propagates it through context
// + response header.
func TestMiddleware_GeneratesIDWhenAbsent(t *testing.T) {
	t.Parallel()

	var captured string
	h := requestidmw.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = requestidmw.RequestIDFromContext(r.Context())
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/whatever", nil)
	h.ServeHTTP(rec, req)

	if captured == "" {
		t.Fatalf("middleware did not stash a request ID in context")
	}
	if len(captured) != 36 {
		t.Fatalf("generated ID %q is not 36 chars (canonical UUID)", captured)
	}
	if got := rec.Header().Get("X-Request-Id"); got != captured {
		t.Fatalf("response header %q != context ID %q", got, captured)
	}
}

// TestMiddleware_ReusesInboundUUID — when the inbound X-Request-Id header
// IS a UUID, the middleware uses it verbatim. This is the load-balancer
// or upstream-proxy case where the LB has already minted an ID.
func TestMiddleware_ReusesInboundUUID(t *testing.T) {
	t.Parallel()

	const inbound = "11111111-2222-3333-4444-555555555555"

	var captured string
	h := requestidmw.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = requestidmw.RequestIDFromContext(r.Context())
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/whatever", nil)
	req.Header.Set("X-Request-Id", inbound)
	h.ServeHTTP(rec, req)

	if captured != inbound {
		t.Fatalf("context ID = %q, want inbound %q", captured, inbound)
	}
	if got := rec.Header().Get("X-Request-Id"); got != inbound {
		t.Fatalf("response header = %q, want %q", got, inbound)
	}
}

// TestMiddleware_RejectsMalformedInbound — when the inbound header is
// NOT a UUID, the middleware ignores it and generates a fresh ID. This
// is the security gate: a hostile client cannot smuggle log-line content
// or 4KB of garbage into the audit trail via the header.
func TestMiddleware_RejectsMalformedInbound(t *testing.T) {
	t.Parallel()

	hostile := []string{
		"not-a-uuid",
		strings.Repeat("a", 4096),
		"' OR 1=1 --",
		"\n\nINJECTED LOG LINE",
		"../../etc/passwd",
		"", // explicit empty also covered by the absent-header test
	}

	for _, in := range hostile {
		in := in
		t.Run("rejects_"+truncForName(in), func(t *testing.T) {
			t.Parallel()

			var captured string
			h := requestidmw.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				captured = requestidmw.RequestIDFromContext(r.Context())
			}))

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
			req.Header.Set("X-Request-Id", in)
			h.ServeHTTP(rec, req)

			if captured == in {
				t.Fatalf("middleware accepted hostile header %q", in)
			}
			if len(captured) != 36 {
				t.Fatalf("generated ID %q is not a canonical UUID after rejecting hostile input", captured)
			}
		})
	}
}

func truncForName(s string) string {
	if len(s) > 16 {
		return s[:16]
	}
	if s == "" {
		return "empty"
	}
	return strings.ReplaceAll(s, "/", "_")
}
