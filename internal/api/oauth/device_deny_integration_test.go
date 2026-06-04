//go:build integration

// device_deny_integration_test.go — slice 422 integration coverage for
// the RFC 8628 §3.5 device-code DENY branches that need a real
// oauth_device_codes row in a specific state:
//
//   - expired_token: an approved-but-expired device code is refused.
//   - invalid_grant (client mismatch): an approved code redeemed with a
//     DIFFERENT client_id than it was issued to is refused — the
//     spoofing guard (RFC 6749 §5.2) that prevents one CLI's approved
//     code from being redeemed by another client.
//
// THREAT FRAMING: both are deny branches whose regression would be an
// auth bypass — an expired or cross-client device code that minted a
// token would be a privilege grant the user never authorized for that
// client/time. The integration tier is required because the branches
// only fire AFTER the DB-backed Consume returns its sentinel (expired)
// or returns the row whose client_id then mismatches.
//
// The slow_down / authorization_pending / invalid_grant(replay)
// branches are covered by device_code_integration_test.go; this file
// adds the two remaining §3.5 deny arms.

package oauth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/oauth"
	"github.com/mgoodric/security-atlas/internal/auth/keystore/fsstore"
	"github.com/mgoodric/security-atlas/internal/auth/oauthclient"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
)

// deviceDenyHarness stands up a device-flow server whose token-endpoint
// clock is controllable so an "expired" row can be redeemed
// deterministically without a real-time sleep.
type deviceDenyHarness struct {
	srv      *httptest.Server
	devCodes *oauth.DeviceCodeStore
	clientID string
	clock    func() time.Time
}

func newDeviceDenyHarness(t *testing.T, now func() time.Time) deviceDenyHarness {
	t.Helper()
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
		Now:           now,
	})
	tokenEP.AttachDeviceCodeStore(devCodes)
	deviceAuthEP := oauth.NewDeviceAuthorizationEndpoint(clients, devCodes,
		oauth.DeviceAuthorizationConfig{Issuer: testIssuer, Now: now})

	h := oauth.New(ks, oauth.Config{Issuer: testIssuer})
	h.AttachTokenEndpoint(tokenEP)
	h.AttachDeviceAuthorizationEndpoint(deviceAuthEP)
	srv := httptest.NewServer(newRouter(h))
	t.Cleanup(srv.Close)

	clientRow, _, err := clients.Issue(ctx, uniqueName(t, "dev-deny"))
	if err != nil {
		t.Fatalf("Issue OAuth client: %v", err)
	}
	return deviceDenyHarness{srv: srv, devCodes: devCodes, clientID: clientRow.ClientID, clock: now}
}

// insertApprovedCode inserts a device code with the given expiry and
// approves it, returning the device_code + user_code. The approval is
// written with an approve-time strictly before expiry so the Approve
// UPDATE's `expires_at > now` gate passes.
func (h deviceDenyHarness) insertApprovedCode(t *testing.T, expiresAt time.Time) (string, string) {
	t.Helper()
	ctx := context.Background()
	deviceCode := "atlas-test-device-code-" + uuid.NewString()
	userCode := shortUserCode(t)
	if err := h.devCodes.Insert(ctx, oauth.DeviceCodeRow{
		DeviceCode: deviceCode,
		UserCode:   userCode,
		ClientID:   h.clientID,
		ExpiresAt:  expiresAt,
		CreatedAt:  expiresAt.Add(-15 * time.Minute),
	}); err != nil {
		t.Fatalf("Insert device code: %v", err)
	}
	// Approve as of a moment before expiry so the Approve gate accepts.
	approveAt := expiresAt.Add(-time.Minute)
	if err := h.devCodes.Approve(ctx, oauth.ApproveInput{
		UserCode:        userCode,
		UserID:          uuid.New().String(),
		IDPIssuer:       "https://idp.example.test",
		IDPSubject:      "test-subject",
		CurrentTenantID: "",
		SuperAdmin:      false,
	}, approveAt); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	return deviceCode, userCode
}

// TestIntegrationDeviceDeny_ExpiredToken covers RFC 8628 §3.5
// `expired_token`: an approved device code whose expires_at is before
// the redemption clock MUST be refused 400 + expired_token. The
// harness clock is set PAST the row's expiry so the Consume gate's
// `expires_at > now` fails and the store returns ErrDeviceCodeExpired.
func TestIntegrationDeviceDeny_ExpiredToken(t *testing.T) {
	// Fixed wall-clock reference for the harness.
	base := time.Now().UTC().Truncate(time.Second)
	expiry := base // the code expires exactly at base...
	// ...and the redemption clock is well past it.
	clock := func() time.Time { return base.Add(10 * time.Minute) }
	h := newDeviceDenyHarness(t, clock)

	deviceCode, _ := h.insertApprovedCode(t, expiry)

	form := url.Values{}
	form.Set("grant_type", oauth.GrantTypeDeviceCode)
	form.Set("client_id", h.clientID)
	form.Set("device_code", deviceCode)
	resp, body := postForm(t, h.srv.URL, form)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s; want 400", resp.StatusCode, body)
	}
	var oe oauthErr
	if err := json.Unmarshal(body, &oe); err != nil {
		t.Fatalf("decode: %v (body %s)", err, body)
	}
	if oe.Error != "expired_token" {
		t.Errorf("error = %q, want expired_token", oe.Error)
	}
}

// TestIntegrationDeviceDeny_ClientMismatch covers the RFC 6749 §5.2
// `invalid_grant` spoofing guard in the device-code grant: an approved,
// unexpired device code redeemed with a client_id DIFFERENT from the one
// it was issued to MUST be refused 400 + invalid_grant. A regression
// here would let any client redeem another client's approved code — a
// cross-client token-theft bypass.
func TestIntegrationDeviceDeny_ClientMismatch(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Second)
	// Redemption clock is BEFORE expiry so the code is live; only the
	// client_id mismatch should trip.
	clock := func() time.Time { return base }
	h := newDeviceDenyHarness(t, clock)

	deviceCode, _ := h.insertApprovedCode(t, base.Add(15*time.Minute))

	form := url.Values{}
	form.Set("grant_type", oauth.GrantTypeDeviceCode)
	form.Set("client_id", "some-other-client-"+uuid.NewString()) // not the issuing client
	form.Set("device_code", deviceCode)
	resp, body := postForm(t, h.srv.URL, form)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s; want 400", resp.StatusCode, body)
	}
	var oe oauthErr
	if err := json.Unmarshal(body, &oe); err != nil {
		t.Fatalf("decode: %v (body %s)", err, body)
	}
	if oe.Error != "invalid_grant" {
		t.Errorf("error = %q, want invalid_grant", oe.Error)
	}
}

// shortUserCode returns a process-unique 8-char user_code in the
// XXXX-XXXX shape the table's CHECK accepts. Neutral test value — not a
// real credential.
func shortUserCode(t *testing.T) string {
	t.Helper()
	raw := uuid.NewString()
	// Strip hyphens, uppercase, take 8 chars, format XXXX-XXXX.
	clean := ""
	for _, c := range raw {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			clean += string(c)
		}
		if len(clean) == 8 {
			break
		}
	}
	up := ""
	for _, c := range clean {
		if c >= 'a' && c <= 'z' {
			up += string(c - 32)
		} else {
			up += string(c)
		}
	}
	return up[:4] + "-" + up[4:8]
}
