package mcpwriteproposals

// Pure-Go unit coverage for the slice-173 HTTP handler. The integration
// suite (integration_test.go, build tag `integration`) drives the
// happy paths against real Postgres + the chi router + bearer auth;
// this file pins the cheap pre-DB / pre-store branches so they survive
// a missing DATABASE_URL_APP at CI time and so merged coverage clears
// the slice-317 70% target.
//
// Load-bearing functions / branches covered here:
//
//   - New                — constructor returns non-nil
//   - tenantCredContext  — 401-paths (no credential, empty tenant id,
//                          missing tenancy GUC)
//   - CreateProposal     — 401 (no auth), 400 (bad JSON), 400 (unknown
//                          tool), 400 (missing ai_model_* fields)
//   - ListProposals      — 401 (no auth)
//   - GetProposal        — 401 (no auth), 400 (non-UUID id)
//   - ConfirmProposal    — 401 (no auth), 403 (no approver role),
//                          400 (non-UUID id)
//   - RejectProposal     — 401 (no auth), 403 (no approver role),
//                          400 (non-UUID id)
//   - writeCreateErr     — every documented branch (Unknown tool /
//                          Invalid input / Pending cap exceeded /
//                          fallthrough server error)
//   - writeTransitionErr — every documented branch (NotFound /
//                          WrongState / UnknownTool / fallthrough)
//   - writeServerErr     — direct call exercises the 500 wrap
//   - proposalWireFrom   — empty-ToolInput defaults to "{}"
//
// The 401 / 403 / 400 branches are reachable without a Store because
// they return BEFORE h.store is dereferenced — Handler{store: nil} is
// load-bearing for these tests.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/mcp/writeproposals"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ----- harness -----

// withTenantAndCred attaches the credential + the tenancy GUC the
// handler expects. Returns a context that satisfies tenantCredContext.
func withTenantAndCred(t *testing.T, cred credstore.Credential) context.Context {
	t.Helper()
	ctx := authctx.WithCredential(context.Background(), cred)
	out, err := tenancy.WithTenant(ctx, cred.TenantID)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return out
}

// withURLParam injects a chi URLParam so chi.URLParam(r, key) resolves
// when the handler is driven directly without going through the chi
// router. Mirrors the pattern used by internal/api/metrics handler tests.
func withURLParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func validCred() credstore.Credential {
	return credstore.Credential{
		ID:       "key_unit_test",
		TenantID: uuid.NewString(),
	}
}

func approverCred() credstore.Credential {
	c := validCred()
	c.IsApprover = true
	return c
}

// ----- New + proposalWireFrom -----

func TestNew_ReturnsHandler(t *testing.T) {
	t.Parallel()
	if h := New(nil); h == nil {
		t.Fatal("New(nil) returned nil")
	}
}

func TestProposalWireFrom_EmptyToolInputDefaultsToEmptyObject(t *testing.T) {
	t.Parallel()
	in := writeproposals.Proposal{
		ID:       uuid.New(),
		TenantID: uuid.New(),
		ToolName: writeproposals.ToolCreateRisk,
	}
	wire := proposalWireFrom(in)
	if string(wire.ToolInput) != "{}" {
		t.Fatalf("empty ToolInput rendered as %q, want \"{}\"", wire.ToolInput)
	}
}

func TestProposalWireFrom_PreservesProvidedToolInput(t *testing.T) {
	t.Parallel()
	in := writeproposals.Proposal{
		ID:        uuid.New(),
		TenantID:  uuid.New(),
		ToolInput: json.RawMessage(`{"title":"DC outage"}`),
	}
	wire := proposalWireFrom(in)
	if string(wire.ToolInput) != `{"title":"DC outage"}` {
		t.Fatalf("ToolInput round-trip mismatch: %q", wire.ToolInput)
	}
}

// ----- tenantCredContext -----

func TestTenantCredContext_NoCredential(t *testing.T) {
	t.Parallel()
	h := New(nil)
	r := httptest.NewRequest(http.MethodGet, "/v1/mcp/write-proposals", nil)
	if _, _, ok := h.tenantCredContext(r); ok {
		t.Fatal("expected ok=false when credential missing")
	}
}

func TestTenantCredContext_EmptyTenantID(t *testing.T) {
	t.Parallel()
	h := New(nil)
	r := httptest.NewRequest(http.MethodGet, "/v1/mcp/write-proposals", nil)
	r = r.WithContext(authctx.WithCredential(r.Context(), credstore.Credential{ID: "k"}))
	if _, _, ok := h.tenantCredContext(r); ok {
		t.Fatal("expected ok=false when TenantID empty")
	}
}

func TestTenantCredContext_MissingTenancyGUC(t *testing.T) {
	t.Parallel()
	h := New(nil)
	r := httptest.NewRequest(http.MethodGet, "/v1/mcp/write-proposals", nil)
	cred := validCred()
	// Cred has TenantID but the tenancy GUC was never installed.
	r = r.WithContext(authctx.WithCredential(r.Context(), cred))
	if _, _, ok := h.tenantCredContext(r); ok {
		t.Fatal("expected ok=false when tenancy GUC missing")
	}
}

func TestTenantCredContext_HappyPath(t *testing.T) {
	t.Parallel()
	h := New(nil)
	cred := validCred()
	r := httptest.NewRequest(http.MethodGet, "/v1/mcp/write-proposals", nil)
	r = r.WithContext(withTenantAndCred(t, cred))
	ctx, got, ok := h.tenantCredContext(r)
	if !ok {
		t.Fatal("expected ok=true on full context")
	}
	if got.ID != cred.ID {
		t.Fatalf("cred.ID = %q, want %q", got.ID, cred.ID)
	}
	if ctx == nil {
		t.Fatal("ctx must be non-nil on happy path")
	}
}

// ----- CreateProposal: pre-store branches -----

func TestCreateProposal_401WhenUnauthenticated(t *testing.T) {
	t.Parallel()
	h := New(nil)
	r := httptest.NewRequest(http.MethodPost, "/v1/mcp/write-proposals", bytes.NewReader([]byte(`{}`)))
	w := httptest.NewRecorder()
	h.CreateProposal(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestCreateProposal_400OnBadJSON(t *testing.T) {
	t.Parallel()
	h := New(nil)
	r := httptest.NewRequest(http.MethodPost, "/v1/mcp/write-proposals", bytes.NewReader([]byte(`{not-json`)))
	r = r.WithContext(withTenantAndCred(t, validCred()))
	w := httptest.NewRecorder()
	h.CreateProposal(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "invalid JSON body") {
		t.Fatalf("body = %s, want invalid-JSON message", w.Body.String())
	}
}

func TestCreateProposal_400OnUnknownTool(t *testing.T) {
	t.Parallel()
	h := New(nil)
	body := `{"tool_name":"delete_tenant","tool_input":{},"ai_model_name":"m","ai_model_version":"v"}`
	r := httptest.NewRequest(http.MethodPost, "/v1/mcp/write-proposals", bytes.NewReader([]byte(body)))
	r = r.WithContext(withTenantAndCred(t, validCred()))
	w := httptest.NewRecorder()
	h.CreateProposal(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "unknown tool_name") {
		t.Fatalf("body = %s, want unknown-tool message", w.Body.String())
	}
}

func TestCreateProposal_400OnMissingModelFields(t *testing.T) {
	t.Parallel()
	h := New(nil)
	cases := []string{
		`{"tool_name":"create_risk","tool_input":{},"ai_model_name":"","ai_model_version":"v"}`,
		`{"tool_name":"create_risk","tool_input":{},"ai_model_name":"m","ai_model_version":""}`,
	}
	for i, body := range cases {
		r := httptest.NewRequest(http.MethodPost, "/v1/mcp/write-proposals", bytes.NewReader([]byte(body)))
		r = r.WithContext(withTenantAndCred(t, validCred()))
		w := httptest.NewRecorder()
		h.CreateProposal(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("case %d: status = %d, want 400", i, w.Code)
		}
		if !strings.Contains(w.Body.String(), "ai_model_name and ai_model_version are required") {
			t.Fatalf("case %d: body = %s, want required-fields message", i, w.Body.String())
		}
	}
}

// ----- ListProposals: pre-store branches -----

func TestListProposals_401WhenUnauthenticated(t *testing.T) {
	t.Parallel()
	h := New(nil)
	r := httptest.NewRequest(http.MethodGet, "/v1/mcp/write-proposals", nil)
	w := httptest.NewRecorder()
	h.ListProposals(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

// ----- GetProposal: pre-store branches -----

func TestGetProposal_401WhenUnauthenticated(t *testing.T) {
	t.Parallel()
	h := New(nil)
	r := httptest.NewRequest(http.MethodGet, "/v1/mcp/write-proposals/abc", nil)
	r = withURLParam(r, "id", "abc")
	w := httptest.NewRecorder()
	h.GetProposal(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestGetProposal_400OnNonUUID(t *testing.T) {
	t.Parallel()
	h := New(nil)
	r := httptest.NewRequest(http.MethodGet, "/v1/mcp/write-proposals/abc", nil)
	r = withURLParam(r, "id", "abc")
	r = r.WithContext(withTenantAndCred(t, validCred()))
	w := httptest.NewRecorder()
	h.GetProposal(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "id must be a UUID") {
		t.Fatalf("body = %s, want id-must-be-UUID message", w.Body.String())
	}
}

// ----- ConfirmProposal: pre-store branches -----

func TestConfirmProposal_401WhenUnauthenticated(t *testing.T) {
	t.Parallel()
	h := New(nil)
	r := httptest.NewRequest(http.MethodPost, "/v1/mcp/write-proposals/x/confirm", nil)
	r = withURLParam(r, "id", "x")
	w := httptest.NewRecorder()
	h.ConfirmProposal(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestConfirmProposal_403WhenNotApprover(t *testing.T) {
	t.Parallel()
	h := New(nil)
	r := httptest.NewRequest(http.MethodPost, "/v1/mcp/write-proposals/x/confirm", nil)
	r = withURLParam(r, "id", "x")
	r = r.WithContext(withTenantAndCred(t, validCred())) // not approver, not admin
	w := httptest.NewRecorder()
	h.ConfirmProposal(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if !strings.Contains(w.Body.String(), "approver role required") {
		t.Fatalf("body = %s, want approver-required message", w.Body.String())
	}
}

func TestConfirmProposal_400OnNonUUID(t *testing.T) {
	t.Parallel()
	h := New(nil)
	r := httptest.NewRequest(http.MethodPost, "/v1/mcp/write-proposals/abc/confirm", nil)
	r = withURLParam(r, "id", "abc")
	r = r.WithContext(withTenantAndCred(t, approverCred()))
	w := httptest.NewRecorder()
	h.ConfirmProposal(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// ----- RejectProposal: pre-store branches -----

func TestRejectProposal_401WhenUnauthenticated(t *testing.T) {
	t.Parallel()
	h := New(nil)
	r := httptest.NewRequest(http.MethodPost, "/v1/mcp/write-proposals/x/reject", nil)
	r = withURLParam(r, "id", "x")
	w := httptest.NewRecorder()
	h.RejectProposal(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestRejectProposal_403WhenNotApprover(t *testing.T) {
	t.Parallel()
	h := New(nil)
	r := httptest.NewRequest(http.MethodPost, "/v1/mcp/write-proposals/x/reject", nil)
	r = withURLParam(r, "id", "x")
	r = r.WithContext(withTenantAndCred(t, validCred()))
	w := httptest.NewRecorder()
	h.RejectProposal(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestRejectProposal_400OnNonUUID(t *testing.T) {
	t.Parallel()
	h := New(nil)
	r := httptest.NewRequest(http.MethodPost, "/v1/mcp/write-proposals/abc/reject", nil)
	r = withURLParam(r, "id", "abc")
	r = r.WithContext(withTenantAndCred(t, approverCred()))
	w := httptest.NewRecorder()
	h.RejectProposal(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// ----- writeCreateErr: every documented branch -----

func TestWriteCreateErr_UnknownTool(t *testing.T) {
	t.Parallel()
	h := New(nil)
	w := httptest.NewRecorder()
	h.writeCreateErr(w, httptest.NewRequest(http.MethodGet, "/", nil), writeproposals.ErrUnknownTool)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestWriteCreateErr_InvalidInput(t *testing.T) {
	t.Parallel()
	h := New(nil)
	w := httptest.NewRecorder()
	h.writeCreateErr(w, httptest.NewRequest(http.MethodGet, "/", nil), writeproposals.ErrInvalidInput)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestWriteCreateErr_PendingCapExceeded(t *testing.T) {
	t.Parallel()
	h := New(nil)
	w := httptest.NewRecorder()
	h.writeCreateErr(w, httptest.NewRequest(http.MethodGet, "/", nil), writeproposals.ErrPendingCapExceeded)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", w.Code)
	}
	if !strings.Contains(w.Body.String(), "pending proposal cap exceeded") {
		t.Fatalf("body = %s, want cap-exceeded message", w.Body.String())
	}
}

func TestWriteCreateErr_Fallthrough500(t *testing.T) {
	t.Parallel()
	h := New(nil)
	w := httptest.NewRecorder()
	h.writeCreateErr(w, httptest.NewRequest(http.MethodGet, "/", nil), errors.New("unexpected"))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}

// ----- writeTransitionErr: every documented branch -----

func TestWriteTransitionErr_NotFound(t *testing.T) {
	t.Parallel()
	h := New(nil)
	w := httptest.NewRecorder()
	h.writeTransitionErr(w, httptest.NewRequest(http.MethodGet, "/", nil), writeproposals.ErrNotFound)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestWriteTransitionErr_WrongState(t *testing.T) {
	t.Parallel()
	h := New(nil)
	w := httptest.NewRecorder()
	h.writeTransitionErr(w, httptest.NewRequest(http.MethodGet, "/", nil), writeproposals.ErrWrongState)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
}

func TestWriteTransitionErr_UnknownTool(t *testing.T) {
	t.Parallel()
	h := New(nil)
	w := httptest.NewRecorder()
	h.writeTransitionErr(w, httptest.NewRequest(http.MethodGet, "/", nil), writeproposals.ErrUnknownTool)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestWriteTransitionErr_Fallthrough500(t *testing.T) {
	t.Parallel()
	h := New(nil)
	w := httptest.NewRecorder()
	h.writeTransitionErr(w, httptest.NewRequest(http.MethodGet, "/", nil), errors.New("unexpected"))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}

// ----- writeServerErr: direct exercise -----

// TestWriteServerErr_GenericInternalError — slice 367 rewires
// writeServerErr to delegate to httperr.WriteInternal which emits a
// generic "internal error" body + a request_id, NEVER the raw err.Error().
// CWE-209 closure.
func TestWriteServerErr_GenericInternalError(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/whatever", nil)
	writeServerErr(w, r, "list proposals", errors.New("db down"))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if strings.Contains(w.Body.String(), "db down") {
		t.Fatalf("slice 367 regression: body leaked raw err: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "list proposals") {
		t.Fatalf("slice 367 regression: body leaked op label: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "internal error") {
		t.Fatalf("body = %s, want generic message", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "request_id") {
		t.Fatalf("body = %s, want request_id field", w.Body.String())
	}
}
