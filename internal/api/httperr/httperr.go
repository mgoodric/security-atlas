// Package httperr is the slice 367 shared helper for emitting generic
// 5xx HTTP responses without leaking err.Error() text to the client.
//
// The package replaces the in-package writeJSON/writeError patterns that
// were previously reflecting raw error text into the JSON body. Those
// patterns are CWE-209 hazards: pgx errors carry table/column/constraint
// names, filesystem errors carry paths, library-internal errors carry
// version hints — all useful for an attacker mapping the deployment.
//
// Usage from a handler:
//
//	rows, err := h.store.List(ctx)
//	if err != nil {
//	    httperr.WriteInternal(w, r, "list rows", err)
//	    return
//	}
//
// What the helper does:
//
//  1. Resolves a request ID — either from the context (set by
//     requestidmw.Middleware) or by minting a fresh UUIDv4. The client
//     always sees a non-empty ID so operators can pivot from a bug
//     report to the slog log line.
//  2. Emits a slog.Error log line with request_id, op, http method/path,
//     and the full err.Error() — P0-367-2 requires we never weaken
//     server-side logging.
//  3. Writes the response with Content-Type application/json, status 500,
//     X-Request-Id header, and a JSON body of exactly:
//     {"error":"internal error","request_id":"<id>"}.
//
// The helper writes status 500 by design. Handlers needing a different
// 5xx code (e.g. 502 Bad Gateway when a downstream connector failed)
// should call WriteStatus instead.
//
// Slice 367 — security audit M-2 (CWE-209).
package httperr

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/requestidmw"
)

// genericInternalMessage is the single client-facing string for every
// 5xx returned through this helper. Concrete error detail goes to the
// slog log keyed by request_id.
const genericInternalMessage = "internal error"

// WriteInternal emits a generic 500 response and logs the full error
// server-side. See package doc for invariants.
func WriteInternal(w http.ResponseWriter, r *http.Request, op string, err error) {
	WriteStatus(w, r, http.StatusInternalServerError, op, err)
}

// WriteStatus is identical to WriteInternal but lets the caller pick a
// non-500 5xx status (e.g. 502 Bad Gateway for downstream-fetch failures
// in the adminsso discovery flow). The client-facing body is the same
// generic shape regardless of status code.
//
// Callers MUST NOT pass a non-5xx status here — the helper exists to
// genericise 5xx responses and would mis-label the client experience
// if used for 4xx. (Enforced at runtime: a non-5xx status falls through
// to 500 with a slog.Warn so the bug surfaces in logs.)
func WriteStatus(w http.ResponseWriter, r *http.Request, status int, op string, err error) {
	if status < 500 || status > 599 {
		slog.Warn("httperr.WriteStatus called with non-5xx status; coercing to 500",
			slog.Int("requested_status", status),
			slog.String("op", op),
		)
		status = http.StatusInternalServerError
	}

	id := requestidmw.RequestIDFromContext(r.Context())
	if id == "" {
		// Fall back to a freshly-minted UUID when the middleware isn't
		// in the chain (e.g. integration tests calling handlers
		// directly). The client always sees a non-empty ID.
		id = uuid.NewString()
	}

	// Server-side log carries the full error text — this is the trace
	// operators search when a user reports the request ID.
	method := ""
	path := ""
	if r != nil {
		method = r.Method
		if r.URL != nil {
			path = r.URL.Path
		}
	}
	errText := ""
	if err != nil {
		errText = err.Error()
	}
	slog.Error("internal handler error",
		slog.String("request_id", id),
		slog.String("op", op),
		slog.String("method", method),
		slog.String("path", path),
		slog.Int("status", status),
		slog.String("error", errText),
	)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set(requestidmw.HeaderName, id)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":      genericInternalMessage,
		"request_id": id,
	})
}
