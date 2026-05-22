//go:build integration

// authorize_integration_test.go — slice 189 integration tests against
// real Postgres.
//
// Run via: `just test-integration` (which sets DATABASE_URL_APP +
// runs migrations). The test file builds only when the `integration`
// build tag is set; CI's "Go · integration (Postgres RLS)" job
// supplies it.
//
// Coverage:
//
//   - AC-20 / AC-44: full GET /oauth/authorize → 302 redirect happy
//     path, with an `oauth_auth_codes` row inserted.
//   - AC-21 / AC-45: authorization_code redemption happy path.
//   - AC-27 / AC-45: code reuse rejection.
//   - AC-29 / AC-45: PKCE verifier mismatch rejection.
//   - AC-28 / AC-45: redirect_uri mismatch rejection.
//   - P0-189-2: unregistered redirect_uri rejection at authorize-time.
//   - P0-189-1: PKCE plain method rejection.

package oauth_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/oauth"
	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/keystore/fsstore"
	"github.com/mgoodric/security-atlas/internal/auth/oauthclient"
	"github.com/mgoodric/security-atlas/internal/auth/oauthcode"
	"github.com/mgoodric/security-atlas/internal/auth/sessions"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
)

// stub session resolver that doesn't read from the slice-034 sessions
// table — the integration test seeds a session record directly via
// this stub so we don't need to bootstrap a full OIDC login flow.
type itSessionResolver struct {
	sess sessions.Session
}

func (s *itSessionResolver) Read(_ context.Context, _ uuid.UUID, id string) (sessions.Session, error) {
	if id != s.sess.ID {
		return sessions.Session{}, fmt.Errorf("session not found")
	}
	return s.sess, nil
}

// stub user resolver returning a deterministic identity for the test.
type itUserResolver struct {
	identity oauth.UserIdentity
}

func (s *itUserResolver) ResolveForOAuth(_ context.Context, _ uuid.UUID, _ uuid.UUID) (oauth.UserIdentity, error) {
	return s.identity, nil
}

// newAuthorizeIntegrationServer wires the full slice-189 surface
// against a real Postgres pool. Stubs session + user resolvers so the
// test doesn't depend on a real OIDC login.
func newAuthorizeIntegrationServer(
	t *testing.T,
	pool *pgxpool.Pool,
	sess sessions.Session,
	identity oauth.UserIdentity,
) (*httptest.Server, *tokensign.Signer, *oauthcode.Store) {
	t.Helper()
	ks, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	signer := tokensign.New(ks)
	clients := oauthclient.New(pool)
	codes := oauthcode.New(pool)
	ep := oauth.NewTokenEndpoint(signer, clients, oauth.TokenEndpointConfig{
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
	return srv, signer, codes
}

// helper: register a redirect URI + return the client we created.
func setupAuthorizeFixture(t *testing.T, pool *pgxpool.Pool, redirectURI string) (clientID, secret string, sess sessions.Session, identity oauth.UserIdentity) {
	t.Helper()
	clients := oauthclient.New(pool)
	c, sec, err := clients.Issue(context.Background(), uniqueName(t, "it-auth"))
	if err != nil {
		t.Fatalf("Issue client: %v", err)
	}
	codes := oauthcode.New(pool)
	if err := codes.RegisterRedirectURI(context.Background(), c.ClientID, redirectURI); err != nil {
		t.Fatalf("RegisterRedirectURI: %v", err)
	}
	userID := uuid.New()
	tenantID := uuid.New()
	sess = sessions.Session{
		ID:         "stub-session-" + uuid.NewString(),
		TenantID:   tenantID,
		UserID:     userID,
		IdpIssuer:  "https://idp.example.test",
		IdpSubject: "subject-" + userID.String()[:8],
		ExpiresAt:  time.Now().Add(time.Hour),
	}
	identity = oauth.UserIdentity{
		UserID:           userID,
		CurrentTenantID:  tenantID,
		AvailableTenants: []uuid.UUID{tenantID},
		Roles:            map[uuid.UUID][]string{tenantID: {"grc_engineer"}},
		SuperAdmin:       false,
	}
	return c.ClientID, sec, sess, identity
}

// TestIntegrationAuthorizeFlow_HappyPath — AC-20 + AC-44.
func TestIntegrationAuthorizeFlow_HappyPath(t *testing.T) {
	pool := openTokenIntegrationPool(t)
	const redirectURI = "https://atlas.example.test/oauth/callback"
	clientID, _, sess, identity := setupAuthorizeFixture(t, pool, redirectURI)
	srv, _, _ := newAuthorizeIntegrationServer(t, pool, sess, identity)

	verifier := "atlas-test-verifier-" + uuid.NewString()
	challenge := oauth.ExportComputePKCEChallengeS256(verifier)
	state := "state-" + uuid.NewString()[:8]
	u := authorizeURL(srv.URL, map[string]string{
		"response_type":         "code",
		"client_id":             clientID,
		"redirect_uri":          redirectURI,
		"scope":                 "openid",
		"state":                 state,
		"code_challenge":        challenge,
		"code_challenge_method": "S256",
		"tenant_id":             identity.CurrentTenantID.String(),
	})
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.AddCookie(&http.Cookie{Name: sessions.CookieName, Value: sess.ID})
	resp := doNoFollow(t, req)
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302; location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatalf("redirect missing code; location=%q", resp.Header.Get("Location"))
	}
	if got := loc.Query().Get("state"); got != state {
		t.Errorf("state echo = %q want %q", got, state)
	}
}

// TestIntegrationAuthorizeFlow_PlainPKCERejected — P0-189-1.
func TestIntegrationAuthorizeFlow_PlainPKCERejected(t *testing.T) {
	pool := openTokenIntegrationPool(t)
	const redirectURI = "https://atlas.example.test/oauth/callback"
	clientID, _, sess, identity := setupAuthorizeFixture(t, pool, redirectURI)
	srv, _, _ := newAuthorizeIntegrationServer(t, pool, sess, identity)

	u := authorizeURL(srv.URL, map[string]string{
		"response_type":         "code",
		"client_id":             clientID,
		"redirect_uri":          redirectURI,
		"code_challenge":        "raw-verifier",
		"code_challenge_method": "plain",
		"tenant_id":             identity.CurrentTenantID.String(),
	})
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.AddCookie(&http.Cookie{Name: sessions.CookieName, Value: sess.ID})
	resp := doNoFollow(t, req)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (plain method rejected)", resp.StatusCode)
	}
}

// TestIntegrationAuthorizeFlow_UnregisteredRedirectURIRejected — P0-189-2.
func TestIntegrationAuthorizeFlow_UnregisteredRedirectURIRejected(t *testing.T) {
	pool := openTokenIntegrationPool(t)
	const redirectURI = "https://atlas.example.test/oauth/callback"
	clientID, _, sess, identity := setupAuthorizeFixture(t, pool, redirectURI)
	srv, _, _ := newAuthorizeIntegrationServer(t, pool, sess, identity)

	verifier := "atlas-test-verifier-" + uuid.NewString()
	challenge := oauth.ExportComputePKCEChallengeS256(verifier)
	u := authorizeURL(srv.URL, map[string]string{
		"response_type":         "code",
		"client_id":             clientID,
		"redirect_uri":          "https://attacker.example.test/steal", // NOT registered
		"code_challenge":        challenge,
		"code_challenge_method": "S256",
		"tenant_id":             identity.CurrentTenantID.String(),
	})
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.AddCookie(&http.Cookie{Name: sessions.CookieName, Value: sess.ID})
	resp := doNoFollow(t, req)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (unregistered URI rejected)", resp.StatusCode)
	}
	// CRITICAL: the response must NOT be a 302 redirect to the
	// attacker URI — that's the open-redirect vulnerability P0-189-2
	// prevents.
	if loc := resp.Header.Get("Location"); loc != "" {
		t.Errorf("UNREGISTERED URI got Location header: %q (open-redirect leak!)", loc)
	}
}

// TestIntegrationAuthorizeCodeRedemption_FullFlow — AC-45 happy path.
func TestIntegrationAuthorizeCodeRedemption_FullFlow(t *testing.T) {
	pool := openTokenIntegrationPool(t)
	const redirectURI = "https://atlas.example.test/oauth/callback"
	clientID, _, sess, identity := setupAuthorizeFixture(t, pool, redirectURI)
	srv, signer, _ := newAuthorizeIntegrationServer(t, pool, sess, identity)

	verifier := "atlas-test-verifier-" + uuid.NewString()
	challenge := oauth.ExportComputePKCEChallengeS256(verifier)
	// Step 1: authorize → get code
	code := mustAuthorize(t, srv.URL, clientID, redirectURI, challenge, sess, identity)

	// Step 2: redeem via token endpoint
	form := url.Values{}
	form.Set("grant_type", oauth.GrantTypeAuthorizationCode)
	form.Set("code", code)
	form.Set("code_verifier", verifier)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", clientID)
	resp, body := postForm(t, srv.URL, form)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("token status = %d body=%s", resp.StatusCode, body)
	}
	var out struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.AccessToken == "" {
		t.Fatal("no access_token in response")
	}
	if out.TokenType != "Bearer" {
		t.Errorf("token_type = %q want Bearer", out.TokenType)
	}
	// Verify the minted JWT carries the user's identity.
	claims, err := signer.Verify(context.Background(), out.AccessToken)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	wantSub := "user:" + identity.UserID.String()
	if claims.Subject != wantSub {
		t.Errorf("sub = %q want %q", claims.Subject, wantSub)
	}
	if claims.CurrentTenantID != identity.CurrentTenantID {
		t.Errorf("current_tenant_id = %v want %v", claims.CurrentTenantID, identity.CurrentTenantID)
	}
}

// TestIntegrationAuthorizeCodeRedemption_PKCEMismatch — AC-29.
func TestIntegrationAuthorizeCodeRedemption_PKCEMismatch(t *testing.T) {
	pool := openTokenIntegrationPool(t)
	const redirectURI = "https://atlas.example.test/oauth/callback"
	clientID, _, sess, identity := setupAuthorizeFixture(t, pool, redirectURI)
	srv, _, _ := newAuthorizeIntegrationServer(t, pool, sess, identity)

	verifier := "atlas-test-verifier-" + uuid.NewString()
	challenge := oauth.ExportComputePKCEChallengeS256(verifier)
	code := mustAuthorize(t, srv.URL, clientID, redirectURI, challenge, sess, identity)

	form := url.Values{}
	form.Set("grant_type", oauth.GrantTypeAuthorizationCode)
	form.Set("code", code)
	form.Set("code_verifier", "wrong-verifier-value")
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", clientID)
	resp, body := postForm(t, srv.URL, form)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s want 400", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "invalid_grant") {
		t.Errorf("body = %q want invalid_grant", body)
	}
}

// TestIntegrationAuthorizeCodeRedemption_CodeReuse — AC-27.
func TestIntegrationAuthorizeCodeRedemption_CodeReuse(t *testing.T) {
	pool := openTokenIntegrationPool(t)
	const redirectURI = "https://atlas.example.test/oauth/callback"
	clientID, _, sess, identity := setupAuthorizeFixture(t, pool, redirectURI)
	srv, _, _ := newAuthorizeIntegrationServer(t, pool, sess, identity)

	verifier := "atlas-test-verifier-" + uuid.NewString()
	challenge := oauth.ExportComputePKCEChallengeS256(verifier)
	code := mustAuthorize(t, srv.URL, clientID, redirectURI, challenge, sess, identity)

	form := url.Values{}
	form.Set("grant_type", oauth.GrantTypeAuthorizationCode)
	form.Set("code", code)
	form.Set("code_verifier", verifier)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", clientID)
	// First redemption succeeds.
	resp1, body1 := postForm(t, srv.URL, form)
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("first redemption status = %d body=%s", resp1.StatusCode, body1)
	}
	// Second redemption MUST fail.
	resp2, body2 := postForm(t, srv.URL, form)
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("second redemption status = %d body=%s want 400", resp2.StatusCode, body2)
	}
	if !strings.Contains(string(body2), "invalid_grant") {
		t.Errorf("body = %q want invalid_grant", body2)
	}
}

// TestIntegrationAuthorizeCodeRedemption_RedirectURIMismatch — AC-28.
func TestIntegrationAuthorizeCodeRedemption_RedirectURIMismatch(t *testing.T) {
	pool := openTokenIntegrationPool(t)
	const redirectURI = "https://atlas.example.test/oauth/callback"
	const altURI = "https://atlas.example.test/different-callback"
	clientID, _, sess, identity := setupAuthorizeFixture(t, pool, redirectURI)
	// Register the alt URI too — so the redemption mismatch is the
	// only failure mode (not "unregistered URI").
	codes := oauthcode.New(pool)
	if err := codes.RegisterRedirectURI(context.Background(), clientID, altURI); err != nil {
		t.Fatalf("RegisterRedirectURI alt: %v", err)
	}
	srv, _, _ := newAuthorizeIntegrationServer(t, pool, sess, identity)

	verifier := "atlas-test-verifier-" + uuid.NewString()
	challenge := oauth.ExportComputePKCEChallengeS256(verifier)
	code := mustAuthorize(t, srv.URL, clientID, redirectURI, challenge, sess, identity)

	form := url.Values{}
	form.Set("grant_type", oauth.GrantTypeAuthorizationCode)
	form.Set("code", code)
	form.Set("code_verifier", verifier)
	form.Set("redirect_uri", altURI) // different from authorize-time
	form.Set("client_id", clientID)
	resp, body := postForm(t, srv.URL, form)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s want 400", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "invalid_grant") {
		t.Errorf("body = %q want invalid_grant", body)
	}
}

// ---- helpers ----

func authorizeURL(base string, params map[string]string) string {
	u := base + "/oauth/authorize"
	q := url.Values{}
	for k, v := range params {
		q.Set(k, v)
	}
	return u + "?" + q.Encode()
}

// doNoFollow performs the request without following redirects. We
// need to inspect the 302 location.
func doNoFollow(t *testing.T, req *http.Request) *http.Response {
	t.Helper()
	cli := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := cli.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	return resp
}

func mustAuthorize(t *testing.T, base, clientID, redirectURI, challenge string, sess sessions.Session, identity oauth.UserIdentity) string {
	t.Helper()
	state := "s-" + uuid.NewString()[:8]
	u := authorizeURL(base, map[string]string{
		"response_type":         "code",
		"client_id":             clientID,
		"redirect_uri":          redirectURI,
		"state":                 state,
		"code_challenge":        challenge,
		"code_challenge_method": "S256",
		"tenant_id":             identity.CurrentTenantID.String(),
	})
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.AddCookie(&http.Cookie{Name: sessions.CookieName, Value: sess.ID})
	resp := doNoFollow(t, req)
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("authorize status = %d want 302; loc=%q", resp.StatusCode, resp.Header.Get("Location"))
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatalf("no code in redirect: %q", resp.Header.Get("Location"))
	}
	return code
}

var _ = jwt.AtlasClaims{} // keep import used
