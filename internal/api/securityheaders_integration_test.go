package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/mgoodric/security-atlas/internal/api/securityheaders"
)

// TestSecurityHeaders_AppliedToHealth is the slice-087 integration
// regression: the hardening headers must be present on /health, which is
// bearer-exempt and authzmw-exempt and therefore proves the headers are
// applied BEFORE the auth middleware short-circuits the chain.
//
// The full Server.httpHandler() requires a pgx pool; here we replicate
// the FIRST two middleware in the production chain (securityheaders +
// corsMiddleware) plus the /health route directly, which is sufficient
// to assert the contract slice 087 binds: every response carries the
// five hardening headers, including unauthenticated paths.
//
// AC-4 of slice 087 is realized across this file (unauth surface) +
// internal/api/securityheaders/middleware_test.go (per-header unit
// asserts) + the slice-069 Playwright spec at
// web/e2e/security-headers.spec.ts (real browser headers).
func TestSecurityHeaders_AppliedToHealth(t *testing.T) {
	srv := New(Config{})

	root := chi.NewRouter()
	// Same first-two ordering as httpserver.go: securityheaders MUST run
	// before any other middleware so it covers 401s and the auth-exempt
	// surface uniformly.
	root.Use(securityheaders.Middleware)
	root.Use(corsMiddleware)
	root.Get("/health", srv.handleHealth)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	root.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /health status = %d; want 200", rec.Code)
	}
	assertAllSecurityHeaders(t, rec, "GET /health")
}

// TestSecurityHeaders_AppliedToAuthError is the defense-in-depth
// regression for AC-4: even when the bearer-auth middleware writes a 401
// (no Authorization header), the response MUST carry the five hardening
// headers. The whole point of mounting securityheaders FIRST is that the
// 401 short-circuit cannot strip them.
func TestSecurityHeaders_AppliedToAuthError(t *testing.T) {
	srv := New(Config{})

	root := chi.NewRouter()
	root.Use(securityheaders.Middleware)
	root.Use(corsMiddleware)
	// httpAuthMiddlewareWithExemptions with no exempt prefixes: every
	// request must have a bearer or it 401s. Drive an unprotected
	// downstream handler that should never be reached.
	root.Use(httpAuthMiddlewareWithExemptions(srv.credStore, srv.apikeyStore))
	root.Get("/protected", func(w http.ResponseWriter, _ *http.Request) {
		t.Errorf("downstream reached; bearer-auth should have rejected")
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	// Deliberately no Authorization header.
	root.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated request; got %d", rec.Code)
	}
	assertAllSecurityHeaders(t, rec, "GET /protected (401)")
}

// TestSecurityHeaders_AppliedToAuthBearerExempt is the third
// AC-4 surface: the /auth/* prefix is bearer-exempt (slice 034 — the
// user has no bearer yet at sign-in). The middleware ordering MUST
// still apply hardening headers there, because /login is exactly where
// a clickjacking or MIME-sniff attack would land.
func TestSecurityHeaders_AppliedToAuthBearerExempt(t *testing.T) {
	srv := New(Config{})

	root := chi.NewRouter()
	root.Use(securityheaders.Middleware)
	root.Use(corsMiddleware)
	root.Use(httpAuthMiddlewareWithExemptions(srv.credStore, srv.apikeyStore, "/auth/"))
	// Wire a tiny /auth/probe to stand in for the bearer-exempt path.
	root.Get("/auth/probe", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth/probe", nil)
	// Deliberately no Authorization header; the /auth/ prefix is exempt.
	root.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /auth/probe status = %d; want 200 (exempt)", rec.Code)
	}
	assertAllSecurityHeaders(t, rec, "GET /auth/probe")
}

// assertAllSecurityHeaders verifies the five header names + expected
// values are present on the response recorder. Centralized so adding a
// sixth header lands one edit, and so every assertion uses the same
// value constants the production middleware uses (no drift between
// "what we assert" and "what we ship").
func assertAllSecurityHeaders(t *testing.T, rec *httptest.ResponseRecorder, label string) {
	t.Helper()
	want := map[string]string{
		"Strict-Transport-Security":           securityheaders.HSTSMaxAge,
		"X-Content-Type-Options":              "nosniff",
		"X-Frame-Options":                     "DENY",
		"Referrer-Policy":                     "strict-origin-when-cross-origin",
		"Content-Security-Policy-Report-Only": securityheaders.CSP,
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s: header %s = %q; want %q", label, k, got, v)
		}
	}
	// Slice 087 ships report-only ONLY — an enforced CSP header would
	// break Next.js hydration today. The decisions log §D1 documents the
	// trajectory.
	if got := rec.Header().Get("Content-Security-Policy"); got != "" {
		t.Errorf("%s: unexpected enforced Content-Security-Policy header = %q (slice 087 ships report-only)",
			label, got)
	}
}
