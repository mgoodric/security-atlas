// Slice 121 (AC-15 / AC-16 / AC-18): unit tests for the opt-in
// Prometheus `/metrics` fallback endpoint and its auth-exemption
// scoping. These exercise the chi handler shape without a real
// Postgres pool — they exercise the FIRST few middleware in the
// chain (security headers, CORS, bearer auth) plus the literal
// /metrics + /health routes, which is sufficient to bind the contract.

package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/securityheaders"
)

// TestMetricsEndpoint_Disabled_Returns404 asserts that when
// ATLAS_METRICS_FALLBACK_ENABLE is unset (P0-A3 default), GET /metrics
// returns 404. The route is simply absent.
func TestMetricsEndpoint_Disabled_Returns404(t *testing.T) {
	srv := New(Config{})
	// Replicate the FIRST two middleware + bearer auth + the routes
	// httpHandler registers. metricsHandler is nil → no /metrics route.
	root := chi.NewRouter()
	root.Use(securityheaders.Middleware)
	root.Use(httpAuthMiddlewareWithExemptions(srv.credStore, srv.apikeyStore, "/auth/", "/health", "/metrics", "/v1/version", "/v1/install-state", "/v1/calendar.ics"))
	root.Get("/health", srv.handleHealth)
	if srv.metricsHandler != nil {
		root.Method(http.MethodGet, "/metrics", srv.metricsHandler)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	root.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /metrics with fallback off: status=%d body=%q; want 404", rec.Code, rec.Body.String())
	}
}

// TestMetricsEndpoint_Enabled_Returns200WithoutAuth asserts AC-16:
// when the fallback handler is wired in (operator set
// ATLAS_METRICS_FALLBACK_ENABLE=true), GET /metrics succeeds WITHOUT a
// bearer token — the route is in the bearer-exempt list. This is the
// load-bearing property a Prometheus scrape relies on.
func TestMetricsEndpoint_Enabled_Returns200WithoutAuth(t *testing.T) {
	srv := New(Config{})
	srv.AttachMetricsHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "# HELP fake_metric Stand-in for the prom registry.\n")
	}))

	root := chi.NewRouter()
	root.Use(securityheaders.Middleware)
	root.Use(httpAuthMiddlewareWithExemptions(srv.credStore, srv.apikeyStore, "/auth/", "/health", "/metrics", "/v1/version", "/v1/install-state", "/v1/calendar.ics"))
	if srv.metricsHandler != nil {
		root.Method(http.MethodGet, "/metrics", srv.metricsHandler)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	// Deliberately no Authorization header. P0 property: Prometheus
	// scrape works without a bearer.
	root.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /metrics (no auth) status=%d body=%q; want 200", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "# HELP") {
		t.Fatalf("GET /metrics body missing Prometheus exposition: %q", rec.Body.String())
	}
}

// TestMetricsEndpoint_ExemptionScopedExactly is the AC-18 regression:
// the /metrics exemption MUST be path-exact, not a prefix that broadens
// the unauthenticated surface. Other /v1/* paths still require a bearer.
func TestMetricsEndpoint_ExemptionScopedExactly(t *testing.T) {
	srv := New(Config{})
	srv.AttachMetricsHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	root := chi.NewRouter()
	root.Use(httpAuthMiddlewareWithExemptions(srv.credStore, srv.apikeyStore, "/auth/", "/health", "/metrics", "/v1/version", "/v1/install-state", "/v1/calendar.ics"))
	root.Method(http.MethodGet, "/metrics", srv.metricsHandler)
	// Stand in for the slice-006 anchors handler — any /v1/* route is
	// fine for the test since we're proving the bearer middleware
	// rejects.
	root.Get("/v1/anchors", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// /metrics works without auth.
	rec1 := httptest.NewRecorder()
	root.ServeHTTP(rec1, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec1.Code != http.StatusOK {
		t.Errorf("GET /metrics no-auth: status=%d; want 200", rec1.Code)
	}

	// /v1/anchors requires auth — the exemption did NOT spill over.
	rec2 := httptest.NewRecorder()
	root.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/v1/anchors", nil))
	if rec2.Code != http.StatusUnauthorized {
		t.Errorf("GET /v1/anchors no-auth: status=%d body=%q; want 401",
			rec2.Code, rec2.Body.String())
	}
}

// TestSpanFilter_ExcludesHighFrequencyProbes asserts AC-7: the otelhttp
// span filter returns false (skip span) for the four high-frequency
// probe paths and true (record span) for application paths. Cheap
// regression against a future contributor accidentally extending the
// exclusion list past the four documented probes.
func TestSpanFilter_ExcludesHighFrequencyProbes(t *testing.T) {
	skipPaths := []string{"/health", "/metrics", "/v1/version", "/v1/install-state"}
	for _, p := range skipPaths {
		r := httptest.NewRequest(http.MethodGet, p, nil)
		if spanFilter(r) {
			t.Errorf("spanFilter(%q) = true; want false (high-freq probe)", p)
		}
	}
	keepPaths := []string{"/v1/anchors", "/v1/risks", "/v1/evidence:push", "/auth/local/login"}
	for _, p := range keepPaths {
		r := httptest.NewRequest(http.MethodGet, p, nil)
		if !spanFilter(r) {
			t.Errorf("spanFilter(%q) = false; want true (app path)", p)
		}
	}
}

// Compile-time guard: AttachMetricsHandler accepts an http.Handler.
var _ = func() bool {
	srv := &Server{}
	srv.AttachMetricsHandler(http.NewServeMux())
	_ = credstore.New(0)
	return true
}
