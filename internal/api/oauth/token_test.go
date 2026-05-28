package oauth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/oauth"
	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/keystore/fsstore"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
)

// pinnedNow returns a deterministic clock used across the token-handler
// unit tests so iat / exp claim values are predictable.
func pinnedNow() time.Time {
	return time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
}

// newTestServer builds a Handler + signer + a route-mounted httptest
// server. The TokenEndpoint is wired with no AuditPool — the audit
// write is a no-op (the tested path is the token issuance + claim
// shape, not the audit log; that's covered in the integration test).
//
// `clientsStub` is nil here because client_credentials path is
// covered in the integration test (it needs a real DB). The unit
// tests exercise paths that do NOT go through oauthclient.Verify:
// content-type rejection, missing grant_type, missing form fields,
// invalid grant type, and the token-exchange path which only needs
// the signer.
func newTokenTestServer(t *testing.T) (*httptest.Server, *tokensign.Signer, *oauth.TokenEndpoint) {
	t.Helper()
	ks, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	signer := tokensign.New(ks)
	ep := oauth.NewTokenEndpoint(signer, nil, oauth.TokenEndpointConfig{
		Issuer:        testIssuer,
		RatePerMinute: 60,
		Now:           pinnedNow,
	})
	h := oauth.New(ks, oauth.Config{Issuer: testIssuer})
	h.AttachTokenEndpoint(ep)
	srv := httptest.NewServer(routerFor(h))
	t.Cleanup(srv.Close)
	return srv, signer, ep
}

// routerFor is the small wrapper that mounts the Handler at root.
func routerFor(h *oauth.Handler) http.Handler {
	r := newRouter(h)
	return r
}

func postForm(t *testing.T, srvURL string, form url.Values) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srvURL+oauth.PathToken, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	body := make([]byte, 4096)
	n, _ := resp.Body.Read(body)
	return resp, body[:n]
}

// TestTokenEndpoint_RejectsNonFormContentType covers AC-4: any
// content-type other than application/x-www-form-urlencoded MUST be
// rejected with 400 + invalid_request.
func TestTokenEndpoint_RejectsNonFormContentType(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTokenTestServer(t)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+oauth.PathToken, strings.NewReader(`{"foo":"bar"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["error"] != "invalid_request" {
		t.Errorf("error = %q, want invalid_request", body["error"])
	}
}

// TestTokenEndpoint_DispatchTable covers AC-5: missing grant_type
// returns invalid_request; unknown grant_type returns
// unsupported_grant_type.
func TestTokenEndpoint_DispatchTable(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTokenTestServer(t)

	cases := []struct {
		name      string
		grantType string
		wantCode  string
	}{
		{"missing", "", "invalid_request"},
		{"bogus", "password", "unsupported_grant_type"},
		{"refresh", "refresh_token", "unsupported_grant_type"}, // explicitly out of scope per P0-188-6
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			form := url.Values{}
			if c.grantType != "" {
				form.Set("grant_type", c.grantType)
			}
			resp, body := postForm(t, srv.URL, form)
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", resp.StatusCode)
			}
			if !strings.Contains(string(body), c.wantCode) {
				t.Errorf("body = %q, want error=%s", body, c.wantCode)
			}
		})
	}
}

// TestTokenEndpoint_TokenExchange_HappyPath covers AC-10/AC-11/AC-12/
// AC-14: caller presents a subject_token whose available_tenants
// contains the target; the AS mints a new JWT with the swapped
// current_tenant_id and the same super_admin / roles.
func TestTokenEndpoint_TokenExchange_HappyPath(t *testing.T) {
	t.Parallel()
	srv, signer, _ := newTokenTestServer(t)

	tenantA := uuid.New()
	tenantB := uuid.New()
	roles := map[uuid.UUID][]string{
		tenantA: {"admin"},
		tenantB: {"reader"},
	}
	subjectTok, err := signer.Sign(context.Background(), jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    testIssuer,
			Subject:   "user:vciso",
			Audience:  []string{testIssuer},
			ExpiresAt: pinnedNow().Add(time.Hour).Unix(),
			IssuedAt:  pinnedNow().Unix(),
			ID:        "jti-subject-1",
		},
		IDPIssuer:        "https://idp.example.test",
		CurrentTenantID:  tenantA,
		AvailableTenants: []uuid.UUID{tenantA, tenantB},
		Roles:            roles,
		SuperAdmin:       false,
	})
	if err != nil {
		t.Fatalf("Sign subject: %v", err)
	}

	form := url.Values{}
	form.Set("grant_type", oauth.GrantTypeTokenExchange)
	form.Set("subject_token", subjectTok)
	form.Set("subject_token_type", oauth.SubjectTokenTypeJWT)
	form.Set("atlas:target_tenant_id", tenantB.String())

	resp, body := postForm(t, srv.URL, form)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	var out struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.TokenType != "Bearer" {
		t.Errorf("token_type = %q, want Bearer", out.TokenType)
	}
	if out.ExpiresIn != 3600 {
		t.Errorf("expires_in = %d, want 3600", out.ExpiresIn)
	}

	claims, err := signer.Verify(context.Background(), out.AccessToken)
	if err != nil {
		t.Fatalf("verify minted: %v", err)
	}
	if claims.CurrentTenantID != tenantB {
		t.Errorf("current_tenant_id = %v, want %v", claims.CurrentTenantID, tenantB)
	}
	if !containsTenant(claims.AvailableTenants, tenantA) || !containsTenant(claims.AvailableTenants, tenantB) {
		t.Errorf("available_tenants = %v, want both A+B", claims.AvailableTenants)
	}
	if claims.Subject != "user:vciso" {
		t.Errorf("sub = %q, want preserved", claims.Subject)
	}
	if claims.IDPIssuer != "https://idp.example.test" {
		t.Errorf("idp_issuer = %q, want preserved", claims.IDPIssuer)
	}
	if claims.SuperAdmin {
		t.Errorf("super_admin minted true; subject was false (P0-188-4)")
	}
}

// TestTokenEndpoint_TokenExchange_RejectsTenantNotInAllowlist covers
// AC-12 + AC-13: when the target tenant is NOT in the
// subject_token's available_tenants AND super_admin is false, the
// exchange MUST be rejected with 403 + invalid_target.
func TestTokenEndpoint_TokenExchange_RejectsTenantNotInAllowlist(t *testing.T) {
	t.Parallel()
	srv, signer, _ := newTokenTestServer(t)

	tenantA := uuid.New()
	tenantB := uuid.New() // not in available_tenants
	subjectTok, err := signer.Sign(context.Background(), jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    testIssuer,
			Subject:   "user:vciso",
			Audience:  []string{testIssuer},
			ExpiresAt: pinnedNow().Add(time.Hour).Unix(),
			IssuedAt:  pinnedNow().Unix(),
			ID:        "jti-subject-2",
		},
		CurrentTenantID:  tenantA,
		AvailableTenants: []uuid.UUID{tenantA},
		SuperAdmin:       false,
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	form := url.Values{}
	form.Set("grant_type", oauth.GrantTypeTokenExchange)
	form.Set("subject_token", subjectTok)
	form.Set("subject_token_type", oauth.SubjectTokenTypeJWT)
	form.Set("atlas:target_tenant_id", tenantB.String())

	resp, body := postForm(t, srv.URL, form)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s; want 403", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "invalid_target") {
		t.Errorf("body = %q, want invalid_target", body)
	}
}

// TestTokenEndpoint_TokenExchange_SuperAdminCanCross covers the
// super_admin escape hatch in AC-12: even when the target is NOT in
// available_tenants, a subject_token with super_admin=true succeeds.
func TestTokenEndpoint_TokenExchange_SuperAdminCanCross(t *testing.T) {
	t.Parallel()
	srv, signer, _ := newTokenTestServer(t)

	tenantA := uuid.New()
	tenantB := uuid.New()
	subjectTok, err := signer.Sign(context.Background(), jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    testIssuer,
			Subject:   "user:platform-admin",
			Audience:  []string{testIssuer},
			ExpiresAt: pinnedNow().Add(time.Hour).Unix(),
			IssuedAt:  pinnedNow().Unix(),
			ID:        "jti-subject-3",
		},
		CurrentTenantID:  tenantA,
		AvailableTenants: []uuid.UUID{tenantA},
		SuperAdmin:       true,
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	form := url.Values{}
	form.Set("grant_type", oauth.GrantTypeTokenExchange)
	form.Set("subject_token", subjectTok)
	form.Set("subject_token_type", oauth.SubjectTokenTypeJWT)
	form.Set("atlas:target_tenant_id", tenantB.String())

	resp, body := postForm(t, srv.URL, form)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s; want 200", resp.StatusCode, body)
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	claims, err := signer.Verify(context.Background(), out.AccessToken)
	if err != nil {
		t.Fatalf("verify minted: %v", err)
	}
	if claims.CurrentTenantID != tenantB {
		t.Errorf("current_tenant_id = %v, want %v", claims.CurrentTenantID, tenantB)
	}
	if !claims.SuperAdmin {
		t.Errorf("super_admin lost in the exchange; subject was true")
	}
}

// TestTokenEndpoint_TokenExchange_NeverElevatesSuperAdmin covers
// AC-15 + P0-188-4: a subject_token with super_admin=false MUST
// produce a minted token with super_admin=false even when the
// target tenant is fully allowed.
func TestTokenEndpoint_TokenExchange_NeverElevatesSuperAdmin(t *testing.T) {
	t.Parallel()
	srv, signer, _ := newTokenTestServer(t)

	tenantA := uuid.New()
	tenantB := uuid.New()
	subjectTok, err := signer.Sign(context.Background(), jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    testIssuer,
			Subject:   "user:non-admin",
			Audience:  []string{testIssuer},
			ExpiresAt: pinnedNow().Add(time.Hour).Unix(),
			IssuedAt:  pinnedNow().Unix(),
			ID:        "jti-subject-4",
		},
		CurrentTenantID:  tenantA,
		AvailableTenants: []uuid.UUID{tenantA, tenantB},
		SuperAdmin:       false,
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	form := url.Values{}
	form.Set("grant_type", oauth.GrantTypeTokenExchange)
	form.Set("subject_token", subjectTok)
	form.Set("subject_token_type", oauth.SubjectTokenTypeJWT)
	form.Set("atlas:target_tenant_id", tenantB.String())

	resp, body := postForm(t, srv.URL, form)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	claims, err := signer.Verify(context.Background(), out.AccessToken)
	if err != nil {
		t.Fatalf("verify minted: %v", err)
	}
	if claims.SuperAdmin {
		t.Fatal("minted super_admin true from non-admin subject — P0-188-4 violation")
	}
}

// TestTokenEndpoint_TokenExchange_RejectsBadSignature covers AC-11
// + P0-188-5: a subject_token whose signature does not verify
// against the local keystore MUST be rejected with 401 +
// invalid_token, AND the rejection MUST happen before any claim
// (including available_tenants) is read.
func TestTokenEndpoint_TokenExchange_RejectsBadSignature(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTokenTestServer(t)

	// Mint a token under a DIFFERENT keystore — the test server's
	// signer will fail to verify it.
	otherKS, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	otherSigner := tokensign.New(otherKS)
	tenantTarget := uuid.New()
	foreignTok, err := otherSigner.Sign(context.Background(), jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    testIssuer,
			Subject:   "user:attacker",
			Audience:  []string{testIssuer},
			ExpiresAt: pinnedNow().Add(time.Hour).Unix(),
			IssuedAt:  pinnedNow().Unix(),
			ID:        "jti-foreign",
		},
		AvailableTenants: []uuid.UUID{tenantTarget}, // attacker-supplied; must NOT be honored
		SuperAdmin:       true,                      // attacker-supplied; MUST NOT be honored
	})
	if err != nil {
		t.Fatalf("foreign Sign: %v", err)
	}

	form := url.Values{}
	form.Set("grant_type", oauth.GrantTypeTokenExchange)
	form.Set("subject_token", foreignTok)
	form.Set("subject_token_type", oauth.SubjectTokenTypeJWT)
	form.Set("atlas:target_tenant_id", tenantTarget.String())

	resp, body := postForm(t, srv.URL, form)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s; want 401", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "invalid_token") {
		t.Errorf("body = %q, want invalid_token", body)
	}
}

// TestTokenEndpoint_TokenExchange_RejectsBadTargetUUID covers a
// shape check: a non-UUID target tenant id must 400 with
// invalid_request, not 403.
func TestTokenEndpoint_TokenExchange_RejectsBadTargetUUID(t *testing.T) {
	t.Parallel()
	srv, signer, _ := newTokenTestServer(t)
	tok, err := signer.Sign(context.Background(), jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: testIssuer, Subject: "user:x", Audience: []string{testIssuer},
			ExpiresAt: pinnedNow().Add(time.Hour).Unix(), IssuedAt: pinnedNow().Unix(),
		},
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	form := url.Values{}
	form.Set("grant_type", oauth.GrantTypeTokenExchange)
	form.Set("subject_token", tok)
	form.Set("subject_token_type", oauth.SubjectTokenTypeJWT)
	form.Set("atlas:target_tenant_id", "not-a-uuid")
	resp, body := postForm(t, srv.URL, form)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s; want 400", resp.StatusCode, body)
	}
}

// TestTokenEndpoint_DiscoveryAdvertisesGrantsAfterAttach covers
// AC-18: after AttachTokenEndpoint, the discovery document MUST
// advertise both grant types.
func TestTokenEndpoint_DiscoveryAdvertisesGrantsAfterAttach(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTokenTestServer(t)

	resp, err := http.Get(srv.URL + oauth.PathDiscovery)
	if err != nil {
		t.Fatalf("GET discovery: %v", err)
	}
	defer resp.Body.Close()
	var doc map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	grants, _ := doc["grant_types_supported"].([]any)
	if len(grants) != 2 {
		t.Fatalf("grant_types_supported = %v, want exactly 2", grants)
	}
	gotCC, gotTE := false, false
	for _, g := range grants {
		s, _ := g.(string)
		if s == oauth.GrantTypeClientCredentials {
			gotCC = true
		}
		if s == oauth.GrantTypeTokenExchange {
			gotTE = true
		}
	}
	if !gotCC || !gotTE {
		t.Errorf("discovery grants missing client_credentials or token-exchange: %v", grants)
	}
	// token_endpoint_auth_methods_supported should be ["client_secret_post"]
	methods, _ := doc["token_endpoint_auth_methods_supported"].([]any)
	if len(methods) != 1 || methods[0] != "client_secret_post" {
		t.Errorf("auth methods = %v, want [client_secret_post]", methods)
	}
}

// TestTokenEndpoint_RateLimit covers AC-9: with a rate of 2/min, the
// third request from the same client_id MUST be rate-limited with
// 429 + Retry-After.
//
// Note: this test exercises the rate limiter without involving the
// DB-backed oauthclient.Verify path because the unit test has no
// real client to authenticate. The limiter is keyed on the form's
// client_id whether or not the verify succeeds — that's the
// P0-188-9 invariant. The test does NOT pass through to a real
// 401 from the verifier because clients is nil in the unit harness;
// the rate limit fires first.
//
// We use a custom-rate harness, not the shared newTokenTestServer.
func TestTokenEndpoint_RateLimit(t *testing.T) {
	t.Parallel()
	ks, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	signer := tokensign.New(ks)

	// Build a tiny in-process clock that doesn't advance — the
	// limiter cannot refill between calls.
	frozen := pinnedNow()
	clock := func() time.Time { return frozen }
	ep := oauth.NewTokenEndpoint(signer, nil, oauth.TokenEndpointConfig{
		Issuer:        testIssuer,
		RatePerMinute: 2,
		Now:           clock,
	})
	h := oauth.New(ks, oauth.Config{Issuer: testIssuer})
	h.AttachTokenEndpoint(ep)
	srv := httptest.NewServer(routerFor(h))
	defer srv.Close()

	form := url.Values{}
	form.Set("grant_type", oauth.GrantTypeClientCredentials)
	form.Set("client_id", "test-rate-limited-client")
	form.Set("client_secret", "bogus") // verify will fail, but limiter runs first

	// Calls 1 + 2: limiter ALLOWS; oauthclient.Verify returns 401.
	for i := 0; i < 2; i++ {
		resp, _ := postForm(t, srv.URL, form)
		// nil clients -> oauthclient.Verify panics on a nil dereference.
		// To avoid that path, we cap this test at observing only the
		// limiter behaviour: if the limiter trips at call 3, we know
		// the limiter ran for calls 1 + 2 (those returned non-429).
		if resp.StatusCode == http.StatusTooManyRequests && i < 2 {
			t.Fatalf("rate limit fired too early on call %d", i+1)
		}
	}
	// Call 3: limiter REJECTS; 429 + Retry-After.
	resp, body := postForm(t, srv.URL, form)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("call 3 status = %d body = %s; want 429", resp.StatusCode, body)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Errorf("missing Retry-After header on 429")
	}
}

// containsTenant is a tiny helper that does not require pulling the
// internal slice-search helper across the test package boundary.
func containsTenant(list []uuid.UUID, target uuid.UUID) bool {
	for _, u := range list {
		if u == target {
			return true
		}
	}
	return false
}
