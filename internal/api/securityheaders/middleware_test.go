package securityheaders

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// helper: build a Middleware-wrapped handler that responds 200 OK with a
// tiny body. The downstream handler is intentionally trivial so tests
// focus exclusively on the header contract.
func wrappedOK() http.Handler {
	return Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
}

// runOnce drives one request through the middleware and returns the
// recorder so each test can assert against headers + status.
func runOnce(t *testing.T, h http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/anything", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("downstream status = %d; want 200", rec.Code)
	}
	return rec
}

func TestMiddleware_SetsHSTS(t *testing.T) {
	rec := runOnce(t, wrappedOK())
	got := rec.Header().Get("Strict-Transport-Security")
	if got != HSTSMaxAge {
		t.Fatalf("Strict-Transport-Security = %q; want %q", got, HSTSMaxAge)
	}
	// Belt-and-suspenders on the substring shape so a future refactor of
	// HSTSMaxAge that drops includeSubDomains gets caught here too.
	if !strings.Contains(got, "max-age=31536000") {
		t.Errorf("HSTS missing max-age=31536000: %q", got)
	}
	if !strings.Contains(got, "includeSubDomains") {
		t.Errorf("HSTS missing includeSubDomains: %q", got)
	}
}

func TestMiddleware_SetsXContentTypeOptions(t *testing.T) {
	rec := runOnce(t, wrappedOK())
	got := rec.Header().Get("X-Content-Type-Options")
	if got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q; want nosniff", got)
	}
}

func TestMiddleware_SetsXFrameOptions(t *testing.T) {
	rec := runOnce(t, wrappedOK())
	got := rec.Header().Get("X-Frame-Options")
	if got != "DENY" {
		t.Fatalf("X-Frame-Options = %q; want DENY", got)
	}
}

func TestMiddleware_SetsReferrerPolicy(t *testing.T) {
	rec := runOnce(t, wrappedOK())
	got := rec.Header().Get("Referrer-Policy")
	if got != "strict-origin-when-cross-origin" {
		t.Fatalf("Referrer-Policy = %q; want strict-origin-when-cross-origin", got)
	}
}

func TestMiddleware_SetsCSPReportOnly(t *testing.T) {
	rec := runOnce(t, wrappedOK())
	got := rec.Header().Get("Content-Security-Policy-Report-Only")
	if got != CSP {
		t.Fatalf("Content-Security-Policy-Report-Only = %q;\n want %q", got, CSP)
	}
	// Spot-check that the load-bearing directives survive any future
	// CSP-constant edit — these are the directives the 2026-Q2 audit
	// finding specifically calls out.
	mustContain := []string{
		"default-src 'self'",
		"script-src 'self'",
		"frame-ancestors 'none'",
		"base-uri 'self'",
		"form-action 'self'",
	}
	for _, want := range mustContain {
		if !strings.Contains(got, want) {
			t.Errorf("CSP missing directive %q; got %q", want, got)
		}
	}
	// Anti-criterion check: this slice ships report-only ONLY. An
	// enforced Content-Security-Policy header would break Next.js
	// hydration today (decisions log §D1).
	if rec.Header().Get("Content-Security-Policy") != "" {
		t.Errorf("expected NO enforced Content-Security-Policy; got %q",
			rec.Header().Get("Content-Security-Policy"))
	}
}

// TestMiddleware_OrderIndependent verifies the middleware sets headers
// even when the downstream handler writes its own headers / body /
// status. The middleware must work regardless of what next does.
func TestMiddleware_OrderIndependent(t *testing.T) {
	// downstream writes a custom header + non-200 status BEFORE the
	// security headers would normally be flushed.
	downstream := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Custom", "yes")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("nope"))
	})
	h := Middleware(downstream)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/anything", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d; want 418 (downstream's status preserved)", rec.Code)
	}
	if rec.Header().Get("X-Custom") != "yes" {
		t.Errorf("downstream's X-Custom header lost; got %q", rec.Header().Get("X-Custom"))
	}
	// All five security headers still present.
	mustPresent := map[string]string{
		"Strict-Transport-Security":           HSTSMaxAge,
		"X-Content-Type-Options":              "nosniff",
		"X-Frame-Options":                     "DENY",
		"Referrer-Policy":                     "strict-origin-when-cross-origin",
		"Content-Security-Policy-Report-Only": CSP,
	}
	for k, want := range mustPresent {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("header %s = %q; want %q (order-independence regression)", k, got, want)
		}
	}
}

// TestMiddleware_DoesNotOverrideContentType is a negative test: the
// security headers must NOT overwrite the downstream Content-Type. A
// future refactor that accidentally Header().Set("Content-Type", ...)
// in the middleware would silently break every JSON endpoint.
func TestMiddleware_DoesNotOverrideContentType(t *testing.T) {
	downstream := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	h := Middleware(downstream)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/anything", nil)
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q; want application/json (middleware clobbered downstream)", got)
	}
}
