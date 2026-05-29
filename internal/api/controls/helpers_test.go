// Slice 291 — unit tests for the pure-Go helpers + remaining
// non-DB-touching branches in the controls handlers. Companion to
// attest_test.go / handlers_test.go / list_test.go / export_test.go
// / history_export_test.go (which already pin the 4xx auth + parse
// branches). This file covers:
//
//   Load-bearing functions targeted:
//     - isManualImplementation (the implementation_type gate that
//       both AttestForm and Submit consult before the DB lookup
//       proceeds — accepts only manual_attested + manual_periodic)
//     - decodeJSONBObject (the JSONB→map[string]any helper used to
//       lift manual_evidence_schema and analogous columns; both the
//       valid + nil/empty branches and the malformed-JSON branch)
//     - deriveIdempotencyKey (the sha256(userID‖0‖controlID‖0‖body)
//       deterministic key; pins prefix + length + determinism + the
//       per-input-change-yields-fresh-key invariant the docstring
//       asserts)
//     - ingestErrorToStatus (the seven-arm switch mapping ingest
//       error sentinels to HTTP status; pins all seven sentinels +
//       the catch-all 500 branch + the wrapped-sentinel path)
//     - writeAttestJSON / writeAttestError / writeError / writeJSON
//       header + status + envelope contract — every 4xx handler
//       path lands on one of these
//     - writeControlLookupError (the pgx.ErrNoRows → 404 vs
//       generic-error → 500 branch in the attest handlers)
//     - controlsHasProgramRead (the program-read role gate; admin,
//       approver, owner-role, and the no-signal reject case)
//     - controlsCountingWriter (the byte-counting wrapper used for
//       the meta-audit byte_count field; single-write + multi-write
//       totals + pass-through return values)
//     - exportLimiter accessor (returns process-wide default when
//       no limiter is configured)
//
// Branches NOT covered here (covered by integration_test.go against
// real Postgres + RLS): the happy paths through AttestForm / Submit
// / List / ExportControls / ExportControlsHistory that round-trip
// the DB. Those are picked up when this slice enrolls
// ./internal/api/controls/... in the CI integration job's
// -coverpkg list.
//
// No vendor token prefixes in test fixtures — neutral test-* tokens
// only, matching the existing handlers_test.go posture.

package controls

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
)

// ---------------------------------------------------------------------
// isManualImplementation
// ---------------------------------------------------------------------

func TestIsManualImplementation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want bool
	}{
		{"manual_attested", true},
		{"manual_periodic", true},
		{"automated", false},
		{"semi_automated", false},
		{"", false},
		{"MANUAL_ATTESTED", false}, // case-sensitive
		{"manual_attested ", false},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := isManualImplementation(tc.in); got != tc.want {
				t.Fatalf("isManualImplementation(%q) = %v; want %v", tc.in, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------
// decodeJSONBObject
// ---------------------------------------------------------------------

func TestDecodeJSONBObject_NilEmpty(t *testing.T) {
	t.Parallel()
	for _, in := range [][]byte{nil, {}} {
		got, err := decodeJSONBObject(in)
		if err != nil {
			t.Fatalf("decodeJSONBObject(%v) returned error: %v", in, err)
		}
		if got != nil {
			t.Fatalf("decodeJSONBObject(%v) = %v; want nil so callers can short-circuit", in, got)
		}
	}
}

func TestDecodeJSONBObject_Valid(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"type":"object","required":["evidence"]}`)
	got, err := decodeJSONBObject(raw)
	if err != nil {
		t.Fatalf("decodeJSONBObject: %v", err)
	}
	if got["type"] != "object" {
		t.Fatalf("type = %v; want object", got["type"])
	}
	req, ok := got["required"].([]any)
	if !ok || len(req) != 1 || req[0] != "evidence" {
		t.Fatalf("required = %v; want [evidence]", got["required"])
	}
}

func TestDecodeJSONBObject_Malformed(t *testing.T) {
	t.Parallel()
	got, err := decodeJSONBObject([]byte(`{not json`))
	if err == nil {
		t.Fatalf("decodeJSONBObject malformed should error; got %v", got)
	}
}

// ---------------------------------------------------------------------
// deriveIdempotencyKey
// ---------------------------------------------------------------------

func TestDeriveIdempotencyKey_Deterministic(t *testing.T) {
	t.Parallel()
	body := []byte(`{"statement":"hello"}`)
	a := deriveIdempotencyKey("user-1", "ctrl-1", body)
	b := deriveIdempotencyKey("user-1", "ctrl-1", body)
	if a != b {
		t.Fatalf("same inputs must produce same key: a=%s b=%s", a, b)
	}
}

func TestDeriveIdempotencyKey_PrefixAndLength(t *testing.T) {
	t.Parallel()
	got := deriveIdempotencyKey("u", "c", []byte("body"))
	if !strings.HasPrefix(got, "attest-") {
		t.Fatalf("key must have attest- prefix; got %q", got)
	}
	suffix := strings.TrimPrefix(got, "attest-")
	if len(suffix) != 32 {
		t.Fatalf("hex suffix must be 32 chars; got %d (%q)", len(suffix), suffix)
	}
	if _, err := hex.DecodeString(suffix); err != nil {
		t.Fatalf("suffix must be hex; got %q (%v)", suffix, err)
	}
}

func TestDeriveIdempotencyKey_DifferentInputsDifferentKeys(t *testing.T) {
	t.Parallel()
	base := deriveIdempotencyKey("u1", "c1", []byte("body"))

	cases := map[string]string{
		"different user":    deriveIdempotencyKey("u2", "c1", []byte("body")),
		"different control": deriveIdempotencyKey("u1", "c2", []byte("body")),
		"different body":    deriveIdempotencyKey("u1", "c1", []byte("body2")),
	}
	for name, k := range cases {
		if k == base {
			t.Fatalf("%s: key collided with base %q", name, k)
		}
	}
}

func TestDeriveIdempotencyKey_ZeroByteSeparator(t *testing.T) {
	t.Parallel()
	// The sha256 separator is a single 0-byte so "u1" + "c1" + "body"
	// must NOT collide with "u" + "1c1" + "body". This pins the
	// separator invariant the docstring relies on.
	a := deriveIdempotencyKey("u1", "c1", []byte("body"))
	b := deriveIdempotencyKey("u", "1c1", []byte("body"))
	if a == b {
		t.Fatalf("zero-byte separator failed: u1/c1 and u/1c1 collided (%q)", a)
	}
}

// ---------------------------------------------------------------------
// ingestErrorToStatus
// ---------------------------------------------------------------------

func TestIngestErrorToStatus_AllSentinels(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"MissingField", ingest.ErrMissingField, http.StatusBadRequest},
		{"UnknownKind", ingest.ErrUnknownKind, http.StatusPreconditionFailed},
		{"Validation", ingest.ErrValidation, http.StatusBadRequest},
		{"IdempotencyMismatch", ingest.ErrIdempotencyMismatch, http.StatusConflict},
		{"ScopeViolation", ingest.ErrScopeViolation, http.StatusForbidden},
		{"ObservedAtSkew", ingest.ErrObservedAtSkew, http.StatusBadRequest},
		{"Oversized", ingest.ErrOversized, http.StatusRequestEntityTooLarge},
		{"Generic", errors.New("anything else"), http.StatusInternalServerError},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ingestErrorToStatus(tc.err); got != tc.want {
				t.Fatalf("ingestErrorToStatus(%q) = %d; want %d", tc.err, got, tc.want)
			}
		})
	}
}

func TestIngestErrorToStatus_WrappedSentinel(t *testing.T) {
	t.Parallel()
	// The handler uses errors.Is under the hood so a sentinel wrapped
	// via errors.Join must still map to the sentinel's bucket.
	wrapped := errors.Join(errors.New("outer"), ingest.ErrValidation)
	if got := ingestErrorToStatus(wrapped); got != http.StatusBadRequest {
		t.Fatalf("wrapped ErrValidation should map to 400; got %d", got)
	}
}

// ---------------------------------------------------------------------
// writeAttestJSON / writeAttestError
// ---------------------------------------------------------------------

func TestWriteAttestJSON_ContractContentTypeAndStatus(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	writeAttestJSON(rr, http.StatusCreated, map[string]any{"ok": true})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d; want 201", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q; want application/json", ct)
	}
	var got map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got["ok"] != true {
		t.Fatalf("body = %v; want {ok: true}", got)
	}
}

func TestWriteAttestError_ShapeEnvelope(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	writeAttestError(rr, http.StatusForbidden, "no role")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d; want 403", rr.Code)
	}
	var got attestErrorBody
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Error != "no role" {
		t.Fatalf("error = %q; want %q", got.Error, "no role")
	}
}

// ---------------------------------------------------------------------
// writeControlLookupError
// ---------------------------------------------------------------------

func TestWriteControlLookupError_NoRowsMapsTo404(t *testing.T) {
	t.Parallel()
	h := NewAttestHandler(nil, nil, nil)
	rr := httptest.NewRecorder()
	h.writeControlLookupError(rr, pgx.ErrNoRows)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "control not found") {
		t.Fatalf("body must say control not found; got %s", rr.Body.String())
	}
}

func TestWriteControlLookupError_OtherErrorMapsTo500(t *testing.T) {
	t.Parallel()
	h := NewAttestHandler(nil, nil, nil)
	rr := httptest.NewRecorder()
	h.writeControlLookupError(rr, errors.New("conn refused"))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "conn refused") {
		t.Fatalf("body must contain underlying error message; got %s", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "load control") {
		t.Fatalf("body must be prefixed with `load control`; got %s", rr.Body.String())
	}
}

// ---------------------------------------------------------------------
// writeJSON / writeError (handlers.go envelope)
// ---------------------------------------------------------------------

func TestWriteJSON_ContractContentTypeAndStatus(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	httpresp.WriteJSON(rr, http.StatusOK, map[string]any{"x": 1})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q; want application/json", ct)
	}
	if !strings.Contains(rr.Body.String(), `"x":1`) {
		t.Fatalf("body missing field: %s", rr.Body.String())
	}
}

func TestWriteError_ShapeEnvelope(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	httpresp.WriteError(rr, http.StatusBadRequest, "boom")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", rr.Code)
	}
	var got map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["error"] != "boom" {
		t.Fatalf("body = %v; want {error: boom}", got)
	}
}

// ---------------------------------------------------------------------
// controlsHasProgramRead
// ---------------------------------------------------------------------

func TestControlsHasProgramRead(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		c    credstore.Credential
		want bool
	}{
		{"admin", credstore.Credential{IsAdmin: true}, true},
		{"approver", credstore.Credential{IsApprover: true}, true},
		{"owner roles", credstore.Credential{OwnerRoles: []string{"control_owner"}}, true},
		{"empty owner-roles slice rejected", credstore.Credential{OwnerRoles: []string{}}, false},
		{"plain credential", credstore.Credential{}, false},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := controlsHasProgramRead(tc.c); got != tc.want {
				t.Fatalf("controlsHasProgramRead = %v; want %v", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------
// controlsCountingWriter
// ---------------------------------------------------------------------

func TestControlsCountingWriter_TallyBytes(t *testing.T) {
	t.Parallel()
	var sink bytes.Buffer
	cw := &controlsCountingWriter{w: &sink}
	if n, err := cw.Write([]byte("hello")); err != nil || n != 5 {
		t.Fatalf("Write 1: n=%d err=%v", n, err)
	}
	if n, err := cw.Write([]byte(" world")); err != nil || n != 6 {
		t.Fatalf("Write 2: n=%d err=%v", n, err)
	}
	if cw.n != 11 {
		t.Fatalf("counter = %d; want 11", cw.n)
	}
	if sink.String() != "hello world" {
		t.Fatalf("sink = %q; want %q", sink.String(), "hello world")
	}
}

func TestControlsCountingWriter_PassThroughZero(t *testing.T) {
	t.Parallel()
	var sink bytes.Buffer
	cw := &controlsCountingWriter{w: &sink}
	if n, err := cw.Write(nil); err != nil || n != 0 {
		t.Fatalf("Write(nil): n=%d err=%v", n, err)
	}
	if cw.n != 0 {
		t.Fatalf("counter = %d; want 0", cw.n)
	}
}

// ---------------------------------------------------------------------
// ExportHandler.exportLimiter / HistoryExportHandler.exportLimiter
// ---------------------------------------------------------------------

func TestExportHandler_ExportLimiter_DefaultsToProcessSingleton(t *testing.T) {
	t.Parallel()
	h := NewExportHandler(nil)
	got := h.exportLimiter()
	if got == nil {
		t.Fatalf("exportLimiter() = nil; want process-wide default")
	}
}

func TestHistoryExportHandler_ExportLimiter_DefaultsToProcessSingleton(t *testing.T) {
	t.Parallel()
	h := NewHistoryExportHandler(nil)
	got := h.exportLimiter()
	if got == nil {
		t.Fatalf("exportLimiter() = nil; want process-wide default")
	}
}
