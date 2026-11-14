package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
)

// Tests for #1892: the reset-bootstrap recovery key is refused for the
// all-zeros tenant, bounded in lifetime, marked, logged without its bearer,
// and revoked by the next reset or by a first sign-in from another
// credential.

const envBootstrapToken = "FAKE_ENV_BOOTSTRAP_not_a_secret"

// (a): a super_admin JWT with no current tenant carries the all-zeros uuid.
// It is refused with 400 before anything is reset, minted or written.
func TestResetBootstrap_AllZerosTenantRefused(t *testing.T) {
	f := newResetFixture(t, 1)
	f.tenantID = uuid.Nil.String()
	rec := f.post(t, f.superAdminJWT)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	f.assertNothingWritten(t, 0)
}

// (c): the recovery key expires. Default 24h; ttl_seconds overrides within
// (0, 7d]; out-of-range values are refused before anything happens.
func TestResetBootstrap_RecoveryKeyTTL(t *testing.T) {
	cases := map[string]struct {
		body    string
		wantTTL time.Duration
	}{
		"default":  {`{"force":true}`, 24 * time.Hour},
		"override": {`{"force":true,"ttl_seconds":3600}`, time.Hour},
		"max":      {`{"force":true,"ttl_seconds":604800}`, 7 * 24 * time.Hour},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newResetFixture(t, 1)
			resp := decodeReset(t, f.postBody(t, f.superAdminJWT, tc.body))
			cred, err := f.srv.credStore.Authenticate(resp.BearerToken)
			if err != nil {
				t.Fatalf("Authenticate: %v", err)
			}
			if cred.TTL != tc.wantTTL || cred.Purpose != credstore.PurposeBootstrapRecovery {
				t.Fatalf("credential ttl=%v purpose=%q; want %v, recovery", cred.TTL, cred.Purpose, tc.wantTTL)
			}
			if want := cred.ExpiresAt().Format(time.RFC3339); resp.ExpiresAt != want {
				t.Fatalf("expires_at = %q; want %s", resp.ExpiresAt, want)
			}
		})
	}
	for name, body := range map[string]string{
		"negative":    `{"force":true,"ttl_seconds":-1}`,
		"over 7 days": `{"force":true,"ttl_seconds":604801}`,
		// 2^62 seconds: time.Duration(n) * time.Second wraps negative.
		// Must be refused before the reset, not as a 500 at the mint.
		"overflowing": `{"force":true,"ttl_seconds":4611686018427387904}`,
		"max int64":   `{"force":true,"ttl_seconds":9223372036854775807}`,
	} {
		t.Run("refused "+name, func(t *testing.T) {
			f := newResetFixture(t, 1)
			rec := f.postBody(t, f.superAdminJWT, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			f.assertNothingWritten(t, 0)
		})
	}
}

// (c): each reset revokes the recovery keys earlier resets minted, and only
// those: the env ATLAS_BOOTSTRAP_TOKEN key and an ordinary admin key stay
// live.
func TestResetBootstrap_RevokesEarlierRecoveryKeysOnly(t *testing.T) {
	f := newResetFixture(t, 1)
	if _, err := f.srv.IssueBootstrapFixedAdminCredential(f.tenantID, envBootstrapToken); err != nil {
		t.Fatalf("IssueBootstrapFixedAdminCredential: %v", err)
	}
	_, adminBearer, err := f.srv.IssueBootstrapCredential(f.tenantID)
	if err != nil {
		t.Fatalf("IssueBootstrapCredential: %v", err)
	}

	first := decodeReset(t, f.post(t, f.superAdminJWT))
	if len(first.RevokedCredentialIDs) != 0 {
		t.Fatalf("first reset revoked %v; want none", first.RevokedCredentialIDs)
	}
	second := decodeReset(t, f.post(t, f.superAdminJWT))

	if len(second.RevokedCredentialIDs) != 1 || second.RevokedCredentialIDs[0] != first.CredentialID {
		t.Fatalf("second reset revoked %v; want [%s]", second.RevokedCredentialIDs, first.CredentialID)
	}
	if _, err := f.srv.credStore.Authenticate(first.BearerToken); err == nil {
		t.Fatalf("first recovery key still authenticates after a second reset")
	}
	for name, b := range map[string]string{
		"env bootstrap key": envBootstrapToken,
		"ordinary key":      adminBearer,
		"new recovery key":  second.BearerToken,
	} {
		if _, err := f.srv.credStore.Authenticate(b); err != nil {
			t.Fatalf("%s revoked by reset: %v", name, err)
		}
	}
}

// (b): the mint is logged with the new credential id, the tenant and the
// caller's credential id and subject, and the bearer appears nowhere. A JWT
// caller is recorded as the minter (api_keys.minted_by), and the key does
// NOT take on that user's identity: its UserID stays its own id.
func TestResetBootstrap_LogsMintWithoutBearer(t *testing.T) {
	f := newResetFixture(t, 1)
	var buf bytes.Buffer
	f.srv.AttachLogger(slog.New(slog.NewJSONHandler(&buf, nil)))

	userID := uuid.NewString()
	asUser := func(ctx context.Context) context.Context {
		ctx = authctx.WithCredential(ctx, credstore.Credential{
			ID: "jwt:abc", UserID: "user:" + userID, TenantID: f.tenantID,
		})
		return jwtmw.WithClaimsForTest(ctx, &jwt.AtlasClaims{
			RegisteredClaims: jwt.RegisteredClaims{Subject: "user:" + userID},
			SuperAdmin:       true,
		})
	}
	resp := decodeReset(t, f.post(t, asUser))
	logs := buf.String()
	if strings.Contains(logs, resp.BearerToken) {
		t.Fatalf("log contains the bearer: %s", logs)
	}
	for _, want := range []string{
		`"credential_id":"` + resp.CredentialID + `"`,
		`"tenant_id":"` + f.tenantID + `"`,
		`"caller_credential_id":"jwt:abc"`,
		`"caller_subject":"user:` + userID + `"`,
		`"minted_by":"` + userID + `"`,
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("log missing %s: %s", want, logs)
		}
	}
	cred, err := f.srv.credStore.Authenticate(resp.BearerToken)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if cred.MintedBy != userID {
		t.Fatalf("recovery key MintedBy = %q; want minter %q", cred.MintedBy, userID)
	}
	if cred.UserID != cred.ID {
		t.Fatalf("recovery key UserID = %q; want its own id %q, never the minter's", cred.UserID, cred.ID)
	}

	// An API-key caller is not a user: no minter is recorded.
	resp = decodeReset(t, f.post(t, f.tenantAdminKey))
	cred, err = f.srv.credStore.Authenticate(resp.BearerToken)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if cred.UserID != cred.ID || cred.MintedBy != "" {
		t.Fatalf("key-minted recovery UserID=%q MintedBy=%q; want its own id and no minter", cred.UserID, cred.MintedBy)
	}
}

// (c): the first sign-in by a different credential revokes the recovery
// key. A sign-in with the recovery key itself keeps it, because revoking it
// would end that session. The env key is never touched.
func TestMarkFirstSignin_RevokesRecoveryKeyForOtherCaller(t *testing.T) {
	for name, tc := range map[string]struct {
		asRecovery bool
		wantKept   bool
	}{
		"other credential signs in":    {false, false},
		"recovery key itself signs in": {true, true},
	} {
		t.Run(name, func(t *testing.T) {
			f := newResetFixture(t, 1)
			f.srv.AttachPlatformStatus(&fakePlatformStatus{markDid: true})
			if _, err := f.srv.IssueBootstrapFixedAdminCredential(f.tenantID, envBootstrapToken); err != nil {
				t.Fatalf("IssueBootstrapFixedAdminCredential: %v", err)
			}
			reset := decodeReset(t, f.post(t, f.superAdminJWT))
			recovery, err := f.srv.credStore.Authenticate(reset.BearerToken)
			if err != nil {
				t.Fatalf("Authenticate recovery key: %v", err)
			}
			caller := credstore.Credential{ID: "jwt:someone", TenantID: f.tenantID, IsAdmin: true}
			if tc.asRecovery {
				caller = recovery
			}

			resp := markFirstSignin(t, f.srv, caller)
			_, authErr := f.srv.credStore.Authenticate(reset.BearerToken)
			if tc.wantKept {
				if !resp.RecoveryKeyKept || len(resp.RevokedCredentialIDs) != 0 || authErr != nil {
					t.Fatalf("resp=%+v authErr=%v; want the caller's recovery key kept live", resp, authErr)
				}
			} else {
				if resp.RecoveryKeyKept || len(resp.RevokedCredentialIDs) != 1 ||
					resp.RevokedCredentialIDs[0] != reset.CredentialID || authErr == nil {
					t.Fatalf("resp=%+v authErr=%v; want the recovery key revoked", resp, authErr)
				}
			}
			if _, err := f.srv.credStore.Authenticate(envBootstrapToken); err != nil {
				t.Fatalf("env bootstrap key revoked on sign-in: %v", err)
			}
		})
	}
}

// A repeat sign-in (first_signin_at already set) revokes nothing.
func TestMarkFirstSignin_RepeatSigninRevokesNothing(t *testing.T) {
	f := newResetFixture(t, 1)
	reset := decodeReset(t, f.post(t, f.superAdminJWT))
	f.srv.AttachPlatformStatus(&fakePlatformStatus{markDid: false})
	resp := markFirstSignin(t, f.srv, credstore.Credential{ID: "jwt:someone", TenantID: f.tenantID, IsAdmin: true})
	if len(resp.RevokedCredentialIDs) != 0 {
		t.Fatalf("repeat sign-in revoked %v", resp.RevokedCredentialIDs)
	}
	if _, err := f.srv.credStore.Authenticate(reset.BearerToken); err != nil {
		t.Fatalf("repeat sign-in revoked the recovery key: %v", err)
	}
}

// #1906: after a reset, a non-admin credential calling mark-first-signin
// first is refused before the marker flips, so the recovery key stays live.
// A request with no credential at all is 401.
func TestMarkFirstSignin_RefusesNonAdminCaller(t *testing.T) {
	for name, tc := range map[string]struct {
		withCred bool
		want     int
	}{
		"non-admin credential": {true, http.StatusForbidden},
		"no credential":        {false, http.StatusUnauthorized},
	} {
		t.Run(name, func(t *testing.T) {
			f := newResetFixture(t, 1)
			reset := decodeReset(t, f.post(t, f.superAdminJWT))
			status := &fakePlatformStatus{markDid: true}
			f.srv.AttachPlatformStatus(status)

			req := httptest.NewRequest(http.MethodPost, "/v1/install/mark-first-signin", nil)
			if tc.withCred {
				req = req.WithContext(authctx.WithCredential(req.Context(),
					credstore.Credential{ID: "low-priv", TenantID: f.tenantID}))
			}
			rec := httptest.NewRecorder()
			f.srv.handleMarkFirstSignin(rec, req)

			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
			if status.markCalls != 0 {
				t.Fatalf("MarkFirstSignin called %d times; want 0 for a refused caller", status.markCalls)
			}
			if _, err := f.srv.credStore.Authenticate(reset.BearerToken); err != nil {
				t.Fatalf("refused caller revoked the recovery key: %v", err)
			}
		})
	}
}

func markFirstSignin(t *testing.T, srv *Server, caller credstore.Credential) markFirstSigninResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/install/mark-first-signin", nil)
	req = req.WithContext(authctx.WithCredential(req.Context(), caller))
	rec := httptest.NewRecorder()
	srv.handleMarkFirstSignin(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mark-first-signin status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var resp markFirstSigninResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func decodeReset(t *testing.T, rec *httptest.ResponseRecorder) resetBootstrapResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp resetBootstrapResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}
