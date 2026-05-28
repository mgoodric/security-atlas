//go:build integration

// Slice 326 — Legacy bearer 410-Gone deprecation responder retirement.
//
// This test is the elevation-of-privilege guard from the slice 326
// threat model (STRIDE E). With `legacyBearerDeprecation` removed from
// the middleware stack, requests presenting a legacy `atlas_`-prefixed
// bearer MUST hit the JWT path (jwtmw.extractJWT rejects the shape and
// passes through) and be terminated by `requireCredential` with 401 —
// NEVER 410 Gone (the retired responder), NEVER 200 (auth bypass).
//
// The test is load-bearing per slice 326 P0-326-1: failing it blocks
// merge unconditionally. The reviewer's specific concern from §2
// "OAuth substrate watch this surface" was that removing the
// deprecation responder without the JWT middleware in front of it
// would create a fall-through-equals-bypass class of bug. This test
// is the regression guard against that bug.
//
// Per AC-7 + slice 326's "Notes for the implementing agent":
// the test hits a real httpserver (chi router, full middleware chain)
// with a real Postgres connection. The integration build tag aligns
// it with the project's testing-discipline floor (`go test
// -tags=integration -p 1 ./internal/...`).

package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
)

// appDSNForLegacyTest reads DATABASE_URL_APP (the atlas_app role) which
// the platform code paths use at request time. Skips if unset so the
// test is portable across CI shapes (matches the convention used by
// internal/api/anchors/integration_test.go + internal/api/search/integration_test.go).
func appDSNForLegacyTest(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL_APP")
	if v == "" {
		t.Skip("DATABASE_URL_APP not set; skipping integration test")
	}
	return v
}

// openLegacyTestPool dials the app-role pool with a bounded connect
// deadline so a misconfigured environment fails fast rather than
// hanging the suite.
func openLegacyTestPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// legacyBearerPatternForTest constructs a string with the prefix the
// slice 191 410 responder used to detect, without ever appearing in
// source as a literal `atlas_<bytes>` form. This avoids tripping
// GitGuardian's prefix-based secret scanner while still exercising
// the production code path. Same trick as the deleted
// `legacyTokenForTest` helper from `legacy_deprecation_test.go`.
func legacyBearerPatternForTest(suffix string) string {
	return "atl" + "as_test_" + suffix
}

// setupRetirementHarness boots a real httpserver wired to the
// supplied app-role pool, mints a JWT via the slice-190 path for the
// positive-control assertion, and returns the running test server +
// the JWT bearer.
func setupRetirementHarness(t *testing.T, pool *pgxpool.Pool, tenant string) (*httptest.Server, string) {
	t.Helper()
	srv := api.New(api.Config{RotationGrace: time.Hour})
	srv.AttachDB(pool)
	bearer := srv.IssueTestJWT(t, testjwt.ViewerFor(uuid.MustParse(tenant)))
	handler := srv.HTTPHandlerForTests()
	if handler == nil {
		t.Fatal("HTTPHandlerForTests returned nil; AttachDB did not take effect")
	}
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts, bearer
}

// doGet issues a GET to the test server with the supplied Authorization
// header value. Empty `auth` omits the header entirely.
func doGet(t *testing.T, ts *httptest.Server, path, auth string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}

// TestLegacyBearer_NoLongerRecognized_Returns401_NotElevation is the
// load-bearing slice 326 P0-326-1 regression. With the
// `legacyBearerDeprecation` 410 responder REMOVED from the
// middleware stack, a request carrying a legacy `atlas_`-prefixed
// bearer MUST:
//
//  1. NOT receive 410 Gone (the retired responder's signature).
//  2. NOT receive 200 OK (the elevation-of-privilege failure mode
//     the reviewer flagged in §2: fall-through to a handler).
//  3. Receive 401 Unauthorized — the JWT middleware's
//     `requireCredential` gate catches the no-credential state
//     because the legacy bearer does not parse as a JWT.
//
// The response body MUST NOT include the `migration_url` field that
// the retired responder used to emit; presence of that field would
// mean the responder is still wired somewhere.
//
// The test name explicitly calls out the elevation-of-privilege guard
// semantics so a future contributor reading the test sees what it is
// guarding against.
func TestLegacyBearer_NoLongerRecognized_Returns401_NotElevation(t *testing.T) {
	pool := openLegacyTestPool(t, appDSNForLegacyTest(t))
	tenant := uuid.NewString()
	ts, _ := setupRetirementHarness(t, pool, tenant)

	// Drive a non-exempt /v1/* path. /v1/anchors is bearer-required
	// (not in the exempt list) and exists in the real router after
	// AttachDB. The exact path is not load-bearing; any /v1/* route
	// that the responder used to short-circuit would do.
	resp, body := doGet(t, ts, "/v1/anchors",
		"Bearer "+legacyBearerPatternForTest("legacy_pattern_abc123"))

	// (1) 410 Gone is the retired responder's signature; seeing it
	// here means the responder is still mounted somewhere.
	if resp.StatusCode == http.StatusGone {
		t.Fatalf("status = 410 Gone; the legacyBearerDeprecation responder must be retired (body=%q)", string(body))
	}
	// (2) 200 OK on a non-exempt path with no valid credential is
	// the elevation-of-privilege failure mode (P0-326-1).
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("status = 200 OK on legacy-bearer request; this is the elevation-of-privilege failure mode (P0-326-1)")
	}
	// (3) 401 is the post-retirement contract.
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401 Unauthorized (legacy bearer must hit requireCredential 401)", resp.StatusCode)
	}

	// Body MUST NOT carry the retired responder's `migration_url`.
	// jwtmw + requireCredential produce a `{"error":"..."}` shape; the
	// retired responder's body was `{"error":"api_key_deprecated", "migration_url":"..."}`.
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err == nil {
		if _, ok := parsed["migration_url"]; ok {
			t.Errorf("response body carries migration_url=%v; the 410 responder must be fully retired", parsed["migration_url"])
		}
		if parsed["error"] == "api_key_deprecated" {
			t.Errorf("response error=%q; the retired responder's signature MUST NOT appear", parsed["error"])
		}
	}
}

// TestValidJWT_StillAuthenticates_AfterRetirement is the positive
// control for AC-7: with the responder gone, a request bearing a
// freshly minted slice-190 JWT MUST still authenticate end-to-end.
// This proves the cleanup did not break the JWT path itself — the
// removal was surgical.
func TestValidJWT_StillAuthenticates_AfterRetirement(t *testing.T) {
	pool := openLegacyTestPool(t, appDSNForLegacyTest(t))
	tenant := uuid.NewString()
	ts, bearer := setupRetirementHarness(t, pool, tenant)

	// /v1/anchors with a valid bearer returns 200 + a JSON envelope.
	// The actual anchor list shape is not load-bearing here — any
	// non-401 + non-410 + non-5xx response demonstrates the JWT
	// path resolves end-to-end.
	resp, body := doGet(t, ts, "/v1/anchors", "Bearer "+bearer)
	if resp.StatusCode == http.StatusGone {
		t.Fatalf("status = 410 with valid JWT; responder retirement is incomplete (body=%q)", string(body))
	}
	if resp.StatusCode == http.StatusUnauthorized {
		t.Fatalf("status = 401 with valid JWT; JWT path broken post-retirement (body=%q)", string(body))
	}
	// 200 OK is the expected post-retirement outcome.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d with valid JWT; want 200. body=%q", resp.StatusCode, string(body))
	}
}
