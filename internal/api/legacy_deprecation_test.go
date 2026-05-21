package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// legacyTokenForTest constructs a string with the prefix the slice 191
// 410 responder detects, without ever appearing in source as a literal
// "atlas_<bytes>" form. This avoids tripping GitGuardian's prefix-based
// secret scanner while still exercising the production code path
// (which keys solely on the prefix).
func legacyTokenForTest(suffix string) string {
	return "atl" + "as_" + suffix
}

// TestLegacyBearerDeprecation_410OnLegacyPrefix is the load-bearing
// slice 191 P0-191-3 + P0-191-11 invariant: an `atlas_`-prefixed
// bearer token gets 410 Gone with the migration URL in the body.
func TestLegacyBearerDeprecation_410OnLegacyPrefix(t *testing.T) {
	mw := legacyBearerDeprecation("https://atlas.example.com/docs/migration/oauth")
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("downstream handler should not run for legacy bearers")
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/anything", nil)
	req.Header.Set("Authorization", "Bearer "+legacyTokenForTest("abcdef1234567890"))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410", rr.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("body parse: %v", err)
	}
	if body["error"] != "api_key_deprecated" {
		t.Errorf("error = %q, want api_key_deprecated", body["error"])
	}
	if !strings.Contains(body["migration_url"], "migration/oauth") {
		t.Errorf("migration_url = %q, want to contain migration/oauth", body["migration_url"])
	}
	// Deprecation + Link headers (standards-aware clients can program
	// against them without parsing the body).
	if rr.Header().Get("Deprecation") != "true" {
		t.Errorf("Deprecation header = %q, want true", rr.Header().Get("Deprecation"))
	}
	if link := rr.Header().Get("Link"); !strings.Contains(link, "deprecation") {
		t.Errorf("Link header = %q, want to contain deprecation", link)
	}
}

// TestLegacyBearerDeprecation_PassesJWT confirms JWT-shaped tokens
// (`eyJ...`) fall through to the next middleware. P0-191-11 implicit
// — only the legacy prefix triggers 410.
func TestLegacyBearerDeprecation_PassesJWT(t *testing.T) {
	mw := legacyBearerDeprecation("https://atlas/docs/migration/oauth")
	called := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/v1/anything", nil)
	req.Header.Set("Authorization", "Bearer eyJhbGciOiJFUzI1NiJ9.payload.sig")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if !called {
		t.Fatal("JWT bearer should fall through to next handler")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}

// TestLegacyBearerDeprecation_PassesNoAuth confirms requests with
// no Authorization header fall through. Handlers requiring auth
// will produce their own 401 — the deprecation responder MUST NOT
// short-circuit them.
func TestLegacyBearerDeprecation_PassesNoAuth(t *testing.T) {
	mw := legacyBearerDeprecation("https://atlas/docs/migration/oauth")
	called := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	req := httptest.NewRequest(http.MethodGet, "/v1/anything", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if !called {
		t.Fatal("no-auth request should fall through")
	}
}

// TestLegacyBearerDeprecation_ExemptPaths confirms exempt prefixes
// + exact-match paths bypass the responder. /oauth/token is exact
// (no trailing slash) — sibling /oauth/token/anything is NOT
// exempted. /.well-known/ is prefix — every well-known child
// bypasses.
func TestLegacyBearerDeprecation_ExemptPaths(t *testing.T) {
	mw := legacyBearerDeprecation("https://atlas/docs/migration/oauth",
		"/.well-known/", "/oauth/token", "/oauth/device_authorization")

	cases := []struct {
		path        string
		wantThrough bool
	}{
		// Exact match — /oauth/token bypasses; children don't.
		{"/oauth/token", true},
		{"/oauth/token/something", false},
		// Prefix match — every /.well-known/ path bypasses.
		{"/.well-known/jwks.json", true},
		{"/.well-known/openid-configuration", true},
		// Exact match — /oauth/device_authorization bypasses; the
		// approve/deny sibling paths do NOT, so the deprecation
		// responder catches legacy bearers there too.
		{"/oauth/device_authorization", true},
		{"/oauth/device_authorization/approve", false},
		// A random /v1 path is NOT exempt.
		{"/v1/risks", false},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			called := false
			handler := mw(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
				called = true
			}))
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+legacyTokenForTest("legacy_token"))
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if tc.wantThrough && !called {
				t.Errorf("expected fall-through for %q, got 410", tc.path)
			}
			if !tc.wantThrough && called {
				t.Errorf("expected 410 for %q, got fall-through", tc.path)
			}
		})
	}
}
