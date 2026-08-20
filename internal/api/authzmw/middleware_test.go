package authzmw_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/authzmw"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
)

// The production middleware in authzmw is the surface we trust; this
// unit test asserts default-deny + exempt-path behavior via a thin
// wrapper that swaps the audit writer for an in-memory recorder.
// Audit-row assertions on DB-backed writes live in the integration
// suite — the unit test focuses on the middleware contract.

func buildEngine(t *testing.T) *authz.Engine {
	t.Helper()
	e, err := authz.NewEngine(context.Background(), authz.NoopRolesResolver{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

// TestMiddleware_DenyOnMissingCredential covers default-deny when no
// credential is on the request context. Anti-criterion P0 hardening.
func TestMiddleware_DenyOnMissingCredential(t *testing.T) {
	t.Parallel()
	engine := buildEngine(t)
	// nil audit writer is tolerated for unit tests; production wires
	// a real one.
	mw := authzmw.Middleware(engine, nil, "/auth/", "/health")
	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/risks", nil)
	h.ServeHTTP(rec, req)
	if called {
		t.Fatalf("inner handler called on missing-credential request")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

// TestMiddleware_ExemptPathPassthrough covers /auth/* not going through
// authz at all -- the sign-in path needs to work for unauthenticated
// callers.
func TestMiddleware_ExemptPathPassthrough(t *testing.T) {
	t.Parallel()
	engine := buildEngine(t)
	mw := authzmw.Middleware(engine, nil, "/auth/", "/health")
	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/local/login", nil)
	h.ServeHTTP(rec, req)
	if !called {
		t.Fatalf("exempt path /auth/* did not pass through to handler")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from inner handler, got %d", rec.Code)
	}
}

// TestMiddleware_AdminCredentialAllowsWrite covers the legacy-flag
// bridge from slice 014/011/018: a credstore.Credential with IsAdmin
// resolves to the admin role inside BuildInput.
func TestMiddleware_AdminCredentialAllowsWrite(t *testing.T) {
	t.Parallel()
	engine := buildEngine(t)
	mw := authzmw.Middleware(engine, nil, "/auth/", "/health")
	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusCreated)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/risks", nil)
	cred := credstore.Credential{
		ID:       "key_admin",
		TenantID: uuid.NewString(),
		UserID:   "key_admin",
		IsAdmin:  true,
	}
	req = req.WithContext(authctx.WithCredential(req.Context(), cred))
	h.ServeHTTP(rec, req)
	if !called {
		t.Fatalf("admin credential POST /v1/risks did not reach handler; status=%d", rec.Code)
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 from inner handler, got %d", rec.Code)
	}
}

// TestMiddleware_ViewerDeniedWrite covers the read-only viewer role
// hitting a write endpoint. The credstore.Credential bridge maps
// "no flags + no OwnerRoles" to grc_engineer, so to test viewer we
// need a credential that legacy bridge maps somewhere else AND a
// custom RolesResolver -- but for unit purposes, we can verify the
// matrix at the rego layer (already covered in decision_test.go).
// Here we cover default-deny via a credential that produces empty
// roles. Cred with TenantID="" has empty derived roles, so:
func TestMiddleware_NoTenantCredentialDenied(t *testing.T) {
	t.Parallel()
	engine := buildEngine(t)
	mw := authzmw.Middleware(engine, nil, "/auth/", "/health")
	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/risks", nil)
	cred := credstore.Credential{
		// Empty TenantID -- legacy bridge returns no roles.
		ID:     "key_orphan",
		UserID: "key_orphan",
	}
	req = req.WithContext(authctx.WithCredential(req.Context(), cred))
	h.ServeHTTP(rec, req)
	if called {
		t.Fatalf("inner handler called on no-tenant credential")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

// TestIsCredentialPresent covers the exported helper the matrix
// integration test relies on to assert a credential is established on
// the context before authz runs. The contract is a faithful reflection
// of authctx.CredentialFromContext: false when no credential was placed
// on the request context, true once one has been. Both branches are
// asserted so the helper can't silently invert (a false "present" would
// let the matrix test pass against an unauthenticated request).
func TestIsCredentialPresent(t *testing.T) {
	t.Parallel()

	// Absent: a bare request has no credential on its context.
	bare := httptest.NewRequest(http.MethodGet, "/v1/risks", nil)
	if authzmw.IsCredentialPresent(bare) {
		t.Fatalf("IsCredentialPresent = true for a request with no credential in context")
	}

	// Present: once authctx.WithCredential seeds the context, the helper
	// reports true.
	cred := credstore.Credential{
		ID:       "key_present",
		TenantID: uuid.NewString(),
		UserID:   "key_present",
	}
	withCred := bare.WithContext(authctx.WithCredential(bare.Context(), cred))
	if !authzmw.IsCredentialPresent(withCred) {
		t.Fatalf("IsCredentialPresent = false after authctx.WithCredential seeded the context")
	}

	// The original request is unchanged (WithContext returns a copy) —
	// guards against the helper reading process-global state instead of
	// the per-request context.
	if authzmw.IsCredentialPresent(bare) {
		t.Fatalf("IsCredentialPresent = true on the original request after deriving a credentialed copy")
	}
}

// failingResolver makes every DB-backed roles lookup fail with the
// configured error, simulating the slice 356a chaos condition (Postgres
// stopped) without a database in the loop. Unit-tier twin of the
// integration test in integration_test.go.
type failingResolver struct{ err error }

func (f failingResolver) RolesFor(_ context.Context, _, _ string) ([]authz.Role, error) {
	return nil, f.err
}

// captureSlog swaps slog's default handler for a buffer for the
// duration of the test. Callers must NOT use t.Parallel — slog.Default
// is process-global state (same pattern and reasoning as
// httperr's TestWriteInternal_LogsFullErrorServerSide).
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelError})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

func tenantCredRequest(method, path string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	cred := credstore.Credential{
		ID:       "key_outage",
		TenantID: uuid.NewString(),
		UserID:   "key_outage",
		IsAdmin:  true,
	}
	return req.WithContext(authctx.WithCredential(req.Context(), cred))
}

// assertNoLeak asserts the slice 367 error-leak bar on a response body:
// no driver text, no SQLSTATE, no file:line frame, no internal import
// path (OE-432 Do #5 / slice 356a check F-3).
func assertNoLeak(t *testing.T, body string) {
	t.Helper()
	for _, leak := range []string{"pgx", "SQLSTATE", "connection refused", ".go:", "internal/", "dial tcp"} {
		if strings.Contains(body, leak) {
			t.Fatalf("response body leaks internal detail %q: %s", leak, body)
		}
	}
}

// TestMiddleware_DBUnavailableReturns503 is the OE-432 headline unit
// test: with the DB-backed roles resolver failing connection-refused
// (the measured slice 356a outage shape), the middleware must answer
// 503 {"error":"database_unavailable","retry_after":5} — NOT the
// pre-fix 500 {"error":"authorization engine error"} — and must write
// an error-level log line carrying the request context and real cause.
//
// NOT t.Parallel: captures slog.Default (process-global).
func TestMiddleware_DBUnavailableReturns503(t *testing.T) {
	logBuf := captureSlog(t)

	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
	engine, err := authz.NewEngine(context.Background(), failingResolver{err: dialErr})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	mw := authzmw.Middleware(engine, nil, "/auth/", "/health")
	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, tenantCredRequest(http.MethodGet, "/v1/anchors"))

	if called {
		t.Fatalf("inner handler called during DB outage")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "5" {
		t.Fatalf("Retry-After header = %q, want \"5\"", got)
	}

	var body struct {
		Error      string `json:"error"`
		RetryAfter int    `json:"retry_after"`
		RequestID  string `json:"request_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v (%s)", err, rec.Body.String())
	}
	if body.Error != "database_unavailable" {
		t.Fatalf("body.error = %q, want \"database_unavailable\"", body.Error)
	}
	if body.RetryAfter != 5 {
		t.Fatalf("body.retry_after = %d, want 5", body.RetryAfter)
	}
	if body.RequestID == "" {
		t.Fatalf("body.request_id empty; operators need it to pivot to the log line")
	}
	assertNoLeak(t, rec.Body.String())

	// G-2: the underlying error must land server-side at error level
	// with request context — the "120 5xx, 0 log lines" behaviour is
	// the bug this slice kills.
	logged := logBuf.String()
	for _, want := range []string{
		"database dependency unavailable",
		body.RequestID,
		`"path":"/v1/anchors"`,
		"tenant_id",
		"connection refused",
	} {
		if !strings.Contains(logged, want) {
			t.Fatalf("slog output missing %q: %s", want, logged)
		}
	}
}

// TestMiddleware_GenuineEngineErrorStays500 covers the other half of
// the OE-432 distinguishability AC: a Decide failure that is NOT a
// dependency outage (here, a data-shaped resolver error) keeps
// returning 500, with the generic slice-367 body, and is now logged
// server-side (pre-fix this path was silent too).
//
// NOT t.Parallel: captures slog.Default (process-global).
func TestMiddleware_GenuineEngineErrorStays500(t *testing.T) {
	logBuf := captureSlog(t)

	engine, err := authz.NewEngine(context.Background(), failingResolver{err: errors.New("user_roles row scan mismatch")})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	mw := authzmw.Middleware(engine, nil, "/auth/", "/health")
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, tenantCredRequest(http.MethodGet, "/v1/anchors"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 for a non-outage engine error", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v (%s)", err, rec.Body.String())
	}
	if body["error"] == "database_unavailable" {
		t.Fatalf("non-outage error mislabelled as database_unavailable")
	}
	if _, hasRetry := body["retry_after"]; hasRetry {
		t.Fatalf("non-outage error carries a retry_after hint: %s", rec.Body.String())
	}
	assertNoLeak(t, rec.Body.String())

	logged := logBuf.String()
	if !strings.Contains(logged, "authz decide") || !strings.Contains(logged, "row scan mismatch") {
		t.Fatalf("genuine engine error not logged server-side: %s", logged)
	}
}

// TestMiddleware_CatalogReadAllowedForAnyCredential covers the public
// catalog allow rule in defaults.rego -- viewer-class credentials can
// read /v1/anchors etc.
func TestMiddleware_CatalogReadAllowedForAnyCredential(t *testing.T) {
	t.Parallel()
	engine := buildEngine(t)
	mw := authzmw.Middleware(engine, nil, "/auth/", "/health")
	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/anchors", nil)
	cred := credstore.Credential{
		ID:       "key_viewer",
		TenantID: uuid.NewString(),
		UserID:   "key_viewer",
		// no flags -> derived role is grc_engineer (default for in-mem creds)
	}
	req = req.WithContext(authctx.WithCredential(req.Context(), cred))
	h.ServeHTTP(rec, req)
	if !called {
		t.Fatalf("catalog GET /v1/anchors blocked; status=%d", rec.Code)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}
