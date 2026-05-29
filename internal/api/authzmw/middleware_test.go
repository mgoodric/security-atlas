package authzmw_test

import (
	"context"
	"net/http"
	"net/http/httptest"
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
