// Package httpresp is the slice 369 shared helper for emitting JSON HTTP
// responses without duplicating the writeJSON/writeError pattern that was
// declared byte-for-byte identical across 50+ internal/api/* packages
// (slice 328 code-review finding H-1).
//
// # Relationship to internal/api/httperr
//
// Slice 367's internal/api/httperr is the precedent and the sibling. The
// two packages partition the response surface cleanly and do NOT overlap:
//
//   - httperr owns 5xx responses. It genericises the client-facing body to
//     {"error":"internal error","request_id":"<id>"} so err.Error() text
//     (table/column names, paths, SQLSTATE codes) never leaks (CWE-209),
//     and logs the full error server-side keyed by request_id.
//   - httpresp owns 2xx and 4xx responses. Success bodies and user-input
//     (4xx) error messages are caller-supplied and safe to surface; this
//     package is the thin JSON-encoding shim for them.
//
// A handler therefore uses httpresp.WriteJSON for success, httpresp.WriteError
// for user-input (4xx) failures, and httperr.WriteInternal for server (5xx)
// failures. Do not route a 5xx through this package — the generic-body /
// request_id / slog contract lives in httperr by design.
//
// # Parameter naming
//
// The status parameter is named status (matching net/http's vocabulary —
// http.StatusOK, http.StatusBadRequest, w.WriteHeader(status)). The legacy
// per-package copies were split: the majority used code, a minority used
// status. Slice 369 standardises on status; see the decisions log
// (docs/audit-log/369-httpresp-shared-helper-consolidation-decisions.md).
//
// # Wire shape (frozen by httpresp_test.go)
//
//	WriteJSON(w, status, body)  ->  Content-Type: application/json
//	                                status code = status
//	                                body = json.Encode(body) + "\n"
//	WriteError(w, status, msg)  ->  WriteJSON(w, status, {"error": msg})
//
// These match the retired per-package helpers exactly, so the migration is
// behavior-preserving at the HTTP wire (P0-369-1).
//
// Slice 369 — code-review audit H-1 (helper consolidation).
package httpresp

import (
	"encoding/json"
	"net/http"
)

// WriteJSON writes body as a JSON response with the given status code and a
// Content-Type of application/json. The encoding is json.Encoder output,
// which appends a trailing newline — preserved from the legacy per-package
// helper so the byte stream is unchanged.
func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// WriteError writes a JSON {"error": msg} response with the given status
// code. It is a thin shim over WriteJSON, matching the majority of the
// retired per-package writeError implementations.
//
// Use this for 4xx user-input errors only. For 5xx server errors, use
// internal/api/httperr.WriteInternal, which genericises the body and logs
// the underlying error server-side (CWE-209).
func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]string{"error": msg})
}

// CodeConcurrentFreeze is the stable `code` on a write refused because an
// audit period in the tenant froze while the write's transaction was open
// (ingest.ErrConcurrentFreeze, #3038). Nothing was written; the client
// retries (#3060).
const CodeConcurrentFreeze = "concurrent_freeze"

// WriteConcurrentFreeze writes a 409 {"error": msg, "code":
// "concurrent_freeze"}. 409, not 500: the request is sound and the server is
// healthy; a freeze that committed mid-write refused it, and a retry starts
// after that freeze.
func WriteConcurrentFreeze(w http.ResponseWriter, msg string) {
	WriteJSON(w, http.StatusConflict, map[string]string{
		"error": msg,
		"code":  CodeConcurrentFreeze,
	})
}

// CodeFeatureNotEnabled is the stable `code` on a refusal for a feature this
// deployment has switched off (an unset env gate, an unwired AI service). A
// client branches on the code, not on the wording (#2730).
const CodeFeatureNotEnabled = "feature_not_enabled"

// WriteFeatureNotEnabled writes a 409 {"error": msg, "code":
// "feature_not_enabled"}. A feature switched off on this deployment is a
// refusal about configuration, not a server failure, so it is a 4xx: the web
// client shows a 4xx `{error}` to the user and replaces every 5xx body with
// the status line (#2645). 409, not 404: the route exists and the request is
// well-formed; the deployment's state refuses it (#2730).
func WriteFeatureNotEnabled(w http.ResponseWriter, msg string) {
	WriteJSON(w, http.StatusConflict, map[string]string{
		"error": msg,
		"code":  CodeFeatureNotEnabled,
	})
}
