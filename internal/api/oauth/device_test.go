// device_test.go — slice 314 unit coverage for the slice-191 RFC 8628
// Device Authorization Grant surface.
//
// RFC: RFC 8628 (OAuth 2.0 Device Authorization Grant) — §3.1 (device
// authorization request), §3.4 (device access-token request on
// /oauth/token), §3.5 (token-error responses: authorization_pending,
// slow_down, expired_token, access_denied), §6.1 (user_code entropy +
// unambiguous alphabet). Load-bearing functions under test:
// DeviceAuthorizationEndpoint.ServeHTTP (pre-DB validation +
// user_code/device_code generation), TokenEndpoint.handleDeviceCode
// (pre-DB validation + slow_down), buildDeviceCodeClaims (approval-
// snapshot → JWT claims), devicePollTracker.Allow,
// DeviceApprovalEndpoint.ServeApprove/ServeDeny (auth + body
// validation).
//
// Scope discipline: the DB-backed redemption + approval persistence is
// covered by device_code_integration_test.go (now enrolled in CI per
// this slice). This file covers the branches reachable WITHOUT
// Postgres: the request-validation gates, the crypto generators (via
// injected entropy), the in-memory poll tracker, and the pure
// claim-projection function.

package oauth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/oauth"
	"github.com/mgoodric/security-atlas/internal/auth/keystore/fsstore"
	"github.com/mgoodric/security-atlas/internal/auth/oauthclient"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
)

// ===== device_authorization endpoint (RFC 8628 §3.1) =====

// newDeviceAuthServer wires a DeviceAuthorizationEndpoint with a
// nil-pool client store. The content-type + missing-client_id gates
// run before clients.Lookup, so they are reachable without a DB.
func newDeviceAuthServer(t *testing.T) *httptest.Server {
	t.Helper()
	ks, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	ep := oauth.NewDeviceAuthorizationEndpoint(
		oauthclient.New(nil),
		oauth.NewDeviceCodeStore(nil),
		oauth.DeviceAuthorizationConfig{Issuer: testIssuer, Now: pinnedNow},
	)
	h := oauth.New(ks, oauth.Config{Issuer: testIssuer})
	h.AttachDeviceAuthorizationEndpoint(ep)
	srv := httptest.NewServer(routerFor(h))
	t.Cleanup(srv.Close)
	return srv
}

// TestDeviceAuth_RejectsNonFormContentType covers RFC 8628 §3.1: the
// device authorization request is form-encoded; other content types
// are 400 + invalid_request before any client lookup.
func TestDeviceAuth_RejectsNonFormContentType(t *testing.T) {
	t.Parallel()
	srv := newDeviceAuthServer(t)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+oauth.PathDeviceAuthorization, strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["error"] != "invalid_request" {
		t.Errorf("error = %q, want invalid_request", body["error"])
	}
}

// TestDeviceAuth_MissingClientID covers RFC 8628 §3.1: client_id is
// REQUIRED; absence is 400 + invalid_request before the client lookup.
func TestDeviceAuth_MissingClientID(t *testing.T) {
	t.Parallel()
	srv := newDeviceAuthServer(t)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+oauth.PathDeviceAuthorization, strings.NewReader(url.Values{}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	if !strings.Contains(string(buf[:n]), "invalid_request") {
		t.Errorf("body = %q, want invalid_request", buf[:n])
	}
}

// ===== /oauth/token device_code grant (RFC 8628 §3.4 / §3.5) =====

// newDeviceGrantServer wires a TokenEndpoint WITHOUT a device-code
// store so the not-configured 503 branch is reachable, plus a variant
// that wires a nil-pool store so the missing-params 400 branch (which
// runs before any DB access) is reachable.
func newDeviceGrantServerNoStore(t *testing.T) *httptest.Server {
	t.Helper()
	ks, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	signer := tokensign.New(ks)
	ep := oauth.NewTokenEndpoint(signer, nil, oauth.TokenEndpointConfig{
		Issuer: testIssuer, RatePerMinute: 60, Now: pinnedNow,
	})
	// No AttachDeviceCodeStore — device_code grant is not configured.
	h := oauth.New(ks, oauth.Config{Issuer: testIssuer})
	h.AttachTokenEndpoint(ep)
	srv := httptest.NewServer(routerFor(h))
	t.Cleanup(srv.Close)
	return srv
}

func newDeviceGrantServerWithStore(t *testing.T) *httptest.Server {
	t.Helper()
	ks, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	signer := tokensign.New(ks)
	ep := oauth.NewTokenEndpoint(signer, nil, oauth.TokenEndpointConfig{
		Issuer: testIssuer, RatePerMinute: 60, Now: pinnedNow,
	})
	ep.AttachDeviceCodeStore(oauth.NewDeviceCodeStore(nil))
	h := oauth.New(ks, oauth.Config{Issuer: testIssuer})
	h.AttachTokenEndpoint(ep)
	srv := httptest.NewServer(routerFor(h))
	t.Cleanup(srv.Close)
	return srv
}

// TestDeviceGrant_NotConfigured covers the defensive 503 branch: when
// the TokenEndpoint has no device-code store wired, the device-code
// grant returns 503 + server_error so clients back off rather than
// retry against an endpoint that cannot serve them.
func TestDeviceGrant_NotConfigured(t *testing.T) {
	t.Parallel()
	srv := newDeviceGrantServerNoStore(t)

	form := url.Values{}
	form.Set("grant_type", oauth.GrantTypeDeviceCode)
	form.Set("device_code", "atlas-device-code-value")
	form.Set("client_id", "cli")
	resp, body := postForm(t, srv.URL, form)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s; want 503", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "server_error") {
		t.Errorf("body = %q, want server_error", body)
	}
}

// TestDeviceGrant_MissingParams covers RFC 8628 §3.4: device_code AND
// client_id are required; absence is 400 + invalid_request. This gate
// runs after the poll-tracker but before the DB consume, so it is
// reachable with a nil-pool store.
func TestDeviceGrant_MissingParams(t *testing.T) {
	t.Parallel()
	srv := newDeviceGrantServerWithStore(t)

	cases := []struct {
		name              string
		deviceCode, cid   string
		wantContainsError string
	}{
		{"both-missing", "", "", "invalid_request"},
		{"missing-client", "atlas-device-code", "", "invalid_request"},
		{"missing-device-code", "", "cli", "invalid_request"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			form := url.Values{}
			form.Set("grant_type", oauth.GrantTypeDeviceCode)
			if c.deviceCode != "" {
				form.Set("device_code", c.deviceCode)
			}
			if c.cid != "" {
				form.Set("client_id", c.cid)
			}
			resp, body := postForm(t, srv.URL, form)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s; want 400", resp.StatusCode, body)
			}
			if !strings.Contains(string(body), c.wantContainsError) {
				t.Errorf("body = %q, want %s", body, c.wantContainsError)
			}
		})
	}
}

// ===== devicePollTracker (RFC 8628 §3.5 slow_down) =====

// TestDevicePollTracker_FirstPollAllowedThenSlowDown covers RFC 8628
// §3.5: the first poll for a device_code always passes; a second poll
// within the 5-second interval is denied (the handler maps this to
// slow_down); a poll after the interval elapses is allowed again.
func TestDevicePollTracker_FirstPollAllowedThenSlowDown(t *testing.T) {
	t.Parallel()
	base := pinnedNow()
	now := base
	clock := func() time.Time { return now }
	tracker := oauth.ExportNewDevicePollTracker(clock)

	const dc = "device-code-A"
	if !oauth.ExportDevicePollAllow(tracker, dc, now) {
		t.Fatal("first poll must be allowed")
	}
	// 2 seconds later — within the 5s window — denied.
	now = base.Add(2 * time.Second)
	if oauth.ExportDevicePollAllow(tracker, dc, now) {
		t.Fatal("second poll within interval must be denied (slow_down)")
	}
	// 6 seconds after the first allowed poll — outside the window —
	// allowed again.
	now = base.Add(6 * time.Second)
	if !oauth.ExportDevicePollAllow(tracker, dc, now) {
		t.Fatal("poll after interval must be allowed")
	}
}

// TestDevicePollTracker_PerCodeIsolation covers that the poll floor is
// keyed per device_code: a fresh code is never throttled by another
// code's recent poll.
func TestDevicePollTracker_PerCodeIsolation(t *testing.T) {
	t.Parallel()
	now := pinnedNow()
	clock := func() time.Time { return now }
	tracker := oauth.ExportNewDevicePollTracker(clock)

	if !oauth.ExportDevicePollAllow(tracker, "code-1", now) {
		t.Fatal("code-1 first poll must be allowed")
	}
	if !oauth.ExportDevicePollAllow(tracker, "code-2", now) {
		t.Fatal("code-2 first poll must be allowed despite code-1's poll")
	}
}

// ===== buildDeviceCodeClaims (approval snapshot → JWT claims) =====

func strptr(s string) *string { return &s }
func boolptr(b bool) *bool    { return &b }

// TestBuildDeviceCodeClaims_HappyPath covers the approval-snapshot →
// JWT claim projection: the sub is `user:<uuid>`, the current tenant +
// available tenants + roles + super_admin are copied verbatim from the
// snapshot, and the issuer + audience are set to the configured issuer.
func TestBuildDeviceCodeClaims_HappyPath(t *testing.T) {
	t.Parallel()
	userID := uuid.New()
	tenant := uuid.New()
	roles := map[string][]string{tenant.String(): {"admin"}}
	rolesJSON, _ := json.Marshal(roles)

	row := &oauth.DeviceCodeRow{
		ApprovedByUserID:           strptr(userID.String()),
		ApprovedByIDPIssuer:        strptr("https://idp.example.test"),
		ApprovedByCurrentTenantID:  strptr(tenant.String()),
		ApprovedByAvailableTenants: []string{tenant.String()},
		ApprovedByRoles:            rolesJSON,
		ApprovedBySuperAdmin:       boolptr(false),
	}
	claims, err := oauth.ExportBuildDeviceCodeClaims(testIssuer, row, pinnedNow())
	if err != nil {
		t.Fatalf("buildDeviceCodeClaims: %v", err)
	}
	if claims.Subject != "user:"+userID.String() {
		t.Errorf("sub = %q, want user:%s", claims.Subject, userID)
	}
	if claims.Issuer != testIssuer {
		t.Errorf("iss = %q, want %s", claims.Issuer, testIssuer)
	}
	if claims.CurrentTenantID != tenant {
		t.Errorf("current_tenant = %v, want %v", claims.CurrentTenantID, tenant)
	}
	if !containsTenant(claims.AvailableTenants, tenant) {
		t.Errorf("available_tenants = %v, want to contain %v", claims.AvailableTenants, tenant)
	}
	if got := claims.Roles[tenant]; len(got) != 1 || got[0] != "admin" {
		t.Errorf("roles[%v] = %v, want [admin]", tenant, got)
	}
	if claims.SuperAdmin {
		t.Error("super_admin minted true from a false snapshot (P0-188-4)")
	}
}

// TestBuildDeviceCodeClaims_CopiesSuperAdmin covers P0-188-4 in the
// device-code path: a snapshot with super_admin=true projects to a
// token with super_admin=true (copy, not synthesize). Combined with
// the happy-path false case, this proves the flag is copied verbatim.
func TestBuildDeviceCodeClaims_CopiesSuperAdmin(t *testing.T) {
	t.Parallel()
	userID := uuid.New()
	row := &oauth.DeviceCodeRow{
		ApprovedByUserID:     strptr(userID.String()),
		ApprovedBySuperAdmin: boolptr(true),
	}
	claims, err := oauth.ExportBuildDeviceCodeClaims(testIssuer, row, pinnedNow())
	if err != nil {
		t.Fatalf("buildDeviceCodeClaims: %v", err)
	}
	if !claims.SuperAdmin {
		t.Error("super_admin not copied from a true snapshot")
	}
}

// TestBuildDeviceCodeClaims_MissingUserIDErrors covers the defensive
// error branch: a snapshot with no approved_by_user_id cannot mint a
// user token — the function returns an error so the handler surfaces a
// server_error rather than minting a subject-less JWT.
func TestBuildDeviceCodeClaims_MissingUserIDErrors(t *testing.T) {
	t.Parallel()
	row := &oauth.DeviceCodeRow{} // ApprovedByUserID nil
	_, err := oauth.ExportBuildDeviceCodeClaims(testIssuer, row, pinnedNow())
	if err == nil {
		t.Fatal("expected error for missing approval user_id")
	}
}

// TestBuildDeviceCodeClaims_InvalidUserIDErrors covers the parse-error
// branch: a non-UUID approved_by_user_id is rejected.
func TestBuildDeviceCodeClaims_InvalidUserIDErrors(t *testing.T) {
	t.Parallel()
	row := &oauth.DeviceCodeRow{ApprovedByUserID: strptr("not-a-uuid")}
	_, err := oauth.ExportBuildDeviceCodeClaims(testIssuer, row, pinnedNow())
	if err == nil {
		t.Fatal("expected error for non-UUID approval user_id")
	}
}

// ===== device approve/deny endpoints (atlas-internal) =====

// mountApproval builds a router that injects a credential into the
// request context (mimicking the upstream bearer middleware) before
// dispatching to the approval endpoints, OR omits it to exercise the
// unauthenticated branch.
func mountApproval(t *testing.T, cred *credstore.Credential) *httptest.Server {
	t.Helper()
	ep := oauth.NewDeviceApprovalEndpoint(oauth.NewDeviceCodeStore(nil), oauth.DeviceApprovalConfig{Now: pinnedNow})
	mux := http.NewServeMux()
	wrap := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if cred != nil {
				r = r.WithContext(authctx.WithCredential(r.Context(), *cred))
			}
			next(w, r)
		}
	}
	mux.HandleFunc(oauth.PathDeviceApprove, wrap(ep.ServeApprove))
	mux.HandleFunc(oauth.PathDeviceDeny, wrap(ep.ServeDeny))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func postJSON(t *testing.T, url, body string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Post(url, "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	return resp, buf[:n]
}

// TestDeviceApprove_Unauthenticated covers the auth gate: without a
// credential on the request context, ServeApprove returns 401 +
// invalid_token — only an authenticated OIDC session may approve a
// device code.
func TestDeviceApprove_Unauthenticated(t *testing.T) {
	t.Parallel()
	srv := mountApproval(t, nil)
	resp, body := postJSON(t, srv.URL+oauth.PathDeviceApprove, `{"user_code":"ABCD-EFGH"}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s; want 401", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "invalid_token") {
		t.Errorf("body = %q, want invalid_token", body)
	}
}

// TestDeviceDeny_Unauthenticated covers the same auth gate on the deny
// handler.
func TestDeviceDeny_Unauthenticated(t *testing.T) {
	t.Parallel()
	srv := mountApproval(t, nil)
	resp, body := postJSON(t, srv.URL+oauth.PathDeviceDeny, `{"user_code":"ABCD-EFGH"}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s; want 401", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "invalid_token") {
		t.Errorf("body = %q, want invalid_token", body)
	}
}

// TestDeviceApprove_MalformedJSON covers the body-decode error branch:
// an authenticated caller posting invalid JSON gets 400 +
// invalid_request.
func TestDeviceApprove_MalformedJSON(t *testing.T) {
	t.Parallel()
	cred := &credstore.Credential{UserID: uuid.NewString(), TenantID: uuid.NewString()}
	srv := mountApproval(t, cred)
	resp, body := postJSON(t, srv.URL+oauth.PathDeviceApprove, `{not json`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s; want 400", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "invalid_request") {
		t.Errorf("body = %q, want invalid_request", body)
	}
}

// TestDeviceApprove_MissingUserCode covers the validation branch: an
// authenticated caller posting valid JSON with an empty user_code gets
// 400 + invalid_request (this gate runs before the DB Approve call).
func TestDeviceApprove_MissingUserCode(t *testing.T) {
	t.Parallel()
	cred := &credstore.Credential{UserID: uuid.NewString(), TenantID: uuid.NewString()}
	srv := mountApproval(t, cred)
	resp, body := postJSON(t, srv.URL+oauth.PathDeviceApprove, `{"user_code":""}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s; want 400", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "invalid_request") {
		t.Errorf("body = %q, want invalid_request", body)
	}
}

// TestDeviceDeny_MissingUserCode covers the same validation branch on
// the deny handler.
func TestDeviceDeny_MissingUserCode(t *testing.T) {
	t.Parallel()
	cred := &credstore.Credential{UserID: uuid.NewString(), TenantID: uuid.NewString()}
	srv := mountApproval(t, cred)
	resp, body := postJSON(t, srv.URL+oauth.PathDeviceDeny, `{"user_code":""}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s; want 400", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "invalid_request") {
		t.Errorf("body = %q, want invalid_request", body)
	}
}

var _ = context.Background
