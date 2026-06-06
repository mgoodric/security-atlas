//go:build integration

// residual_audit_integration_test.go — slice 456 integration coverage
// for the DB-state-dependent RESIDUAL arms of the OAuth AS that the
// slice-422 unit suite could not reach without Postgres.
//
// Two surfaces:
//
//   - Best-effort audit-write FAILURE arms (token.go writeAudit +
//     pkce.go writeAuthCodeAudit). Per D3 the audit write is best-effort:
//     a BeginTx / Exec failure MUST NOT block nor corrupt the token
//     response. These tests inject the failure deterministically (a
//     closed pool for BeginTx; a search_path-stripped pool for the
//     missing-relation Exec failure) and assert the SECURE outcome — the
//     method returns without panicking, and through the full handler the
//     token response is still 200. This is the REPUDIATION-surface
//     residual the slice-456 spec calls out.
//
//   - Signer-failure 500 arm for client_credentials (token.go:272). The
//     unit suite reaches the token-exchange sign-fail arm; the
//     client_credentials arm needs a real oauth_clients row to pass
//     Verify before reaching the Sign step, so it lives here. Driven with
//     a verify-ok/sign-fail signer (non-ES256 active key) + a real client.
//
// No JWT/vendor-shaped fixture literals — every token is minted
// in-process by the real fsstore signer; the sign-failure is a P-384
// curve, not a pasted credential.

package oauth_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/oauth"
	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/keystore"
	"github.com/mgoodric/security-atlas/internal/auth/keystore/fsstore"
	"github.com/mgoodric/security-atlas/internal/auth/oauthclient"
	"github.com/mgoodric/security-atlas/internal/auth/oauthcode"
	"github.com/mgoodric/security-atlas/internal/auth/sessions"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
)

// openBrokenSearchPathPool opens an atlas_app pool whose connections run
// `SET search_path TO oauth_residual_void` — an empty schema — so the
// UNQUALIFIED `oauth_token_exchanges` INSERT resolves to no relation and
// the Exec step fails deterministically. ApplyTenant's SET LOCAL still
// succeeds (it sets a GUC, not a table). This drives the Exec-failure
// best-effort arm without corrupting the real audit table.
func openBrokenSearchPathPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := openDSN(t)
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		// Create-if-missing an empty schema, then strip the search_path
		// to it so unqualified table names resolve to nothing.
		if _, err := c.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS oauth_residual_void"); err != nil {
			return err
		}
		_, err := c.Exec(ctx, "SET search_path TO oauth_residual_void")
		return err
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewWithConfig: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func openDSN(t *testing.T) string {
	t.Helper()
	pool := openTokenIntegrationPool(t) // skips if DATABASE_URL_APP unset
	return pool.Config().ConnString()
}

// newAuditSeamEndpoint builds a TokenEndpoint wired with the supplied
// audit pool (real fsstore signer) so the writeAudit / writeAuthCodeAudit
// seams drive the failure arms.
func newAuditSeamEndpoint(t *testing.T, auditPool *pgxpool.Pool) *oauth.TokenEndpoint {
	t.Helper()
	ks, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	return oauth.ExportTokenEndpointForAudit(tokensign.New(ks), auditPool, time.Now)
}

// TestWriteAudit_BeginTxFailureIsNonBlocking covers token.go:405 — a
// BeginTx failure (here: a closed pool) is swallowed best-effort; the
// seam returns without panicking. D3: the audit failure never surfaces
// to the caller.
func TestWriteAudit_BeginTxFailureIsNonBlocking(t *testing.T) {
	pool := openTokenIntegrationPool(t)
	// Close the pool so BeginTx fails.
	closed, err := pgxpool.New(context.Background(), pool.Config().ConnString())
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	closed.Close()

	ep := newAuditSeamEndpoint(t, closed)
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", nil)
	req.RemoteAddr = "192.0.2.10:54321"
	// MUST NOT panic; BeginTx error returns early.
	oauth.ExportWriteAudit(ep, req, jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{Issuer: testIssuer, Subject: "user:vciso", ID: uuid.NewString()},
		CurrentTenantID:  uuid.New(),
	}, uuid.New())
}

// TestWriteAudit_ExecFailureIsNonBlocking covers token.go:445 — an Exec
// failure (missing relation via stripped search_path) is swallowed
// best-effort. ALSO exercises the non-empty-jti happy branch up to the
// Exec call and the from_tenant non-nil branch (token.go:416-419).
func TestWriteAudit_ExecFailureIsNonBlocking(t *testing.T) {
	pool := openBrokenSearchPathPool(t)
	ep := newAuditSeamEndpoint(t, pool)
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", nil)
	req.RemoteAddr = "192.0.2.10:54321"
	oauth.ExportWriteAudit(ep, req, jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{Issuer: testIssuer, Subject: "user:vciso", ID: uuid.NewString()},
		CurrentTenantID:  uuid.New(), // non-nil → from_tenant branch taken
	}, uuid.New())
}

// TestWriteAudit_EmptyJTIFallback covers token.go:429 — a subject_token
// with an empty `jti` (ID) uses the "unknown" fallback so the
// ote_jti_nonempty CHECK is not violated. We write against the REAL audit
// table (happy insert) with an empty-ID claim and assert no panic; the
// row lands with jti="unknown".
func TestWriteAudit_EmptyJTIFallback(t *testing.T) {
	pool := openTokenIntegrationPool(t)
	ep := newAuditSeamEndpoint(t, pool)
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", nil)
	req.RemoteAddr = "192.0.2.10:54321"
	target := uuid.New()
	oauth.ExportWriteAudit(ep, req, jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{Issuer: testIssuer, Subject: "user:vciso", ID: ""}, // empty jti
		// CurrentTenantID nil → from_tenant NULL branch.
	}, target)

	// Verify the fallback row landed with jti="unknown" (RLS-scoped read).
	var jti string
	if err := scanAuditRowForTenant(t, pool, target,
		`SELECT subject_token_jti FROM oauth_token_exchanges WHERE to_tenant_id=$1 ORDER BY exchanged_at DESC LIMIT 1`,
		&jti); err != nil {
		t.Fatalf("scan audit row: %v", err)
	}
	if jti != "unknown" {
		t.Errorf("subject_token_jti = %q, want \"unknown\" (empty-jti fallback)", jti)
	}
}

// scanAuditRowForTenant runs an RLS-scoped read against
// oauth_token_exchanges inside a tx with the tenant GUC set, so the
// tenant_read policy admits the row written by the best-effort audit
// path. dst pointers receive the selected columns.
func scanAuditRowForTenant(t *testing.T, pool *pgxpool.Pool, tenant uuid.UUID, q string, dst ...any) error {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenant.String()); err != nil {
		return err
	}
	return tx.QueryRow(ctx, q, tenant).Scan(dst...)
}

// TestWriteAuthCodeAudit_ExecFailureIsNonBlocking covers pkce.go:135 —
// the authorization_code audit Exec failure best-effort arm. ALSO
// exercises the jti-truncation branch (pkce.go:116) with an over-64-char
// code.
func TestWriteAuthCodeAudit_ExecFailureIsNonBlocking(t *testing.T) {
	pool := openBrokenSearchPathPool(t)
	ep := newAuditSeamEndpoint(t, pool)
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", nil)
	longCode := "atlas-test-authcode-" + strings.Repeat("x", 80) // > 64 chars → truncated
	oauth.ExportWriteAuthCodeAudit(ep, req, oauthcode.AuthCode{
		Code:            longCode,
		UserID:          uuid.New(),
		CurrentTenantID: uuid.New(),
		IDPIssuer:       "https://idp.example.test",
	})
}

// TestWriteAuthCodeAudit_BeginTxFailureIsNonBlocking covers pkce.go:101.
func TestWriteAuthCodeAudit_BeginTxFailureIsNonBlocking(t *testing.T) {
	pool := openTokenIntegrationPool(t)
	closed, err := pgxpool.New(context.Background(), pool.Config().ConnString())
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	closed.Close()
	ep := newAuditSeamEndpoint(t, closed)
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", nil)
	oauth.ExportWriteAuthCodeAudit(ep, req, oauthcode.AuthCode{
		Code:            "atlas-test-authcode",
		UserID:          uuid.New(),
		CurrentTenantID: uuid.New(),
		IDPIssuer:       "https://idp.example.test",
	})
}

// TestWriteAuthCodeAudit_HappyInsert covers the pkce.go:126 successful
// INSERT + Commit path against the REAL table (the integration suite's
// device/authorize flows exercise this transitively, but an explicit
// assertion pins the row shape: from_tenant NULL, to_tenant = current).
func TestWriteAuthCodeAudit_HappyInsert(t *testing.T) {
	pool := openTokenIntegrationPool(t)
	ep := newAuditSeamEndpoint(t, pool)
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", nil)
	tenant := uuid.New()
	userID := uuid.New()
	oauth.ExportWriteAuthCodeAudit(ep, req, oauthcode.AuthCode{
		Code:            "atlas-test-authcode-" + uuid.NewString(),
		UserID:          userID,
		CurrentTenantID: tenant,
		IDPIssuer:       "https://idp.example.test",
	})

	var fromTenant *uuid.UUID
	var sub string
	if err := scanAuditRowForTenant(t, pool, tenant,
		`SELECT from_tenant_id, subject_token_sub FROM oauth_token_exchanges WHERE to_tenant_id=$1 ORDER BY exchanged_at DESC LIMIT 1`,
		&fromTenant, &sub); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if fromTenant != nil {
		t.Errorf("from_tenant_id = %v, want NULL (initial mint)", fromTenant)
	}
	if sub != "user:"+userID.String() {
		t.Errorf("subject_token_sub = %q, want user:%s", sub, userID)
	}
}

// ===== client_credentials signer-failure (token.go:272) =====

// signFailStore mirrors residual_branches_test.go's verify-ok/sign-fail
// keystore (duplicated here because that one lives in the non-integration
// build; this file is integration-tagged).
type signFailStore struct {
	inner keystore.KeyStore
	bad   keystore.SigningKey
}

func newSignFailStore(t *testing.T, inner keystore.KeyStore) *signFailStore {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatalf("generate P-384: %v", err)
	}
	return &signFailStore{inner: inner, bad: keystore.SigningKey{KeyID: "sign-fail-p384", Key: priv}}
}

func (s *signFailStore) Get(ctx context.Context) (keystore.SigningKey, []keystore.VerificationKey, error) {
	_, vks, err := s.inner.Get(ctx)
	if err != nil {
		return keystore.SigningKey{}, nil, err
	}
	return s.bad, vks, nil
}
func (s *signFailStore) Rotate(ctx context.Context) error { return s.inner.Rotate(ctx) }

// TestClientCredentials_SignerFailureReturnsServerError covers
// token.go:272 (AC-2): a valid client passes Verify, execution reaches
// the JWT mint, which fails on the non-ES256 signing key. The handler
// MUST surface 500 + server_error with no internal-detail leak.
func TestClientCredentials_SignerFailureReturnsServerError(t *testing.T) {
	pool := openTokenIntegrationPool(t)

	ks, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	signFailSigner := tokensign.New(newSignFailStore(t, ks))
	clients := oauthclient.New(pool)
	name := uniqueName(t, "it-cc-signfail")
	client, secret, err := clients.Issue(context.Background(), name)
	if err != nil {
		t.Fatalf("Issue client: %v", err)
	}

	ep := oauth.NewTokenEndpoint(signFailSigner, clients, oauth.TokenEndpointConfig{
		Issuer:        testIssuer,
		AuditPool:     pool,
		RatePerMinute: 600,
	})
	h := oauth.New(ks, oauth.Config{Issuer: testIssuer})
	h.AttachTokenEndpoint(ep)
	srv := httptest.NewServer(routerFor(h))
	t.Cleanup(srv.Close)

	form := url.Values{}
	form.Set("grant_type", oauth.GrantTypeClientCredentials)
	form.Set("client_id", client.ClientID)
	form.Set("client_secret", secret)
	resp, body := postForm(t, srv.URL, form)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s; want 500", resp.StatusCode, body)
	}
	if got := decodeOAuthErr(t, body); got != "server_error" {
		t.Errorf("error = %q, want server_error", got)
	}
}

// TestAuthorizationCode_SignerFailureReturnsServerError covers
// authorize.go:479 (AC-2): a valid PKCE authorization_code redemption
// reaches the JWT mint, which fails on the non-ES256 signing key. The
// AuthorizeEndpoint mints the code (no signing); the sign-fail signer
// only bites at the token-redemption step. Asserts 500 + server_error.
func TestAuthorizationCode_SignerFailureReturnsServerError(t *testing.T) {
	pool := openTokenIntegrationPool(t)
	const redirectURI = "https://atlas.example.test/oauth/callback"
	clientID, _, sess, identity := setupAuthorizeFixture(t, pool, redirectURI)
	srv := newAuthorizeSignFailServer(t, pool, sess, identity)

	verifier := "atlas-test-verifier-" + uuid.NewString()
	challenge := oauth.ExportComputePKCEChallengeS256(verifier)
	code := mustAuthorize(t, srv.URL, clientID, redirectURI, challenge, sess, identity)

	form := url.Values{}
	form.Set("grant_type", oauth.GrantTypeAuthorizationCode)
	form.Set("code", code)
	form.Set("code_verifier", verifier)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", clientID)
	resp, body := postForm(t, srv.URL, form)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s; want 500", resp.StatusCode, body)
	}
	if got := decodeOAuthErr(t, body); got != "server_error" {
		t.Errorf("error = %q, want server_error", got)
	}
}

// newAuthorizeSignFailServer mirrors newAuthorizeIntegrationServer but
// wires a verify-ok/sign-fail signer so the authorization_code mint
// step fails. The AuthorizeEndpoint shares the same (sign-fail) signer
// but never signs — it only writes the oauth_auth_codes row.
func newAuthorizeSignFailServer(t *testing.T, pool *pgxpool.Pool, sess sessions.Session, identity oauth.UserIdentity) *httptest.Server {
	t.Helper()
	ks, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	signFailSigner := tokensign.New(newSignFailStore(t, ks))
	clients := oauthclient.New(pool)
	codes := oauthcode.New(pool)
	ep := oauth.NewTokenEndpoint(signFailSigner, clients, oauth.TokenEndpointConfig{
		Issuer:        testIssuer,
		AuditPool:     pool,
		RatePerMinute: 600,
	})
	ep.AttachAuthCodeStore(codes)
	authorizeEP := oauth.NewAuthorizeEndpoint(oauth.AuthorizeEndpointConfig{
		Codes:    codes,
		Clients:  clients,
		Sessions: &itSessionResolver{sess: sess},
		Users:    &itUserResolver{identity: identity},
		Issuer:   testIssuer,
	})
	h := oauth.New(ks, oauth.Config{Issuer: testIssuer})
	h.AttachTokenEndpoint(ep)
	h.AttachAuthorizeEndpoint(authorizeEP)
	r := chi.NewRouter()
	h.Mount(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

// TestDeviceCode_SignerFailureReturnsServerError covers
// device_code_grant.go:127 (AC-2): an APPROVED device code redeemed via
// grant_type=device_code reaches the JWT mint, which fails on the
// non-ES256 signing key. Approving BEFORE the first poll means a single
// poll redeems and reaches the sign step with no slow_down sleep needed.
func TestDeviceCode_SignerFailureReturnsServerError(t *testing.T) {
	pool := openTokenIntegrationPool(t)
	ctx := context.Background()
	ks, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	signFailSigner := tokensign.New(newSignFailStore(t, ks))
	clients := oauthclient.New(pool)
	devCodes := oauth.NewDeviceCodeStore(pool)
	tokenEP := oauth.NewTokenEndpoint(signFailSigner, clients, oauth.TokenEndpointConfig{
		Issuer:        testIssuer,
		AuditPool:     pool,
		RatePerMinute: 600,
	})
	tokenEP.AttachDeviceCodeStore(devCodes)
	deviceAuthEP := oauth.NewDeviceAuthorizationEndpoint(clients, devCodes, oauth.DeviceAuthorizationConfig{Issuer: testIssuer})
	h := oauth.New(ks, oauth.Config{Issuer: testIssuer})
	h.AttachTokenEndpoint(tokenEP)
	h.AttachDeviceAuthorizationEndpoint(deviceAuthEP)
	srv := httptest.NewServer(newRouter(h))
	t.Cleanup(srv.Close)

	clientRow, _, err := clients.Issue(ctx, uniqueName(t, "dev-signfail"))
	if err != nil {
		t.Fatalf("Issue OAuth client: %v", err)
	}

	// Initiate device authorization.
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

	// Approve BEFORE the first poll so a single poll redeems → reaches Sign.
	if err := devCodes.Approve(ctx, oauth.ApproveInput{
		UserCode:        auth.UserCode,
		UserID:          uuid.New().String(),
		IDPIssuer:       "https://idp.example.test",
		IDPSubject:      "test-subject",
		CurrentTenantID: "",
		SuperAdmin:      true,
	}, time.Now()); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	pollForm := url.Values{}
	pollForm.Set("grant_type", oauth.GrantTypeDeviceCode)
	pollForm.Set("client_id", clientRow.ClientID)
	pollForm.Set("device_code", auth.DeviceCode)
	resp, body := postForm(t, srv.URL, pollForm)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s; want 500", resp.StatusCode, body)
	}
	if got := decodeOAuthErr(t, body); got != "server_error" {
		t.Errorf("error = %q, want server_error", got)
	}
}
