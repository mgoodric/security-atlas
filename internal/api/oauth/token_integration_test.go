//go:build integration

package oauth_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/oauth"
	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/keystore/fsstore"
	"github.com/mgoodric/security-atlas/internal/auth/oauthclient"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
)

// openTokenIntegrationPool opens the atlas_app-role pool used for
// oauth_clients reads/writes + oauth_token_exchanges audit writes.
// Returns nil if DATABASE_URL_APP is unset (test will skip).
func openTokenIntegrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL_APP")
	if dsn == "" {
		t.Skip("DATABASE_URL_APP not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// newIntegrationServer wires the full slice-188 surface: a real
// fsstore keystore, a real pgxpool-backed oauthclient.Store, and a
// TokenEndpoint with the audit pool wired. Returns the test server
// + signer for verifying minted tokens.
func newIntegrationServer(t *testing.T, pool *pgxpool.Pool) (*httptest.Server, *tokensign.Signer) {
	t.Helper()
	ks, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	signer := tokensign.New(ks)
	clients := oauthclient.New(pool)
	ep := oauth.NewTokenEndpoint(signer, clients, oauth.TokenEndpointConfig{
		Issuer:        testIssuer,
		AuditPool:     pool,
		RatePerMinute: 600, // generous limit so the integration test never trips
	})
	h := oauth.New(ks, oauth.Config{Issuer: testIssuer})
	h.AttachTokenEndpoint(ep)
	r := newRouter(h)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, signer
}

// uniqueName returns a process-unique name for an oauth_clients
// row so concurrent integration test invocations cannot collide.
func uniqueName(t *testing.T, prefix string) string {
	t.Helper()
	return fmt.Sprintf("%s-%s", prefix, uuid.New().String()[:8])
}

// TestIntegrationClientCredentialsRoundTrip covers AC-1..AC-8 end
// to end: issue a client via internal/auth/oauthclient.Issue,
// authenticate via POST /oauth/token, verify the minted JWT has the
// claim shape AC-6 mandates.
func TestIntegrationClientCredentialsRoundTrip(t *testing.T) {
	pool := openTokenIntegrationPool(t)
	srv, signer := newIntegrationServer(t, pool)

	store := oauthclient.New(pool)
	name := uniqueName(t, "it-cc")
	client, secret, err := store.Issue(context.Background(), name)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	form := url.Values{}
	form.Set("grant_type", oauth.GrantTypeClientCredentials)
	form.Set("client_id", client.ClientID)
	form.Set("client_secret", secret)

	resp, body := postForm(t, srv.URL, form)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
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
		t.Errorf("token_type = %q want Bearer", out.TokenType)
	}
	if out.ExpiresIn != 3600 {
		t.Errorf("expires_in = %d want 3600", out.ExpiresIn)
	}

	claims, err := signer.Verify(context.Background(), out.AccessToken)
	if err != nil {
		t.Fatalf("verify minted: %v", err)
	}
	wantSub := oauth.MachineSubjectPrefix + client.ClientID
	if claims.Subject != wantSub {
		t.Errorf("sub = %q want %q", claims.Subject, wantSub)
	}
	if claims.IDPIssuer != oauth.MachineIDPIssuer {
		t.Errorf("atlas:idp_issuer = %q want %q", claims.IDPIssuer, oauth.MachineIDPIssuer)
	}
	if claims.SuperAdmin {
		t.Errorf("super_admin true on client_credentials token")
	}
	if claims.CurrentTenantID != uuid.Nil {
		t.Errorf("current_tenant_id non-zero on client_credentials token: %v", claims.CurrentTenantID)
	}

	// Cleanup: remove the row so test runs are idempotent. The
	// table has no DELETE policy under RLS but is also NOT
	// RLS-enabled — admin_pool / atlas_app with explicit DELETE
	// grant (in v1 we did NOT grant DELETE; use UPDATE
	// disabled_at instead) suffices to soft-disable. The
	// integration test leaves the row; the unique name guarantees
	// idempotent reruns.
}

// TestIntegrationClientCredentials_InvalidSecretRejected covers
// AC-7 + P0-188-3: wrong secret returns 401 invalid_client. The
// plaintext is never echoed back.
func TestIntegrationClientCredentials_InvalidSecretRejected(t *testing.T) {
	pool := openTokenIntegrationPool(t)
	srv, _ := newIntegrationServer(t, pool)

	store := oauthclient.New(pool)
	client, _, err := store.Issue(context.Background(), uniqueName(t, "it-cc-bad"))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	form := url.Values{}
	form.Set("grant_type", oauth.GrantTypeClientCredentials)
	form.Set("client_id", client.ClientID)
	form.Set("client_secret", "wrong-secret-value")

	resp, body := postForm(t, srv.URL, form)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d body = %s want 401", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "invalid_client") {
		t.Errorf("body = %q want invalid_client", body)
	}
	if strings.Contains(string(body), "wrong-secret-value") {
		t.Fatal("plaintext secret echoed back in error response (P0-188-3 violation)")
	}
}

// TestIntegrationTokenExchange_CrossTenantRejected covers AC-13
// (LOAD-BEARING — the failure mode this slice is named after). A
// JWT issued for tenant A cannot be exchanged for tenant B unless
// B is in available_tenants OR super_admin=true. This is the
// integration-level proof of the load-bearing primitive that slice
// 192's frontend tenant-switcher depends on.
func TestIntegrationTokenExchange_CrossTenantRejected(t *testing.T) {
	pool := openTokenIntegrationPool(t)
	srv, signer := newIntegrationServer(t, pool)

	tenantA := uuid.New()
	tenantB := uuid.New()
	// Subject token: available_tenants = [A]; super_admin = false;
	// current_tenant = A. Caller tries to exchange for B.
	subjectTok, err := signer.Sign(context.Background(), jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    testIssuer,
			Subject:   "user:it-cross",
			Audience:  []string{testIssuer},
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
			IssuedAt:  time.Now().Unix(),
			ID:        "jti-it-cross-" + uuid.NewString(),
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
		t.Fatalf("status = %d body = %s want 403", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "invalid_target") {
		t.Errorf("body = %q want invalid_target", body)
	}
}

// TestIntegrationTokenExchange_AllowlistedExchangeWritesAudit covers
// AC-12 (positive case) + AC-16 + AC-17: when the target tenant IS
// in available_tenants, the exchange succeeds AND an audit row
// lands in oauth_token_exchanges scoped to the target tenant.
func TestIntegrationTokenExchange_AllowlistedExchangeWritesAudit(t *testing.T) {
	pool := openTokenIntegrationPool(t)
	srv, signer := newIntegrationServer(t, pool)

	tenantA := uuid.New()
	tenantB := uuid.New()
	jti := "jti-it-allow-" + uuid.NewString()
	subjectTok, err := signer.Sign(context.Background(), jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    testIssuer,
			Subject:   "user:it-allow",
			Audience:  []string{testIssuer},
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
			IssuedAt:  time.Now().Unix(),
			ID:        jti,
		},
		CurrentTenantID:  tenantA,
		AvailableTenants: []uuid.UUID{tenantA, tenantB},
		Roles:            map[uuid.UUID][]string{tenantA: {"admin"}, tenantB: {"reader"}},
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
		t.Fatalf("status = %d body = %s want 200", resp.StatusCode, body)
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	mintedClaims, err := signer.Verify(context.Background(), out.AccessToken)
	if err != nil {
		t.Fatalf("verify minted: %v", err)
	}
	if mintedClaims.CurrentTenantID != tenantB {
		t.Errorf("current_tenant_id = %v want %v", mintedClaims.CurrentTenantID, tenantB)
	}

	// Audit log assertion: a row should exist under tenant B with
	// the jti of the subject token. Read via the same atlas_app
	// pool that wrote it; apply the tenant GUC so RLS lets us
	// see the row.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Allow ~1s for the best-effort write to land (D3 — async).
	deadline := time.Now().Add(2 * time.Second)
	var (
		gotJTI        string
		gotFromTenant *uuid.UUID
		gotToTenant   uuid.UUID
		gotSubjectIss string
		gotSubjectSub string
		found         bool
	)
	for time.Now().Before(deadline) {
		tx, err := pool.BeginTx(ctx, pgxRO())
		if err != nil {
			t.Fatalf("BeginTx: %v", err)
		}
		if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenantB.String()); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("set_config: %v", err)
		}
		row := tx.QueryRow(ctx, `
			SELECT subject_token_jti, from_tenant_id, to_tenant_id,
			       subject_token_iss, subject_token_sub
			FROM oauth_token_exchanges
			WHERE subject_token_jti = $1
			LIMIT 1
		`, jti)
		err = row.Scan(&gotJTI, &gotFromTenant, &gotToTenant, &gotSubjectIss, &gotSubjectSub)
		_ = tx.Rollback(ctx)
		if err == nil {
			found = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !found {
		t.Fatal("oauth_token_exchanges row never landed for the subject token's jti")
	}
	if gotJTI != jti {
		t.Errorf("jti = %q want %q", gotJTI, jti)
	}
	if gotFromTenant == nil || *gotFromTenant != tenantA {
		t.Errorf("from_tenant_id = %v want %v", gotFromTenant, tenantA)
	}
	if gotToTenant != tenantB {
		t.Errorf("to_tenant_id = %v want %v", gotToTenant, tenantB)
	}
	if gotSubjectIss != testIssuer {
		t.Errorf("subject_iss = %q want %q", gotSubjectIss, testIssuer)
	}
	if gotSubjectSub != "user:it-allow" {
		t.Errorf("subject_sub = %q want user:it-allow", gotSubjectSub)
	}
}

// TestIntegrationTokenExchange_AuditRLSIsolation covers constitutional
// invariant #6 + P0-188-8: the audit log row is RLS-isolated to its
// target tenant. A different tenant's connection MUST NOT see the row.
func TestIntegrationTokenExchange_AuditRLSIsolation(t *testing.T) {
	pool := openTokenIntegrationPool(t)
	srv, signer := newIntegrationServer(t, pool)

	tenantA := uuid.New()
	tenantB := uuid.New()
	tenantC := uuid.New() // unrelated tenant — MUST NOT see B's audit rows
	jti := "jti-it-rls-" + uuid.NewString()
	subjectTok, err := signer.Sign(context.Background(), jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    testIssuer,
			Subject:   "user:it-rls",
			Audience:  []string{testIssuer},
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
			IssuedAt:  time.Now().Unix(),
			ID:        jti,
		},
		CurrentTenantID:  tenantA,
		AvailableTenants: []uuid.UUID{tenantA, tenantB},
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	form := url.Values{}
	form.Set("grant_type", oauth.GrantTypeTokenExchange)
	form.Set("subject_token", subjectTok)
	form.Set("subject_token_type", oauth.SubjectTokenTypeJWT)
	form.Set("atlas:target_tenant_id", tenantB.String())
	resp, _ := postForm(t, srv.URL, form)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("exchange failed: %d", resp.StatusCode)
	}

	// Tenant C reads — RLS MUST hide the row.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Allow the async audit-write to land.
	time.Sleep(300 * time.Millisecond)
	tx, err := pool.BeginTx(ctx, pgxRO())
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenantC.String()); err != nil {
		t.Fatalf("set_config: %v", err)
	}
	var count int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM oauth_token_exchanges WHERE subject_token_jti = $1
	`, jti).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("tenant C saw %d audit rows for jti=%q; RLS isolation broken", count, jti)
	}
}

// TestIntegrationOAuthClients_DuplicateNameRejected covers AC-2 +
// AC-3: a second Issue call with the same name returns
// ErrDuplicateName.
func TestIntegrationOAuthClients_DuplicateNameRejected(t *testing.T) {
	pool := openTokenIntegrationPool(t)
	store := oauthclient.New(pool)

	name := uniqueName(t, "it-dup")
	if _, _, err := store.Issue(context.Background(), name); err != nil {
		t.Fatalf("first Issue: %v", err)
	}
	_, _, err := store.Issue(context.Background(), name)
	if err == nil {
		t.Fatal("second Issue with same name succeeded; expected ErrDuplicateName")
	}
	if err.Error() != "oauthclient: client with that name already exists" {
		t.Errorf("error = %q want ErrDuplicateName", err)
	}
}

// pgxRO returns read-only TxOptions for the audit-log assertions.
// Read-only transactions still trigger RLS evaluation, so the GUC
// + the tenant_read policy gate apply.
func pgxRO() pgx.TxOptions { return pgx.TxOptions{AccessMode: pgx.ReadOnly} }
