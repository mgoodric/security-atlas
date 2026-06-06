//go:build integration

// device_approval_integration_test.go — slice 472 integration coverage
// for the slice-191 device-APPROVAL browser flow
// (device_approve.go + the DeviceCodeStore Approve/Deny/Consume/
// LookupByUserCode write+read arms + oauth.go AttachDeviceApprovalEndpoint).
//
// These arms were left untested by slices 314/422/456: the device_code
// integration suite writes the approval snapshot by calling
// DeviceCodeStore.Approve directly, never exercising the HTTP approve/deny
// handlers nor the store's error-mapping arms (not-found / expired /
// raced). This suite drives the real handlers mounted on the public
// Handler (via AttachDeviceApprovalEndpoint), injecting an authenticated
// credential the way the upstream bearer middleware does, and asserts the
// SECURE outcome for each arm — the specific RFC error CODE, never merely
// the HTTP status (P0-472-3).
//
// AUTH: ServeApprove/ServeDeny require an authenticated OIDC session. The
// harness injects a credstore.Credential onto the request context exactly
// as the device_test.go unit harness does, but here the endpoint is backed
// by a REAL pgx DeviceCodeStore so the Approve/Deny UPDATE arms run against
// a real oauth_device_codes row.
//
// No JWT/vendor-shaped fixture literals — the user_code values are neutral
// XXXX-XXXX test tokens (shortUserCode), the user id is a fresh UUID.

package oauth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/oauth"
	"github.com/mgoodric/security-atlas/internal/auth/keystore/fsstore"
)

// approvalHarness mounts the device-approval endpoints on the public
// Handler (via AttachDeviceApprovalEndpoint — AC-1) behind a credential-
// injecting middleware, against a real DeviceCodeStore. The clock is
// controllable so the expired-row arm needs no real-time sleep.
type approvalHarness struct {
	srv      *httptest.Server
	devCodes *oauth.DeviceCodeStore
	cred     credstore.Credential
	now      func() time.Time
}

func newApprovalHarness(t *testing.T, cred credstore.Credential, now func() time.Time) approvalHarness {
	t.Helper()
	pool := openTokenIntegrationPool(t)
	ks, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	devCodes := oauth.NewDeviceCodeStore(pool)
	approvalEP := oauth.NewDeviceApprovalEndpoint(devCodes, oauth.DeviceApprovalConfig{Now: now})

	h := oauth.New(ks, oauth.Config{Issuer: testIssuer})
	h.AttachDeviceApprovalEndpoint(approvalEP) // oauth.go:151 — AC-1 (0% before)

	// A chi router that injects the credential the way the upstream
	// bearer middleware does, then mounts the OAuth handler.
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			req = req.WithContext(authctx.WithCredential(req.Context(), cred))
			next.ServeHTTP(w, req)
		})
	})
	h.Mount(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return approvalHarness{srv: srv, devCodes: devCodes, cred: cred, now: now}
}

// insertCode inserts an unapproved device code expiring at expiresAt and
// returns its user_code + device_code. Neutral test values.
func (h approvalHarness) insertCode(t *testing.T, expiresAt time.Time) (userCode, deviceCode string) {
	t.Helper()
	deviceCode = "atlas-test-device-code-" + uuid.NewString()
	userCode = shortUserCode(t)
	if err := h.devCodes.Insert(context.Background(), oauth.DeviceCodeRow{
		DeviceCode: deviceCode,
		UserCode:   userCode,
		ClientID:   "atlas-cli-" + uuid.NewString()[:8],
		ExpiresAt:  expiresAt,
		CreatedAt:  expiresAt.Add(-15 * time.Minute),
	}); err != nil {
		t.Fatalf("Insert device code: %v", err)
	}
	return userCode, deviceCode
}

func (h approvalHarness) postApprove(t *testing.T, userCode string) (*http.Response, []byte) {
	t.Helper()
	return postJSONTo(t, h.srv.URL+oauth.PathDeviceApprove, `{"user_code":`+jsonString(userCode)+`}`)
}

func (h approvalHarness) postDeny(t *testing.T, userCode string) (*http.Response, []byte) {
	t.Helper()
	return postJSONTo(t, h.srv.URL+oauth.PathDeviceDeny, `{"user_code":`+jsonString(userCode)+`}`)
}

// jsonString returns a minimal JSON-quoted form of s (test values are
// alnum + hyphen so no escaping is needed beyond the quotes).
func jsonString(s string) string { return `"` + s + `"` }

// postJSONTo posts a JSON body to an explicit URL and returns the
// response + a bounded body read.
func postJSONTo(t *testing.T, url, body string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 8192)
	n, _ := resp.Body.Read(buf)
	return resp, buf[:n]
}

// approveAck is the success response shape the approve/deny handlers emit.
type approveAck struct {
	Status   string `json:"status"`
	UserCode string `json:"user_code"`
}

// adminCred builds an authenticated admin credential carrying a tenant +
// owner roles, so the ServeApprove snapshot-build branch (the
// cred.TenantID != "" arm that marshals AvailableTenants + RolesJSON) is
// exercised, AND super_admin inherits from IsAdmin (P0-188-4 shape).
func adminCred() credstore.Credential {
	return credstore.Credential{
		UserID:     uuid.NewString(),
		TenantID:   uuid.NewString(),
		IsAdmin:    true,
		OwnerRoles: []string{"grc_engineer"},
	}
}

// TestIntegrationDeviceApprove_Success covers device_approve.go ServeApprove
// happy path (AC-1): an authenticated caller approves a live device code →
// 200 + status:"approved", and the snapshot lands on the row (verified via
// LookupByUserCode — device_authorization.go:366, 0% before). Exercises the
// cred.TenantID != "" snapshot-build branch + DeviceCodeStore.Approve
// success arm (RowsAffected == 1).
func TestIntegrationDeviceApprove_Success(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Second)
	h := newApprovalHarness(t, adminCred(), func() time.Time { return base })
	userCode, _ := h.insertCode(t, base.Add(15*time.Minute))

	resp, body := h.postApprove(t, userCode)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s; want 200", resp.StatusCode, body)
	}
	var ack approveAck
	if err := json.Unmarshal(body, &ack); err != nil {
		t.Fatalf("decode ack: %v (body %s)", err, body)
	}
	if ack.Status != "approved" {
		t.Errorf("status = %q, want approved", ack.Status)
	}

	// Verify the snapshot landed (LookupByUserCode read path).
	row, err := h.devCodes.LookupByUserCode(context.Background(), userCode)
	if err != nil {
		t.Fatalf("LookupByUserCode: %v", err)
	}
	if row.ApprovedAt == nil {
		t.Error("approved_at not set after approval")
	}
	if row.ApprovedByUserID == nil || *row.ApprovedByUserID != h.cred.UserID {
		t.Errorf("approved_by_user_id = %v, want %q", row.ApprovedByUserID, h.cred.UserID)
	}
	// P0-188-4: super_admin inherits from the verified IsAdmin, not synthetic.
	if row.ApprovedBySuperAdmin == nil || !*row.ApprovedBySuperAdmin {
		t.Errorf("approved_by_super_admin = %v, want true (inherited from IsAdmin)", row.ApprovedBySuperAdmin)
	}
	if len(row.ApprovedByAvailableTenants) != 1 || row.ApprovedByAvailableTenants[0] != h.cred.TenantID {
		t.Errorf("approved_by_available_tenants = %v, want [%s]", row.ApprovedByAvailableTenants, h.cred.TenantID)
	}
}

// TestIntegrationDeviceApprove_NotFound covers ServeApprove's
// ErrDeviceCodeNotFound arm: approving a user_code with no row → 404 +
// invalid_request. Drives DeviceCodeStore.Approve's 0-rows-affected
// LookupByUserCode → ErrDeviceCodeNotFound branch (device_authorization.go:486).
func TestIntegrationDeviceApprove_NotFound(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Second)
	h := newApprovalHarness(t, adminCred(), func() time.Time { return base })

	resp, body := h.postApprove(t, shortUserCode(t)) // never inserted
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s; want 404", resp.StatusCode, body)
	}
	if got := decodeOAuthErr(t, body); got != "invalid_request" {
		t.Errorf("error = %q, want invalid_request", got)
	}
}

// TestIntegrationDeviceApprove_Expired covers ServeApprove's
// ErrDeviceCodeExpired arm: approving a code whose expires_at is before the
// approval clock → 400 + expired_token. Drives the Approve 0-rows path's
// row.ExpiresAt.Before(now) branch (device_authorization.go:495).
func TestIntegrationDeviceApprove_Expired(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Second)
	// Approval clock is well PAST the row's expiry.
	h := newApprovalHarness(t, adminCred(), func() time.Time { return base.Add(30 * time.Minute) })
	userCode, _ := h.insertCode(t, base.Add(5*time.Minute)) // expires before the approval clock

	resp, body := h.postApprove(t, userCode)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s; want 400", resp.StatusCode, body)
	}
	if got := decodeOAuthErr(t, body); got != "expired_token" {
		t.Errorf("error = %q, want expired_token", got)
	}
}

// TestIntegrationDeviceApprove_AlreadyConsumed covers ServeApprove's
// ErrDeviceCodeConsumed arm: approving a code that was already DENIED
// (consumed_at set) → 400 + invalid_grant. Drives the Approve 0-rows path's
// row.ConsumedAt != nil branch (device_authorization.go:492). The deny step
// also exercises DeviceCodeStore.Deny's success arm.
func TestIntegrationDeviceApprove_AlreadyConsumed(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Second)
	h := newApprovalHarness(t, adminCred(), func() time.Time { return base })
	userCode, _ := h.insertCode(t, base.Add(15*time.Minute))

	// Deny first → sets consumed_at.
	if err := h.devCodes.Deny(context.Background(), userCode, base); err != nil {
		t.Fatalf("Deny (setup): %v", err)
	}

	resp, body := h.postApprove(t, userCode)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s; want 400", resp.StatusCode, body)
	}
	if got := decodeOAuthErr(t, body); got != "invalid_grant" {
		t.Errorf("error = %q, want invalid_grant", got)
	}
}

// TestIntegrationDeviceApprove_Raced covers the "already approved" 0-rows
// arm of DeviceCodeStore.Approve (device_authorization.go:500): a second
// approval of an already-approved (but unconsumed, unexpired) code returns
// ErrDeviceCodeConsumed → the handler maps it to 400 + invalid_grant. This
// is the second-tab race shape the snapshot-at-approval invariant defends.
func TestIntegrationDeviceApprove_Raced(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Second)
	h := newApprovalHarness(t, adminCred(), func() time.Time { return base })
	userCode, _ := h.insertCode(t, base.Add(15*time.Minute))

	// First approval wins.
	resp1, body1 := h.postApprove(t, userCode)
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("first approve status = %d, body = %s; want 200", resp1.StatusCode, body1)
	}
	// Second approval (race) loses → invalid_grant.
	resp2, body2 := h.postApprove(t, userCode)
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("raced approve status = %d, body = %s; want 400", resp2.StatusCode, body2)
	}
	if got := decodeOAuthErr(t, body2); got != "invalid_grant" {
		t.Errorf("error = %q, want invalid_grant", got)
	}
}

// TestIntegrationDeviceDeny_Success covers ServeDeny's happy path (AC-1):
// an authenticated caller denies a live device code → 200 + status:"denied",
// and the row's consumed_at is set (verified via LookupByUserCode). Drives
// DeviceCodeStore.Deny's RowsAffected == 1 success arm.
func TestIntegrationDeviceDeny_Success(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Second)
	h := newApprovalHarness(t, adminCred(), func() time.Time { return base })
	userCode, _ := h.insertCode(t, base.Add(15*time.Minute))

	resp, body := h.postDeny(t, userCode)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s; want 200", resp.StatusCode, body)
	}
	var ack approveAck
	if err := json.Unmarshal(body, &ack); err != nil {
		t.Fatalf("decode ack: %v (body %s)", err, body)
	}
	if ack.Status != "denied" {
		t.Errorf("status = %q, want denied", ack.Status)
	}

	row, err := h.devCodes.LookupByUserCode(context.Background(), userCode)
	if err != nil {
		t.Fatalf("LookupByUserCode: %v", err)
	}
	if row.ConsumedAt == nil {
		t.Error("consumed_at not set after deny")
	}
	if row.ApprovedAt != nil {
		t.Error("approved_at set on a denied code; deny must NOT write the snapshot")
	}
}

// TestIntegrationDeviceDeny_NotFound covers ServeDeny's
// ErrDeviceCodeNotFound arm: denying a user_code with no row → 404 +
// invalid_request. Drives DeviceCodeStore.Deny's RowsAffected == 0 branch.
func TestIntegrationDeviceDeny_NotFound(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Second)
	h := newApprovalHarness(t, adminCred(), func() time.Time { return base })

	resp, body := h.postDeny(t, shortUserCode(t)) // never inserted
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s; want 404", resp.StatusCode, body)
	}
	if got := decodeOAuthErr(t, body); got != "invalid_request" {
		t.Errorf("error = %q, want invalid_request", got)
	}
}

// TestIntegrationDeviceApprove_NoTenantSnapshot covers the ServeApprove
// branch where the credential carries NO tenant (cred.TenantID == "" — a
// platform-global super_admin with no current tenant): the snapshot-build
// SKIPS the AvailableTenants/RolesJSON marshal, and Approve still writes a
// row with a NULL current_tenant. Asserts 200 + a NULL tenant snapshot.
func TestIntegrationDeviceApprove_NoTenantSnapshot(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Second)
	cred := credstore.Credential{UserID: uuid.NewString(), IsAdmin: true} // no TenantID
	h := newApprovalHarness(t, cred, func() time.Time { return base })
	userCode, _ := h.insertCode(t, base.Add(15*time.Minute))

	resp, body := h.postApprove(t, userCode)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s; want 200", resp.StatusCode, body)
	}
	row, err := h.devCodes.LookupByUserCode(context.Background(), userCode)
	if err != nil {
		t.Fatalf("LookupByUserCode: %v", err)
	}
	if row.ApprovedByCurrentTenantID != nil {
		t.Errorf("approved_by_current_tenant_id = %v, want NULL (no-tenant credential)", *row.ApprovedByCurrentTenantID)
	}
	if len(row.ApprovedByAvailableTenants) != 0 {
		t.Errorf("approved_by_available_tenants = %v, want empty (no-tenant credential)", row.ApprovedByAvailableTenants)
	}
}

// ensure pgxpool import is used even if the harness signature evolves.
var _ = (*pgxpool.Pool)(nil)
