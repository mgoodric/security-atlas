//go:build integration

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
	"github.com/mgoodric/security-atlas/internal/auth/keystore/fsstore"
	"github.com/mgoodric/security-atlas/internal/auth/oauthclient"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
)

// TestIntegrationDeviceFlow_EndToEnd exercises the full RFC 8628
// flow:
//
//  1. POST /oauth/device_authorization                     -> 200
//  2. POST /oauth/token grant_type=device_code (pending)    -> 400 authorization_pending
//  3. (test harness writes approval snapshot directly to DB)
//  4. POST /oauth/token grant_type=device_code (approved)   -> 200 + JWT
//  5. Re-POST same device_code (replay)                     -> 400 invalid_grant
//
// The poll throttle (RFC 8628 §3.5 `slow_down`) is exercised in
// TestIntegrationDeviceFlow_SlowDown below.
func TestIntegrationDeviceFlow_EndToEnd(t *testing.T) {
	pool := openTokenIntegrationPool(t)
	ctx := context.Background()

	// Stand up a complete OAuth server: keystore + signer + clients
	// store + device-code store + token endpoint + device-auth
	// endpoint, mounted onto a chi router via the public Handler.
	ks, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	signer := tokensign.New(ks)
	clients := oauthclient.New(pool)
	devCodes := oauth.NewDeviceCodeStore(pool)

	tokenEP := oauth.NewTokenEndpoint(signer, clients, oauth.TokenEndpointConfig{
		Issuer:        testIssuer,
		AuditPool:     pool,
		RatePerMinute: 600,
	})
	tokenEP.AttachDeviceCodeStore(devCodes)

	deviceAuthEP := oauth.NewDeviceAuthorizationEndpoint(clients, devCodes, oauth.DeviceAuthorizationConfig{
		Issuer: testIssuer,
	})

	h := oauth.New(ks, oauth.Config{Issuer: testIssuer})
	h.AttachTokenEndpoint(tokenEP)
	h.AttachDeviceAuthorizationEndpoint(deviceAuthEP)

	r := newRouter(h)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	// Issue an OAuth client to act as the CLI client_id.
	clientRow, _, err := clients.Issue(ctx, uniqueName(t, "dev-flow"))
	if err != nil {
		t.Fatalf("Issue OAuth client: %v", err)
	}

	// Step 1: initiate.
	authForm := url.Values{}
	authForm.Set("client_id", clientRow.ClientID)
	authResp, authBody := postForm(t, srv.URL+"/oauth/device_authorization", authForm)
	if authResp.StatusCode != http.StatusOK {
		t.Fatalf("device_authorization status = %d, body=%s", authResp.StatusCode, string(authBody))
	}
	var auth deviceAuthorizationResp
	if err := json.Unmarshal(authBody, &auth); err != nil {
		t.Fatalf("parse device auth: %v", err)
	}
	if auth.DeviceCode == "" || auth.UserCode == "" {
		t.Fatal("device_authorization missing device_code or user_code")
	}
	if !strings.Contains(auth.UserCode, "-") {
		t.Errorf("user_code %q is missing hyphen", auth.UserCode)
	}
	if auth.Interval != oauth.DevicePollInterval {
		t.Errorf("interval = %d, want %d", auth.Interval, oauth.DevicePollInterval)
	}

	// Step 2: poll while pending.
	pollForm := url.Values{}
	pollForm.Set("grant_type", oauth.GrantTypeDeviceCode)
	pollForm.Set("client_id", clientRow.ClientID)
	pollForm.Set("device_code", auth.DeviceCode)
	pollResp1, pollBody1 := postForm(t, srv.URL+"/oauth/token", pollForm)
	if pollResp1.StatusCode != http.StatusBadRequest {
		t.Fatalf("pending poll status = %d (body %s), want 400", pollResp1.StatusCode, string(pollBody1))
	}
	var pendingErr oauthErr
	if err := json.Unmarshal(pollBody1, &pendingErr); err != nil {
		t.Fatalf("parse pending: %v", err)
	}
	if pendingErr.Error != "authorization_pending" {
		t.Errorf("pending error = %q, want authorization_pending", pendingErr.Error)
	}

	// Step 3: simulate approval by writing the snapshot directly.
	approveAt := time.Now()
	if err := devCodes.Approve(ctx, oauth.ApproveInput{
		UserCode:        auth.UserCode,
		UserID:          uuid.New().String(),
		IDPIssuer:       "https://idp.example.test",
		IDPSubject:      "test-subject",
		CurrentTenantID: "",
		SuperAdmin:      true,
	}, approveAt); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	// Wait at least 5s before the next poll so we don't trip the
	// per-device_code slow_down (P0-191-8 / AC-21). Sleep is the
	// natural fit here because the slow_down tracker uses
	// time.Now internally.
	time.Sleep(6 * time.Second)

	// Step 4: poll for redemption.
	pollResp2, pollBody2 := postForm(t, srv.URL+"/oauth/token", pollForm)
	if pollResp2.StatusCode != http.StatusOK {
		t.Fatalf("approved poll status = %d (body %s), want 200", pollResp2.StatusCode, string(pollBody2))
	}
	var tokResp tokenResp
	if err := json.Unmarshal(pollBody2, &tokResp); err != nil {
		t.Fatalf("parse token: %v", err)
	}
	if tokResp.AccessToken == "" {
		t.Fatal("redemption returned empty access_token")
	}

	// Step 5: replay - same device_code can NOT be redeemed again
	// (P0-191-8). Wait for slow_down window again.
	time.Sleep(6 * time.Second)
	pollResp3, pollBody3 := postForm(t, srv.URL+"/oauth/token", pollForm)
	if pollResp3.StatusCode != http.StatusBadRequest {
		t.Fatalf("replay status = %d (body %s), want 400", pollResp3.StatusCode, string(pollBody3))
	}
	var replayErr oauthErr
	if err := json.Unmarshal(pollBody3, &replayErr); err != nil {
		t.Fatalf("parse replay: %v", err)
	}
	if replayErr.Error != "invalid_grant" {
		t.Errorf("replay error = %q, want invalid_grant", replayErr.Error)
	}
}

// TestIntegrationDeviceFlow_SlowDown exercises the per-device_code
// 5-second poll floor — the second poll inside the window returns
// RFC 8628 §3.5 `slow_down`.
func TestIntegrationDeviceFlow_SlowDown(t *testing.T) {
	pool := openTokenIntegrationPool(t)
	ctx := context.Background()
	ks, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	signer := tokensign.New(ks)
	clients := oauthclient.New(pool)
	devCodes := oauth.NewDeviceCodeStore(pool)
	tokenEP := oauth.NewTokenEndpoint(signer, clients, oauth.TokenEndpointConfig{
		Issuer:        testIssuer,
		AuditPool:     pool,
		RatePerMinute: 600,
	})
	tokenEP.AttachDeviceCodeStore(devCodes)
	deviceAuthEP := oauth.NewDeviceAuthorizationEndpoint(clients, devCodes, oauth.DeviceAuthorizationConfig{
		Issuer: testIssuer,
	})
	h := oauth.New(ks, oauth.Config{Issuer: testIssuer})
	h.AttachTokenEndpoint(tokenEP)
	h.AttachDeviceAuthorizationEndpoint(deviceAuthEP)
	r := newRouter(h)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	clientRow, _, err := clients.Issue(ctx, uniqueName(t, "slow-down"))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	authForm := url.Values{}
	authForm.Set("client_id", clientRow.ClientID)
	_, authBody := postForm(t, srv.URL+"/oauth/device_authorization", authForm)
	var auth deviceAuthorizationResp
	if err := json.Unmarshal(authBody, &auth); err != nil {
		t.Fatalf("parse auth: %v", err)
	}

	pollForm := url.Values{}
	pollForm.Set("grant_type", oauth.GrantTypeDeviceCode)
	pollForm.Set("client_id", clientRow.ClientID)
	pollForm.Set("device_code", auth.DeviceCode)

	// First poll: returns authorization_pending (allowed by tracker).
	r1, _ := postForm(t, srv.URL+"/oauth/token", pollForm)
	if r1.StatusCode != http.StatusBadRequest {
		t.Fatalf("first poll status = %d, want 400", r1.StatusCode)
	}

	// Second poll immediately: should trip slow_down (429).
	r2, body2 := postForm(t, srv.URL+"/oauth/token", pollForm)
	if r2.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("rapid poll status = %d, want 429 (body %s)", r2.StatusCode, string(body2))
	}
	var slow oauthErr
	if err := json.Unmarshal(body2, &slow); err != nil {
		t.Fatalf("parse slow_down: %v", err)
	}
	if slow.Error != "slow_down" {
		t.Errorf("error = %q, want slow_down", slow.Error)
	}
	if retryAfter := r2.Header.Get("Retry-After"); retryAfter != "5" {
		t.Errorf("Retry-After = %q, want 5", retryAfter)
	}
}

// Helper response shapes — local to the integration test.

type deviceAuthorizationResp struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

type tokenResp struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

type oauthErr struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}
