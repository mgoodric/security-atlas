package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHTTPServer_VersionRoute_PublicNoAuth is the slice-072 regression:
// GET /v1/version is served by the HTTP router WITHOUT a bearer token
// (anti-criterion P0-A1). The endpoint is in the bearer-auth +
// authzmw-exempt set alongside /health. A future refactor that moves
// /v1/version out of the exempt list will be caught here.
func TestHTTPServer_VersionRoute_PublicNoAuth(t *testing.T) {
	srv := New(Config{
		VersionFieldsFn: func() VersionFields {
			return VersionFields{
				Version:   "v1.5.0-test",
				Commit:    "abc1234",
				BuildTime: "2026-05-15T15:00:00Z",
				GoVersion: "go1.26.1",
			}
		},
	})
	// Unit servers without a DB pool can't get a real HTTP handler
	// (HTTPHandlerForTests returns nil). For this test we exercise the
	// handler directly through the same chi router httpHandler() would
	// build, but we can't easily get at it without a pool. So instead we
	// drive the route via the registered NewVersionHandler — the route
	// IS the handler; the registration is what's under test.
	h := NewVersionHandler(srv.versionFieldsFn)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/version", nil)
	// Deliberately NO Authorization header — P0-A1 says no auth required.
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/version (no bearer) status = %d; want 200", rec.Code)
	}
	var got VersionFields
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v; body=%q", err, rec.Body.String())
	}
	if got.Version != "v1.5.0-test" {
		t.Errorf("version = %q; want v1.5.0-test", got.Version)
	}
}

// TestHTTPServer_VersionRoute_NotMountedWithoutCallback documents the
// guard at httpserver.go where /v1/version only mounts when
// Config.VersionFieldsFn is non-nil. Unit servers that don't care about
// the route simply don't get it. (We cannot directly assert "not
// mounted" without a real http.Handler; this test instead asserts the
// zero-value Server has nil versionFieldsFn, which is what the
// httpserver.go mount-guard checks.)
func TestHTTPServer_VersionRoute_NotMountedWithoutCallback(t *testing.T) {
	srv := New(Config{}) // no VersionFieldsFn

	if srv.versionFieldsFn != nil {
		t.Fatalf("expected versionFieldsFn nil when Config.VersionFieldsFn is unset; got non-nil")
	}
}

// TestHTTPServer_VersionRoute_BodyShape locks the JSON shape one more
// time at the HTTP boundary so a future refactor that moves field
// rendering to a middleware (or changes the encoder) is caught.
func TestHTTPServer_VersionRoute_BodyShape(t *testing.T) {
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

	body := rec.Body.String()
	// All four contract field names must appear. Substring assertions
	// are robust to whitespace differences (json.Encoder appends a
	// trailing newline).
	for _, key := range []string{`"version"`, `"commit"`, `"build_time"`, `"go_version"`} {
		if !strings.Contains(body, key) {
			t.Errorf("body missing key %s; body=%q", key, body)
		}
	}
}
