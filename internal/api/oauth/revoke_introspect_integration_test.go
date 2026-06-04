//go:build integration

package oauth_test

import (
	"context"
	"encoding/json"
	"io"
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
	"github.com/mgoodric/security-atlas/internal/auth/oauthclient"
	"github.com/mgoodric/security-atlas/internal/auth/revocation"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// postFormAt POSTs to a fully-qualified URL (no PathToken suffix),
// returning the response + body. The existing postForm helper in
// token_test.go hard-codes `+oauth.PathToken` onto the supplied URL;
// slice 190 needs to hit /oauth/revoke + /oauth/introspect so a
// local helper is required.
func postFormAt(t *testing.T, urlStr string, form url.Values, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, urlStr, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}

// newRevokeIntrospectServer wires the slice-190 surface end to end:
// fsstore keystore + atlas_app pool-backed oauthclient.Store +
// revocation.Store + RevocationEndpoint + IntrospectionEndpoint, all
// behind the Handler.Mount chi router. The test mints a JWT via the
// returned signer and exercises POST /oauth/revoke + POST
// /oauth/introspect.
func newRevokeIntrospectServer(t *testing.T) (*httptest.Server, *tokensign.Signer, *revocation.Store, *oauthclient.Store, string) {
	t.Helper()
	pool := openTokenIntegrationPool(t)
	ks, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	signer := tokensign.New(ks)
	clients := oauthclient.New(pool)
	revoked := revocation.New(pool)

	revokeEP := oauth.NewRevocationEndpoint(signer, revoked, clients, oauth.RevocationEndpointConfig{
		Issuer: testIssuer,
	})
	introspectEP := oauth.NewIntrospectionEndpoint(signer, revoked, clients, oauth.IntrospectionEndpointConfig{
		Issuer: testIssuer,
	})

	h := oauth.New(ks, oauth.Config{Issuer: testIssuer})
	h.AttachRevocationEndpoint(revokeEP)
	h.AttachIntrospectionEndpoint(introspectEP)

	r := newRouter(h)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	// Issue a client_credentials client for the inspector + revoker
	// auth path.
	name := uniqueName(t, "it-rev")
	_, secret, err := clients.Issue(context.Background(), name)
	if err != nil {
		t.Fatalf("Issue client: %v", err)
	}
	// Issue returns the client with ClientID; we need to look it up.
	// Reuse the name via a fresh Issue isn't right — use the
	// returned client. Adapt:
	return srv, signer, revoked, clients, name + ":" + secret
}

// mintJWTForTest produces a JWT scoped to the supplied tenant.
func mintJWTForTest(t *testing.T, signer *tokensign.Signer, sub string) (string, jwt.AtlasClaims) {
	t.Helper()
	tenantID := uuid.New()
	claims := jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    testIssuer,
			Subject:   sub,
			Audience:  []string{testIssuer},
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
			IssuedAt:  time.Now().Unix(),
			NotBefore: time.Now().Add(-time.Minute).Unix(),
			ID:        uuid.NewString(),
		},
		IDPIssuer:        "test-idp",
		CurrentTenantID:  tenantID,
		AvailableTenants: []uuid.UUID{tenantID},
		Roles:            map[uuid.UUID][]string{tenantID: {"admin"}},
		SuperAdmin:       false,
	}
	tok, err := signer.Sign(context.Background(), claims)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return tok, claims
}

// issueClientForTest issues a fresh oauth_clients row + returns the
// (clientID, plaintextSecret) tuple the inspector/revoker test path
// uses for Basic-header auth.
func issueClientForTest(t *testing.T, clients *oauthclient.Store) (string, string) {
	t.Helper()
	name := uniqueName(t, "it-cli")
	client, secret, err := clients.Issue(context.Background(), name)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	return client.ClientID, secret
}

// TestIntegrationIntrospect_ActiveToken: a valid token returns
// {active: true, ...claims}.
func TestIntegrationIntrospect_ActiveToken(t *testing.T) {
	srv, signer, _, clients, _ := newRevokeIntrospectServer(t)
	clientID, clientSecret := issueClientForTest(t, clients)

	tok, _ := mintJWTForTest(t, signer, "user:alice-introspect-active")

	form := url.Values{}
	form.Set("token", tok)
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)

	resp, body := postFormAt(t, srv.URL+oauth.PathIntrospect, form, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if act, _ := out["active"].(bool); !act {
		t.Fatalf("active = %v, want true; body = %s", out["active"], body)
	}
	if out["sub"] != "user:alice-introspect-active" {
		t.Fatalf("sub = %v, want user:alice-introspect-active", out["sub"])
	}
}

// TestIntegrationIntrospect_RevokedToken_Inactive: AC-21 / P0-190-5.
// A revoked token must return {active: false}, NOT 401.
func TestIntegrationIntrospect_RevokedToken_Inactive(t *testing.T) {
	srv, signer, revoked, clients, _ := newRevokeIntrospectServer(t)
	clientID, clientSecret := issueClientForTest(t, clients)

	tok, claims := mintJWTForTest(t, signer, "user:alice-introspect-rev")

	// Pre-revoke.
	if err := revoked.Revoke(context.Background(), claims.ID,
		time.Unix(claims.ExpiresAt, 0), "user:test", ""); err != nil {
		t.Fatalf("pre-revoke: %v", err)
	}

	form := url.Values{}
	form.Set("token", tok)
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)

	resp, body := postFormAt(t, srv.URL+oauth.PathIntrospect, form, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s — must be 200 even for revoked (P0-190-5)", resp.StatusCode, body)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if act, _ := out["active"].(bool); act {
		t.Fatalf("active = true for revoked token; want false")
	}
}

// TestIntegrationIntrospect_UnauthInspector_Returns401: a missing
// client_credentials must return 401 — NOT a free oracle on token
// validity.
func TestIntegrationIntrospect_UnauthInspector_Returns401(t *testing.T) {
	srv, signer, _, _, _ := newRevokeIntrospectServer(t)
	tok, _ := mintJWTForTest(t, signer, "user:alice-noauth")

	form := url.Values{}
	form.Set("token", tok)
	// NO client_id / client_secret.

	resp, body := postFormAt(t, srv.URL+oauth.PathIntrospect, form, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d body = %s, want 401", resp.StatusCode, body)
	}
}

// TestIntegrationIntrospect_ExpiredToken_Inactive (slice 422, AC-4):
// an AUTHENTICATED inspector introspecting an EXPIRED but
// validly-signed and un-revoked token MUST get 200 + {active:false} —
// NOT 401 and NOT active:true. The deny here is the temporal
// claim-validation arm (introspect.go jwt.Validate → writeInactive):
// a regression that reported an expired token as active would let a
// stale token pass a resource server's introspection check, a privilege
// extension past the token's intended lifetime.
func TestIntegrationIntrospect_ExpiredToken_Inactive(t *testing.T) {
	srv, signer, _, clients, _ := newRevokeIntrospectServer(t)
	clientID, clientSecret := issueClientForTest(t, clients)

	// Mint a token whose exp is in the past (signature still valid).
	tenantID := uuid.New()
	expiredTok, err := signer.Sign(context.Background(), jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    testIssuer,
			Subject:   "user:alice-introspect-expired",
			Audience:  []string{testIssuer},
			ExpiresAt: time.Now().Add(-time.Hour).Unix(),
			IssuedAt:  time.Now().Add(-2 * time.Hour).Unix(),
			NotBefore: time.Now().Add(-2 * time.Hour).Unix(),
			ID:        uuid.NewString(),
		},
		CurrentTenantID:  tenantID,
		AvailableTenants: []uuid.UUID{tenantID},
	})
	if err != nil {
		t.Fatalf("Sign expired: %v", err)
	}

	form := url.Values{}
	form.Set("token", expiredTok)
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)

	resp, body := postFormAt(t, srv.URL+oauth.PathIntrospect, form, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s — expired must be 200+{active:false}, not %d", resp.StatusCode, body, resp.StatusCode)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if act, _ := out["active"].(bool); act {
		t.Fatalf("active = true for EXPIRED token; want false (body %s)", body)
	}
}

// TestIntegrationIntrospect_BadSignatureToken_Inactive (slice 422,
// AC-4): an AUTHENTICATED inspector introspecting a token whose
// signature does NOT verify against the AS keystore MUST get 200 +
// {active:false}. The deny is the signature-verification arm
// (introspect.go signer.Verify → writeInactive). A forged token must
// never report active.
func TestIntegrationIntrospect_BadSignatureToken_Inactive(t *testing.T) {
	srv, _, _, clients, _ := newRevokeIntrospectServer(t)
	clientID, clientSecret := issueClientForTest(t, clients)

	// Mint a token under a DIFFERENT keystore so the server's signer
	// cannot verify it.
	otherKS, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	otherSigner := tokensign.New(otherKS)
	tenantID := uuid.New()
	foreignTok, err := otherSigner.Sign(context.Background(), jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    testIssuer,
			Subject:   "user:forged",
			Audience:  []string{testIssuer},
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
			IssuedAt:  time.Now().Unix(),
			ID:        uuid.NewString(),
		},
		CurrentTenantID: tenantID,
	})
	if err != nil {
		t.Fatalf("Sign foreign: %v", err)
	}

	form := url.Values{}
	form.Set("token", foreignTok)
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)

	resp, body := postFormAt(t, srv.URL+oauth.PathIntrospect, form, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s — bad-signature must be 200+{active:false}", resp.StatusCode, body)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if act, _ := out["active"].(bool); act {
		t.Fatalf("active = true for forged-signature token; want false (body %s)", body)
	}
}

// TestIntegrationRevoke_HappyPath: AC-13..AC-17. client_credentials
// caller revokes a token; subsequent introspection returns inactive.
func TestIntegrationRevoke_HappyPath(t *testing.T) {
	srv, signer, _, clients, _ := newRevokeIntrospectServer(t)
	clientID, clientSecret := issueClientForTest(t, clients)

	tok, _ := mintJWTForTest(t, signer, "user:alice-revoke-happy")

	revokeForm := url.Values{}
	revokeForm.Set("token", tok)
	revokeForm.Set("client_id", clientID)
	revokeForm.Set("client_secret", clientSecret)

	resp, body := postFormAt(t, srv.URL+oauth.PathRevoke, revokeForm, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("revoke status = %d body = %s", resp.StatusCode, body)
	}

	// Subsequent introspection returns inactive.
	introspectForm := url.Values{}
	introspectForm.Set("token", tok)
	introspectForm.Set("client_id", clientID)
	introspectForm.Set("client_secret", clientSecret)

	resp, body = postFormAt(t, srv.URL+oauth.PathIntrospect, introspectForm, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("introspect status = %d", resp.StatusCode)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if act, _ := out["active"].(bool); act {
		t.Fatalf("active = true after revoke; want false")
	}
}

// TestIntegrationRevoke_UnknownToken_Returns200: AC-17 / P0-190-4 —
// RFC 7009 §2.2 silent 200 for unknown tokens.
func TestIntegrationRevoke_UnknownToken_Returns200(t *testing.T) {
	srv, _, _, clients, _ := newRevokeIntrospectServer(t)
	clientID, clientSecret := issueClientForTest(t, clients)

	form := url.Values{}
	// Garbage token; signature won't verify.
	form.Set("token", "eyJ.totally.bogus")
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)

	resp, body := postFormAt(t, srv.URL+oauth.PathRevoke, form, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200 (RFC 7009 §2.2)", resp.StatusCode, body)
	}
}

// TestIntegrationRevoke_NoAuth_Returns401: a revoke call with no
// client_credentials AND no Bearer must return 401.
func TestIntegrationRevoke_NoAuth_Returns401(t *testing.T) {
	srv, signer, _, _, _ := newRevokeIntrospectServer(t)
	tok, _ := mintJWTForTest(t, signer, "user:alice-noauth-rev")

	form := url.Values{}
	form.Set("token", tok)

	resp, body := postFormAt(t, srv.URL+oauth.PathRevoke, form, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d body = %s, want 401", resp.StatusCode, body)
	}
}

// TestIntegrationRevoke_SelfRevocation: AC-16 — a user can revoke
// their OWN token by presenting the JWT as Authorization Bearer.
func TestIntegrationRevoke_SelfRevocation(t *testing.T) {
	srv, signer, _, clients, _ := newRevokeIntrospectServer(t)

	tok, _ := mintJWTForTest(t, signer, "user:alice-self-revoke")

	form := url.Values{}
	form.Set("token", tok)
	body := strings.NewReader(form.Encode())

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+oauth.PathRevoke, body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (self-revoke)", resp.StatusCode)
	}

	// Confirm via introspection (need client_credentials).
	clientID, clientSecret := issueClientForTest(t, clients)
	introspectForm := url.Values{}
	introspectForm.Set("token", tok)
	introspectForm.Set("client_id", clientID)
	introspectForm.Set("client_secret", clientSecret)
	resp2, body2 := postFormAt(t, srv.URL+oauth.PathIntrospect, introspectForm, nil)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("introspect status = %d", resp2.StatusCode)
	}
	var out map[string]any
	if err := json.Unmarshal(body2, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if act, _ := out["active"].(bool); act {
		t.Fatalf("active = true after self-revoke; want false")
	}
}

// TestIntegrationRevoke_SelfRevocation_RejectOtherUser: a user MUST
// NOT be able to revoke someone else's token via the self-revoke
// path. The handler must reject the call with 401.
func TestIntegrationRevoke_SelfRevocation_RejectOtherUser(t *testing.T) {
	srv, signer, _, _, _ := newRevokeIntrospectServer(t)

	// Alice's token + Bob's bearer.
	aliceTok, _ := mintJWTForTest(t, signer, "user:alice")
	bobTok, _ := mintJWTForTest(t, signer, "user:bob")

	form := url.Values{}
	form.Set("token", aliceTok)
	body := strings.NewReader(form.Encode())
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+oauth.PathRevoke, body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+bobTok)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (cross-user revoke rejected)", resp.StatusCode)
	}
}

// ensure tenancy package imported for the integration server signature
var _ = tenancy.GUCName
