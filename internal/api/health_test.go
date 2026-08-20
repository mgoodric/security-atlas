package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHandleHealth_ReturnsOK is the slice-037 regression: the
// docker-compose self-host bundle's healthcheck and the atlas-bootstrap
// readiness poll both hit GET /health and require a 200. With no DB pool
// attached the handler reports db="absent" but still 200 — /health is a
// liveness probe, not readiness.
func TestHandleHealth_ReturnsOK(t *testing.T) {
	srv := New(Config{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	srv.handleHealth(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /health status = %d; want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"status":"ok"`) {
		t.Fatalf("GET /health body = %q; want status:ok", body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("GET /health Content-Type = %q; want application/json", ct)
	}
}

func TestHandleReady_NoDBPoolIsNotReady(t *testing.T) {
	srv := New(Config{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	srv.handleReady(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /ready status = %d; want 503", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"status":"not_ready"`) || !strings.Contains(body, `"db":"absent"`) {
		t.Fatalf("GET /ready body = %q; want not_ready absent", body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("GET /ready Content-Type = %q; want application/json", ct)
	}
}
