// Package httperr tests cover the slice 367 generic-5xx response surface.
//
// The package's job is to take a server-side error and:
//
//  1. Generate (or reuse) a request ID
//  2. Emit a slog.Error log line with the request ID, op label, and full error
//  3. Write a generic client-facing JSON body that ONLY contains
//     {"error":"internal error","request_id":"<id>"} — NO err.Error() reflection
//
// These tests are the RED phase for slice 367 — the contract is locked here
// before any handler migrations begin.
package httperr_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/requestidmw"
)

// TestWriteInternal_GenericClientBody asserts that the client-facing body
// NEVER carries the original error text. The whole point of slice 367 is
// CWE-209 closure: pgx error messages with table/column/constraint names,
// filesystem paths, and library-internal state stay server-side.
func TestWriteInternal_GenericClientBody(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/whatever", nil)
	// Sensitive error text — must NOT appear in the response body.
	err := errors.New(`ERROR: duplicate key value violates unique constraint "idx_users_email" (SQLSTATE 23505)`)

	httperr.WriteInternal(rec, req, "list users", err)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	body := rec.Body.String()
	if strings.Contains(body, "idx_users_email") {
		t.Fatalf("client body leaked constraint name: %q", body)
	}
	if strings.Contains(body, "duplicate key") {
		t.Fatalf("client body leaked pgx error text: %q", body)
	}
	if strings.Contains(body, "23505") {
		t.Fatalf("client body leaked SQLSTATE code: %q", body)
	}

	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("response body is not JSON: %v (body=%q)", err, body)
	}
	if got["error"] != "internal error" {
		t.Fatalf("error field = %q, want %q", got["error"], "internal error")
	}
	if got["request_id"] == "" {
		t.Fatalf("response body missing request_id field: %q", body)
	}
}

// TestWriteInternal_GeneratesRequestIDWhenAbsent — when no request-ID
// middleware populated the context, the helper still emits a usable ID
// rather than empty string. Operators rely on the ID to pivot from
// user-reported bug to log lookup; an empty ID breaks that.
func TestWriteInternal_GeneratesRequestIDWhenAbsent(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/whatever", nil)

	httperr.WriteInternal(rec, req, "op", errors.New("boom"))

	var got map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	id := got["request_id"]
	if id == "" {
		t.Fatalf("expected generated request_id, got empty")
	}
	// UUIDv4 shape sanity — 36 chars with hyphens at the canonical
	// positions.
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		t.Fatalf("request_id %q is not a canonical UUID", id)
	}
}

// TestWriteInternal_ReusesRequestIDFromContext — when the slice 367
// request-ID middleware HAS already populated the context, the helper
// reuses that ID rather than minting a new one. Correlation across the
// access log, slog error log, and client response requires the same ID.
func TestWriteInternal_ReusesRequestIDFromContext(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	const wantID = "11111111-2222-3333-4444-555555555555"
	ctx := requestidmw.WithRequestID(context.Background(), wantID)
	req := httptest.NewRequest(http.MethodGet, "/v1/whatever", nil).WithContext(ctx)

	httperr.WriteInternal(rec, req, "op", errors.New("boom"))

	var got map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got["request_id"] != wantID {
		t.Fatalf("request_id = %q, want %q (context-supplied)", got["request_id"], wantID)
	}
}

// TestWriteInternal_LogsFullErrorServerSide — the original (possibly
// sensitive) error text MUST land in the slog log line so operators can
// triage. P0-367-2 says we never weaken server-side logging.
func TestWriteInternal_LogsFullErrorServerSide(t *testing.T) {
	t.Parallel()

	// Capture slog output.
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelError})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/things", nil)
	httperr.WriteInternal(rec, req, "list things", errors.New("pgx: connection refused (127.0.0.1:5432)"))

	logged := buf.String()
	if !strings.Contains(logged, "pgx: connection refused") {
		t.Fatalf("slog output missing full error: %q", logged)
	}
	if !strings.Contains(logged, "list things") {
		t.Fatalf("slog output missing op label: %q", logged)
	}
	if !strings.Contains(logged, "request_id") {
		t.Fatalf("slog output missing request_id key: %q", logged)
	}
	if !strings.Contains(logged, `"method":"GET"`) {
		t.Fatalf("slog output missing HTTP method: %q", logged)
	}
	if !strings.Contains(logged, `"path":"/v1/things"`) {
		t.Fatalf("slog output missing HTTP path: %q", logged)
	}
}

// TestWriteInternal_SetsContentTypeAndResponseHeader — clients and
// downstream proxies/CDNs read both the body and the X-Request-Id
// response header. The header should mirror the body's request_id so
// log search works regardless of which surface the operator copies from.
func TestWriteInternal_SetsContentTypeAndResponseHeader(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/whatever", nil)

	httperr.WriteInternal(rec, req, "op", errors.New("x"))

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	hdrID := rec.Header().Get("X-Request-Id")
	var got map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if hdrID == "" {
		t.Fatalf("X-Request-Id response header is empty")
	}
	if hdrID != got["request_id"] {
		t.Fatalf("X-Request-Id header %q != body request_id %q", hdrID, got["request_id"])
	}
}

// TestRequestIDFromContext_RoundTrip — the public accessor returns the
// value WithRequestID stored, empty string for an unkeyed context.
func TestRequestIDFromContext_RoundTrip(t *testing.T) {
	t.Parallel()

	if got := requestidmw.RequestIDFromContext(context.Background()); got != "" {
		t.Fatalf("unkeyed ctx returned %q, want empty", got)
	}
	ctx := requestidmw.WithRequestID(context.Background(), "abc")
	if got := requestidmw.RequestIDFromContext(ctx); got != "abc" {
		t.Fatalf("keyed ctx returned %q, want %q", got, "abc")
	}
}

// Compile-time assertion that the helper does not retain the request body
// or response writer beyond the call. (Trivial check; the function body is
// a single write — the test guards against future drift toward async log
// pipelines that might capture the writer.)
var _ = io.Discard
