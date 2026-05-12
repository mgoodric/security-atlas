package tenancymw_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// TestMiddleware_NoCredential_Passthrough verifies that the middleware is
// a no-op when no credential is attached to the context — the
// bearer-exempt-path safety contract documented on Middleware. The
// handler must still execute and the context must NOT carry a tenant
// (so a downstream that forgets to call tenancy.WithTenant itself fails
// loudly via ErrNoTenant rather than silently picking a wrong tenant).
func TestMiddleware_NoCredential_Passthrough(t *testing.T) {
	called := false
	var sawTenant string
	var sawErr error

	h := tenancymw.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		sawTenant, sawErr = tenancy.TenantFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/auth/oidc/login", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !called {
		t.Fatal("handler was not invoked")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if sawErr == nil {
		t.Fatalf("expected ErrNoTenant in passthrough; got tenant %q", sawTenant)
	}
}

// TestMiddleware_CredentialPresent_SetsTenant verifies the load-bearing
// path: a credential in context produces a tenant on the request
// context for the downstream handler.
func TestMiddleware_CredentialPresent_SetsTenant(t *testing.T) {
	tenantID := uuid.NewString()
	cred := credstore.Credential{ID: "key_test", TenantID: tenantID}

	var sawTenant string
	var sawErr error

	h := tenancymw.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawTenant, sawErr = tenancy.TenantFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/risks", nil)
	req = req.WithContext(authctx.WithCredential(req.Context(), cred))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if sawErr != nil {
		t.Fatalf("TenantFromContext err = %v", sawErr)
	}
	if sawTenant != tenantID {
		t.Fatalf("tenant = %q, want %q", sawTenant, tenantID)
	}
}

// TestMiddleware_MalformedTenant_Fails500 verifies the fail-closed
// behaviour when a credential carries a non-UUID tenant id. This should
// be impossible (credstore validates at issuance) — a 500 here would
// indicate data-store drift, which the middleware refuses to paper over.
func TestMiddleware_MalformedTenant_Fails500(t *testing.T) {
	cred := credstore.Credential{ID: "key_bad", TenantID: "not-a-uuid"}

	called := false
	h := tenancymw.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/risks", nil)
	req = req.WithContext(authctx.WithCredential(req.Context(), cred))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if called {
		t.Fatal("inner handler should NOT have run on malformed tenant id")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

// TestMiddleware_ContextOverride verifies the bearer-exempt-path
// contract from a different angle: even when the middleware no-ops, a
// handler that calls tenancy.WithTenant itself (e.g. /auth/local/login
// with a request-body tenant_id) successfully establishes the tenant.
// The handler's WithTenant wins because context is shadowed.
func TestMiddleware_ContextOverride(t *testing.T) {
	overrideTenant := uuid.NewString()

	var sawTenant string
	h := tenancymw.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, err := tenancy.WithTenant(r.Context(), overrideTenant)
		if err != nil {
			t.Fatalf("WithTenant: %v", err)
		}
		sawTenant, _ = tenancy.TenantFromContext(ctx)
		w.WriteHeader(http.StatusOK)
	}))

	// No credential — exempt path.
	req := httptest.NewRequest(http.MethodPost, "/auth/local/login", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if sawTenant != overrideTenant {
		t.Fatalf("override tenant = %q, want %q", sawTenant, overrideTenant)
	}
}
