//go:build integration

// #2401: a user with no user_roles row in the tenant approves a device
// code. The approval snapshot copies the approver's empty claim, the CLI
// redeems it, and the minted token must derive no role at all: before
// #2401 the empty claim derived grc_engineer, so a role-less approver could
// mint a usable CLI token.

package oauth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/oauth"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
	"github.com/mgoodric/security-atlas/internal/auth/keystore/fsstore"
	"github.com/mgoodric/security-atlas/internal/auth/oauthclient"
	"github.com/mgoodric/security-atlas/internal/auth/superadmin"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
	"github.com/mgoodric/security-atlas/internal/authz"
)

func TestIntegrationDeviceFlow2401_RolelessApproverDerivesNothing(t *testing.T) {
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
	h := oauth.New(ks, oauth.Config{Issuer: testIssuer})
	h.AttachTokenEndpoint(tokenEP)
	h.AttachDeviceAuthorizationEndpoint(oauth.NewDeviceAuthorizationEndpoint(clients, devCodes, oauth.DeviceAuthorizationConfig{Issuer: testIssuer}))
	h.AttachDeviceApprovalEndpoint(oauth.NewDeviceApprovalEndpoint(devCodes, oauth.DeviceApprovalConfig{
		SuperAdmins: superadmin.NewDBChecker(pool),
	}))

	// The approver is a JWT session whose claim is empty for the tenant:
	// what every login path mints for a user with no user_roles row.
	tenant := uuid.New()
	approver := credstore.Credential{
		ID:       "jwt:" + uuid.NewString(),
		TenantID: tenant.String(),
		UserID:   "user:" + uuid.NewString(),
	}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(authctx.WithCredential(req.Context(), approver)))
		})
	})
	h.Mount(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	clientRow, _, err := clients.Issue(ctx, uniqueName(t, "dev-2401"))
	if err != nil {
		t.Fatalf("Issue OAuth client: %v", err)
	}
	authForm := url.Values{}
	authForm.Set("client_id", clientRow.ClientID)
	authResp, authBody := postFormTo(t, srv.URL+oauth.PathDeviceAuthorization, authForm)
	if authResp.StatusCode != http.StatusOK {
		t.Fatalf("device_authorization status = %d, body=%s", authResp.StatusCode, authBody)
	}
	var auth deviceAuthorizationResp
	if err := json.Unmarshal(authBody, &auth); err != nil {
		t.Fatalf("parse device auth: %v", err)
	}

	resp, body := postJSONTo(t, srv.URL+oauth.PathDeviceApprove, `{"user_code":`+jsonString(auth.UserCode)+`}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve status = %d, body = %s; want 200", resp.StatusCode, body)
	}

	pollForm := url.Values{}
	pollForm.Set("grant_type", oauth.GrantTypeDeviceCode)
	pollForm.Set("client_id", clientRow.ClientID)
	pollForm.Set("device_code", auth.DeviceCode)
	tokResp, tokBody := postForm(t, srv.URL, pollForm)
	if tokResp.StatusCode != http.StatusOK {
		t.Fatalf("redeem status = %d, body = %s; want 200", tokResp.StatusCode, tokBody)
	}
	var tok tokenResp
	if err := json.Unmarshal(tokBody, &tok); err != nil {
		t.Fatalf("parse token: %v", err)
	}

	// Present the token to the real jwtmw bridge and read the role set
	// authz would decide on.
	var roles []authz.Role
	var cred credstore.Credential
	mw := jwtmw.Middleware(signer, nil, jwtmw.Options{ExpectedIssuer: testIssuer, ExpectedAudience: testIssuer})
	probe := mw(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		cred, _ = authctx.CredentialFromContext(req.Context())
		roles = authz.BuildInput(req, nil).User.Roles
	}))
	req := httptest.NewRequest(http.MethodGet, "/v1/controls", nil)
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	rec := httptest.NewRecorder()
	probe.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("jwtmw rejected the device token: %d %s", rec.Code, rec.Body.String())
	}
	if cred.TenantID != tenant.String() || len(cred.OwnerRoles) != 0 {
		t.Fatalf("device token credential = tenant %q roles %v; want tenant %s, empty claim", cred.TenantID, cred.OwnerRoles, tenant)
	}
	if len(roles) != 0 {
		t.Errorf("role-less approver's device token derives %v; want nothing", roles)
	}
}
