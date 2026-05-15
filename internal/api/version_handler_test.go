package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestVersionHandler_JSONShape is the slice-072 regression: GET /v1/version
// returns a JSON object with the four contract fields the frontend
// (`web/lib/version.ts`) reads. Changing the shape is a breaking change
// to the BFF + the VersionFooter.
func TestVersionHandler_JSONShape(t *testing.T) {
	h := NewVersionHandler(func() VersionFields {
		return VersionFields{
			Version:   "v1.5.0",
			Commit:    "abc1234",
			BuildTime: "2026-05-15T15:00:00Z",
			GoVersion: "go1.26.1",
		}
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/version", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%q", err, rec.Body.String())
	}
	wantFields := map[string]string{
		"version":    "v1.5.0",
		"commit":     "abc1234",
		"build_time": "2026-05-15T15:00:00Z",
		"go_version": "go1.26.1",
	}
	for k, v := range wantFields {
		if got[k] != v {
			t.Errorf("body[%q] = %q; want %q", k, got[k], v)
		}
	}
}

// TestVersionHandler_ContentType locks in the JSON content type the BFF
// proxy and downstream tooling expect.
func TestVersionHandler_ContentType(t *testing.T) {
	h := NewVersionHandler(func() VersionFields {
		return VersionFields{Version: "dev"}
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/version", nil)
	h.ServeHTTP(rec, req)

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q; want application/json", ct)
	}
}

// TestVersionHandler_CacheControl confirms the aggressive browser-cache
// header (slice-072 AC-2 / P0-A5 — over-fetching is the failure mode).
// 300s = 5 minutes; version doesn't change between binary restarts so
// the browser does not need to re-fetch every page load.
func TestVersionHandler_CacheControl(t *testing.T) {
	h := NewVersionHandler(func() VersionFields {
		return VersionFields{Version: "dev"}
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/version", nil)
	h.ServeHTTP(rec, req)

	const want = "public, max-age=300"
	if cc := rec.Header().Get("Cache-Control"); cc != want {
		t.Fatalf("Cache-Control = %q; want %q", cc, want)
	}
}

// TestVersionHandler_FieldsFnNotNil ensures the constructor refuses a
// nil callback (the route would otherwise panic on first request, which
// is a worse failure mode than refusing to construct).
func TestVersionHandler_FieldsFnNotNil(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on nil fieldsFn; got none")
		}
	}()
	_ = NewVersionHandler(nil)
}
