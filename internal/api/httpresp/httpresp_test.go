// Package httpresp tests lock the slice 369 shared 2xx/4xx JSON-response
// contract. The package consolidates the 78-instance writeJSON/writeError
// pattern that was duplicated byte-for-byte across internal/api/*; these
// tests pin the exact wire shape so the migration is provably behavior
// preserving (P0-369-1: no wire change).
//
// Sibling package internal/api/httperr owns the 5xx surface (generic error
// body + request_id + slog). httpresp owns success bodies and user-input
// (4xx) errors. The two do not overlap.
package httpresp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/httpresp"
)

// TestWriteJSON_ExactWireShape pins the three observable effects of the
// helper: Content-Type header, status code, and a newline-terminated JSON
// encoding of the body (json.Encoder.Encode appends a trailing '\n'). The
// pre-migration per-package writeJSON produced exactly this; the test fails
// loudly if the shared helper drifts.
func TestWriteJSON_ExactWireShape(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	body := map[string]any{"id": "abc", "count": 3}

	httpresp.WriteJSON(rec, http.StatusCreated, body)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	// json.NewEncoder(w).Encode appends a trailing newline — the legacy
	// per-package helper did the same, so the byte stream must match.
	want := "{\"count\":3,\"id\":\"abc\"}\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

// TestWriteJSON_StatusOK covers the success path with a slice body — the
// most common shape in list handlers.
func TestWriteJSON_StatusOK(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	httpresp.WriteJSON(rec, http.StatusOK, []string{"x", "y"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var got []string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if len(got) != 2 || got[0] != "x" || got[1] != "y" {
		t.Fatalf("body = %v, want [x y]", got)
	}
}

// TestWriteError_ExactWireShape pins the error body to the historical
// map[string]string{"error": msg} encoding. Every retired per-package
// writeError produced exactly this byte stream (verified across 45 call
// sites — 14 inline, 30 delegating to writeJSON, 1 via an errorBody struct
// that serializes identically).
func TestWriteError_ExactWireShape(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	httpresp.WriteError(rec, http.StatusBadRequest, "invalid id")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	want := "{\"error\":\"invalid id\"}\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

// TestWriteError_DelegatesToWriteJSON asserts WriteError is a thin shim
// over WriteJSON — the historical per-package writeError majority delegated
// to writeJSON, so the shapes must be interchangeable.
func TestWriteError_DelegatesToWriteJSON(t *testing.T) {
	t.Parallel()

	errRec := httptest.NewRecorder()
	httpresp.WriteError(errRec, http.StatusNotFound, "not found")

	jsonRec := httptest.NewRecorder()
	httpresp.WriteJSON(jsonRec, http.StatusNotFound, map[string]string{"error": "not found"})

	if errRec.Body.String() != jsonRec.Body.String() {
		t.Fatalf("WriteError body %q != WriteJSON-with-error-map body %q",
			errRec.Body.String(), jsonRec.Body.String())
	}
	if errRec.Code != jsonRec.Code {
		t.Fatalf("WriteError status %d != WriteJSON status %d", errRec.Code, jsonRec.Code)
	}
}

// TestWriteError_SpecialCharactersEscaped guards the JSON escaping path —
// a message containing quotes or angle brackets must be encoded, not raw,
// matching json.Encoder behavior the legacy helpers relied on.
func TestWriteError_SpecialCharactersEscaped(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	httpresp.WriteError(rec, http.StatusUnprocessableEntity, `bad "value" <x>`)

	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not valid JSON: %v (body=%q)", err, rec.Body.String())
	}
	if got["error"] != `bad "value" <x>` {
		t.Fatalf("error field = %q, want %q", got["error"], `bad "value" <x>`)
	}
}
