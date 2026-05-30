// revoke_test.go — slice 314 unit coverage for the slice-190
// `POST /oauth/revoke` endpoint.
//
// RFC: RFC 7009 (OAuth 2.0 Token Revocation), §2.1 (request) + §2.2
// (response — silent 200 for unknown tokens; 401 only on caller-auth
// failure). Load-bearing functions under test:
// RevocationEndpoint.ServeHTTP + authenticate + extractBearerRaw (the
// pre-DB request validation + the self-revocation-via-bearer
// authentication branches).
//
// Scope discipline: the self-revoke authentication path (RFC 7009
// §2.1 client/self auth) is fully exercisable WITHOUT Postgres — it
// runs signer.Verify (keystore, no DB) + jwt.Validate (no DB) and
// rejects on a subject mismatch BEFORE the revocation.Store.Revoke DB
// write. The successful-revocation write + the client_credentials
// auth-success path need a DB-backed oauthclient.Store +
// revocation.Store and are covered by
// revoke_introspect_integration_test.go (now enrolled in CI per this
// slice).

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
	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/keystore/fsstore"
	"github.com/mgoodric/security-atlas/internal/auth/oauthclient"
	"github.com/mgoodric/security-atlas/internal/auth/revocation"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
)

// newRevokeUnitServer wires a RevocationEndpoint with nil-pool stores.
// The pre-DB branches (content-type, missing token) + the
// self-revoke-bearer auth-rejection branches run without touching the
// pool. The signer is real (fsstore-backed) so caller/target JWTs can
// be minted + verified in-process.
func newRevokeUnitServer(t *testing.T) (*httptest.Server, *tokensign.Signer) {
	t.Helper()
	ks, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	signer := tokensign.New(ks)
	ep := oauth.NewRevocationEndpoint(
		signer,
		revocation.New(nil),
		oauthclient.New(nil),
		oauth.RevocationEndpointConfig{Issuer: testIssuer},
	)
	h := oauth.New(ks, oauth.Config{Issuer: testIssuer})
	h.AttachRevocationEndpoint(ep)
	srv := httptest.NewServer(routerFor(h))
	t.Cleanup(srv.Close)
	return srv, signer
}

func postRevoke(t *testing.T, srvURL string, form url.Values, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srvURL+oauth.PathRevoke, strings.NewReader(form.Encode()))
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
	buf := make([]byte, 8192)
	n, _ := resp.Body.Read(buf)
	return resp, buf[:n]
}

// signValidBearer mints a token addressed to the atlas issuer +
// audience so jwt.Validate (iss + aud + temporal) passes.
func signValidBearer(t *testing.T, signer *tokensign.Signer, subject string) string {
	t.Helper()
	tok, err := signer.Sign(context.Background(), jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    testIssuer,
			Subject:   subject,
			Audience:  []string{testIssuer},
			ExpiresAt: pinnedNow().Add(time.Hour).Unix(),
			IssuedAt:  pinnedNow().Add(-time.Minute).Unix(),
			NotBefore: pinnedNow().Add(-time.Minute).Unix(),
			ID:        uuid.NewString(),
		},
		CurrentTenantID: uuid.New(),
	})
	if err != nil {
		t.Fatalf("Sign bearer: %v", err)
	}
	return tok
}

// TestRevoke_RejectsNonFormContentType covers RFC 7009 §2.1: the
// revocation request is application/x-www-form-urlencoded; any other
// content type is rejected 400 + invalid_request before any auth work.
func TestRevoke_RejectsNonFormContentType(t *testing.T) {
	t.Parallel()
	srv, _ := newRevokeUnitServer(t)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+oauth.PathRevoke, strings.NewReader(`{"token":"x"}`))
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

// TestRevoke_MissingToken covers RFC 7009 §2.1: the `token` parameter
// is REQUIRED; its absence is 400 + invalid_request (distinct from the
// silent-200-for-unknown-token semantics — a missing param is a
// malformed request, not an unknown token).
func TestRevoke_MissingToken(t *testing.T) {
	t.Parallel()
	srv, _ := newRevokeUnitServer(t)

	resp, body := postRevoke(t, srv.URL, url.Values{}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s; want 400", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "invalid_request") {
		t.Errorf("body = %q, want invalid_request", body)
	}
}

// TestRevoke_NoCallerAuth covers RFC 7009 §2.2: a request with a
// `token` but no caller authentication (no Basic, no client form
// params, no Authorization Bearer) MUST be rejected 401 +
// invalid_client. RFC 7009 requires the caller to authenticate before
// any revocation effect; an unauthenticated caller gets 401, NOT the
// silent 200.
func TestRevoke_NoCallerAuth(t *testing.T) {
	t.Parallel()
	srv, _ := newRevokeUnitServer(t)

	form := url.Values{}
	form.Set("token", "atlas-opaque-target-token")
	resp, body := postRevoke(t, srv.URL, form, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s; want 401", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "invalid_client") {
		t.Errorf("body = %q, want invalid_client", body)
	}
}

// TestRevoke_SelfRevokeRejectsForeignSubject covers the
// self-revocation authentication branch (path b in authenticate): a
// caller presents a VALID bearer JWT but tries to revoke a DIFFERENT
// subject's token. The handler MUST reject with 401 + invalid_client —
// a user may only self-revoke their own tokens; cross-subject
// revocation requires client_credentials. The rejection happens before
// the revocation.Store.Revoke DB write, so it runs without Postgres.
func TestRevoke_SelfRevokeRejectsForeignSubject(t *testing.T) {
	t.Parallel()
	srv, signer := newRevokeUnitServer(t)

	caller := signValidBearer(t, signer, "user:alice")
	// The target token is a valid JWT whose subject is bob — alice may
	// not revoke bob's token without client_credentials.
	target := signValidBearer(t, signer, "user:bob")

	form := url.Values{}
	form.Set("token", target)
	resp, body := postRevoke(t, srv.URL, form, map[string]string{
		"Authorization": "Bearer " + caller,
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s; want 401", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "invalid_client") {
		t.Errorf("body = %q, want invalid_client", body)
	}
}

// TestRevoke_SelfRevokeRejectsUnsignedBearer covers the bearer-shape
// guard in authenticate path (b): a bearer that does not verify
// against the local keystore is rejected. We present a structurally
// JWT-shaped string ("eyJ..." prefix) that is NOT a valid signature,
// so signer.Verify fails and the caller is treated as unauthenticated
// (401 invalid_client). This guards against accepting an arbitrary
// attacker-supplied bearer.
func TestRevoke_SelfRevokeRejectsUnsignedBearer(t *testing.T) {
	t.Parallel()
	srv, signer := newRevokeUnitServer(t)

	target := signValidBearer(t, signer, "user:carol")

	// A bearer carrying the "eyJ" prefix the self-revoke path requires
	// (revoke.authenticate gates on strings.HasPrefix(bearer, "eyJ"))
	// but with non-decodable segments so signer.Verify fails. Kept
	// deliberately minimal — NOT a structurally-complete JWT — so it is
	// unmistakably a test placeholder, not a credential.
	bogusBearer := "eyJ.not.ajwt"

	form := url.Values{}
	form.Set("token", target)
	resp, body := postRevoke(t, srv.URL, form, map[string]string{
		"Authorization": "Bearer " + bogusBearer,
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s; want 401", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "invalid_client") {
		t.Errorf("body = %q, want invalid_client", body)
	}
}

// TestRevoke_EmptyFormClientSecretRejected covers the
// client_credentials form-param branch in authenticate path (a): a
// client_id present in the form WITHOUT a client_secret is rejected
// 401 — the handler does not fall through to self-revoke when a
// client_id form param is declared (an explicit "I'm a client" intent).
func TestRevoke_EmptyFormClientSecretRejected(t *testing.T) {
	t.Parallel()
	srv, _ := newRevokeUnitServer(t)

	form := url.Values{}
	form.Set("token", "atlas-opaque-target")
	form.Set("client_id", "machine-client-1")
	// no client_secret
	resp, body := postRevoke(t, srv.URL, form, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s; want 401", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "invalid_client") {
		t.Errorf("body = %q, want invalid_client", body)
	}
}

// TestRevoke_DiscoveryAdvertisesAuthMethods covers RFC 8414 §2: when
// the revocation endpoint is wired, the discovery document advertises
// revocation_endpoint_auth_methods_supported (slice-190 honest
// advertising).
func TestRevoke_DiscoveryAdvertisesAuthMethods(t *testing.T) {
	t.Parallel()
	srv, _ := newRevokeUnitServer(t)

	resp, err := http.Get(srv.URL + oauth.PathDiscovery)
	if err != nil {
		t.Fatalf("GET discovery: %v", err)
	}
	defer resp.Body.Close()
	var doc map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	methods, ok := doc["revocation_endpoint_auth_methods_supported"].([]any)
	if !ok {
		t.Fatalf("revocation_endpoint_auth_methods_supported missing/wrong type: %T",
			doc["revocation_endpoint_auth_methods_supported"])
	}
	if len(methods) == 0 {
		t.Fatal("revocation auth methods list is empty")
	}
}
