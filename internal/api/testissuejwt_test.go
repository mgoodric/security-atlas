// Slice 201 — unit tests for the env-gated POST /v1/test/issue-jwt
// endpoint that mints a JWT for the Playwright e2e harness.
//
// The endpoint is the runtime analog of the slice 197 in-test
// `Server.IssueTestJWT` helper: where the latter is callable only from
// `*testing.T` (compile-time gated), the former is callable from any
// process — gated AT REQUEST TIME on `ATLAS_TEST_MODE=1`. The Playwright
// global-setup module reads the response and seeds it into
// `process.env.TEST_BEARER` for downstream specs.
//
// P0-201-2: in production the env var is unset; the handler MUST refuse
// with 404 so the endpoint is indistinguishable from a missing route.
// P0-201-4: the handler MUST sign via the slice 187 OAuth keystore that
// the production middleware is gated on — no parallel test-only
// signer surface. Asserted by round-tripping the returned token through
// the SAME signer the Server is wired with.
//
// Tests live in `package api` (not `api_test`) so they reach the
// unexported `handleIssueTestJWT` directly. The mount-time gating
// (env unset → route absent) is exercised by the chi-router-level
// integration test in `securityheaders_integration_test.go` style —
// here we focus on the handler's per-request env check, which is the
// load-bearing defense (the chi mount gate is a second layer).
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/keystore/fsstore"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
)

// testServerWithJWT constructs a Server with the JWT validator wired to
// a fresh in-memory keystore. Mirrors the slice 197 `IssueTestJWT`
// lazy-wire path so the unit test exercises the same Signer instance the
// handler reads.
func testServerWithJWT(t *testing.T, issuer string) (*Server, *tokensign.Signer) {
	t.Helper()
	ks, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	signer := tokensign.New(ks)
	srv := New(Config{})
	srv.AttachJWTValidator(signer, nil, issuer, issuer)
	return srv, signer
}

// TestIssueTestJWT_SuccessRoundTrip — slice 201 ISC-4, ISC-5, ISC-6,
// ISC-7. Success path: with ATLAS_TEST_MODE=1 set and a wired signer,
// the handler returns 200 + a token that round-trips through the same
// signer's Verify with matching iss/aud + SuperAdmin + Subject claim
// shape correct.
func TestIssueTestJWT_SuccessRoundTrip(t *testing.T) {
	t.Setenv("ATLAS_TEST_MODE", "1")

	const issuer = "https://atlas.test"
	srv, signer := testServerWithJWT(t, issuer)

	tenant := uuid.New()
	userID := uuid.New()
	body := map[string]any{
		"tenant_id":   tenant.String(),
		"user_id":     userID.String(),
		"roles":       []string{"admin", "grc_engineer"},
		"super_admin": true,
	}
	raw, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/v1/test/issue-jwt", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.handleIssueTestJWT(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200. body = %q", rec.Code, rec.Body.String())
	}
	var resp struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Token == "" {
		t.Fatal("token field empty")
	}

	// Round-trip: the same signer the Server is wired with must Verify
	// the token. This is the P0-201-4 anchor — the handler reuses the
	// slice 187 keystore + signer, never a parallel test-only surface.
	claims, err := signer.Verify(context.Background(), resp.Token)
	if err != nil {
		t.Fatalf("signer.Verify: %v", err)
	}
	if claims.Issuer != issuer {
		t.Errorf("Issuer = %q; want %q", claims.Issuer, issuer)
	}
	if len(claims.Audience) == 0 || claims.Audience[0] != issuer {
		t.Errorf("Audience = %v; want [%q]", claims.Audience, issuer)
	}
	if !claims.SuperAdmin {
		t.Error("SuperAdmin = false; want true (admin claim shape)")
	}
	if claims.Subject != userID.String() {
		t.Errorf("Subject = %q; want %q", claims.Subject, userID.String())
	}
	if claims.CurrentTenantID != tenant {
		t.Errorf("CurrentTenantID = %v; want %v", claims.CurrentTenantID, tenant)
	}
	if got := claims.Roles[tenant]; len(got) != 2 || got[0] != "admin" || got[1] != "grc_engineer" {
		t.Errorf("Roles[tenant] = %v; want [admin grc_engineer]", got)
	}
	// ISC-6: 1h expiry. ExpiresAt - IssuedAt should equal ~3600 seconds.
	if delta := claims.ExpiresAt - claims.IssuedAt; delta < 3590 || delta > 3610 {
		t.Errorf("exp - iat = %d; want ~3600 (1h)", delta)
	}
}

// TestIssueTestJWT_EnvUnset_404 — slice 201 ISC-2 + ISC-A2 + P0-201-2.
// Without ATLAS_TEST_MODE the handler refuses with 404 — the same
// response shape an unmounted route would produce. The 404 is the
// production-safety anchor.
func TestIssueTestJWT_EnvUnset_404(t *testing.T) {
	// Intentionally do not set ATLAS_TEST_MODE. t.Setenv unsets at
	// cleanup, but to make the test independent of any process-level
	// env we explicitly clear here too.
	t.Setenv("ATLAS_TEST_MODE", "")

	const issuer = "https://atlas.test"
	srv, _ := testServerWithJWT(t, issuer)

	req := httptest.NewRequest(http.MethodPost, "/v1/test/issue-jwt",
		bytes.NewReader([]byte(`{"tenant_id":"00000000-0000-0000-0000-000000000001"}`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.handleIssueTestJWT(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404. body = %q", rec.Code, rec.Body.String())
	}
}

// TestIssueTestJWT_SignerNil_404 — slice 201 ISC-3 + ISC-8. With env set
// but no signer wired, the handler refuses with 404. A missing signer
// indicates a misconfigured deployment (no ATLAS_ISSUER_URL); responding
// 404 keeps the surface uniform with "no route present".
func TestIssueTestJWT_SignerNil_404(t *testing.T) {
	t.Setenv("ATLAS_TEST_MODE", "1")

	// Construct a Server without AttachJWTValidator.
	srv := New(Config{})

	req := httptest.NewRequest(http.MethodPost, "/v1/test/issue-jwt",
		bytes.NewReader([]byte(`{"tenant_id":"00000000-0000-0000-0000-000000000001"}`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.handleIssueTestJWT(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404. body = %q", rec.Code, rec.Body.String())
	}
}

// TestIssueTestJWT_RoundTripValidatesAgainstStandardParams — extra
// belt-and-suspenders: the token validates through the standard
// jwt.Validate path with the same issuer + audience the middleware uses.
// Catches regressions where the handler stamps mismatched iss/aud.
func TestIssueTestJWT_RoundTripValidatesAgainstStandardParams(t *testing.T) {
	t.Setenv("ATLAS_TEST_MODE", "1")

	const issuer = "https://atlas.test"
	srv, signer := testServerWithJWT(t, issuer)

	tenant := uuid.New()
	body := map[string]any{
		"tenant_id":   tenant.String(),
		"super_admin": true,
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/test/issue-jwt", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	srv.handleIssueTestJWT(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %q", rec.Code, rec.Body.String())
	}
	var resp struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&resp)

	verified, err := signer.Verify(context.Background(), resp.Token)
	if err != nil {
		t.Fatalf("signer.Verify: %v", err)
	}
	if err := jwt.Validate(verified, jwt.ValidationParams{
		ExpectedIssuer:   issuer,
		ExpectedAudience: issuer,
	}); err != nil {
		t.Fatalf("jwt.Validate: %v", err)
	}
}

// TestIssueTestJWT_DefaultsApplied — when the caller supplies only the
// minimum required field (tenant_id), the handler applies sensible
// defaults: user_id falls back to a synthetic "test-admin:<tenant>"
// subject, roles default to ["admin"], super_admin defaults true. This
// keeps the Playwright global-setup body minimal.
func TestIssueTestJWT_DefaultsApplied(t *testing.T) {
	t.Setenv("ATLAS_TEST_MODE", "1")

	const issuer = "https://atlas.test"
	srv, signer := testServerWithJWT(t, issuer)

	tenant := uuid.New()
	body := map[string]any{
		"tenant_id": tenant.String(),
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/test/issue-jwt", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	srv.handleIssueTestJWT(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %q", rec.Code, rec.Body.String())
	}
	var resp struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	claims, err := signer.Verify(context.Background(), resp.Token)
	if err != nil {
		t.Fatalf("signer.Verify: %v", err)
	}
	if claims.Subject == "" {
		t.Error("Subject = empty; want non-empty default")
	}
	if !claims.SuperAdmin {
		t.Error("SuperAdmin defaulted false; want true")
	}
}

// TestIssueTestJWT_MissingTenantID_400 — the handler requires tenant_id;
// a missing or invalid tenant_id is a client error.
func TestIssueTestJWT_MissingTenantID_400(t *testing.T) {
	t.Setenv("ATLAS_TEST_MODE", "1")

	const issuer = "https://atlas.test"
	srv, _ := testServerWithJWT(t, issuer)

	req := httptest.NewRequest(http.MethodPost, "/v1/test/issue-jwt",
		bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.handleIssueTestJWT(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400. body = %q", rec.Code, rec.Body.String())
	}
}
