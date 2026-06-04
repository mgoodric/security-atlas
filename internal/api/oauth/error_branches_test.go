// error_branches_test.go — slice 422 unit coverage for the
// security-critical RFC ERROR branches of the OAuth 2.0 AS that are
// reachable WITHOUT Postgres.
//
// THREAT FRAMING (slice 422 threat model): an untested OAuth error
// branch is an auth-bypass / elevation-of-privilege class bug. Each
// test here drives a DENY/REJECT branch and asserts the SECURE
// outcome — the specific RFC error code in the response body, not
// merely the HTTP status (AC-7 / P0-422-3). The deny outcome is the
// non-regressable property: a regression that silently turned any of
// these rejections into a success would be a privilege escalation or
// a spoofing bypass.
//
// Scope discipline: these are the branches that run BEFORE the first
// DB call (request validation, signature/claim validation on a
// self-minted subject_token, constructor invariants). The branches
// that need a DB-backed code/client/revocation store (the invalid_grant
// code-consume paths, the inspector-auth-success introspect paths, the
// client_credentials Verify-success path) are exercised by the
// enrolled integration suites (token_integration_test.go,
// revoke_introspect_integration_test.go, device_code_integration_test.go,
// authorize_integration_test.go).
//
// No JWT/vendor-shaped fixture literals: every subject_token is minted
// in-process by the real fsstore-backed signer; no static credential
// or key material appears as a literal.

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

// decodeOAuthErr decodes an RFC 6749 §5.2 error body and returns the
// `error` code. Used so assertions pin the RFC error CODE, not just
// the HTTP status (AC-7).
func decodeOAuthErr(t *testing.T, body []byte) string {
	t.Helper()
	var out struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode error body %q: %v", body, err)
	}
	return out.Error
}

// ===== /oauth/token — token-exchange (RFC 8693) DENY branches =====

// TestTokenExchange_MissingAndMalformedParams covers the RFC 8693 §2.1
// request-validation gates that run BEFORE any signature work:
// missing subject_token, an unsupported subject_token_type (atlas
// accepts only the JWT type in v1), and a missing target tenant.
// Each MUST be rejected 400 + invalid_request. These gates protect the
// downstream signature/allowlist logic from being reached with a
// malformed request.
func TestTokenExchange_MissingAndMalformedParams(t *testing.T) {
	t.Parallel()
	srv, signer, _ := newTokenTestServer(t)

	// A validly-signed subject token so the cases that DO supply a
	// subject_token still exercise the param gates rather than the
	// signature path.
	validSubject := signExchangeSubject(t, signer, exchangeSubjectParams{
		subject:   "user:vciso",
		issuer:    testIssuer,
		audience:  testIssuer,
		expiresAt: pinnedNow().Add(time.Hour),
		tenants:   []uuid.UUID{uuid.New()},
	})

	cases := []struct {
		name      string
		mutate    func(url.Values)
		wantError string
	}{
		{
			name:      "missing-subject_token",
			mutate:    func(f url.Values) { f.Del("subject_token") },
			wantError: "invalid_request",
		},
		{
			name:      "unsupported-subject_token_type",
			mutate:    func(f url.Values) { f.Set("subject_token_type", "urn:ietf:params:oauth:token-type:saml2") },
			wantError: "invalid_request",
		},
		{
			name:      "missing-target_tenant",
			mutate:    func(f url.Values) { f.Del("atlas:target_tenant_id") },
			wantError: "invalid_request",
		},
		{
			name:      "non-uuid-target_tenant",
			mutate:    func(f url.Values) { f.Set("atlas:target_tenant_id", "not-a-uuid") },
			wantError: "invalid_request",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			form := url.Values{}
			form.Set("grant_type", oauth.GrantTypeTokenExchange)
			form.Set("subject_token", validSubject)
			form.Set("subject_token_type", oauth.SubjectTokenTypeJWT)
			form.Set("atlas:target_tenant_id", uuid.NewString())
			c.mutate(form)

			resp, body := postForm(t, srv.URL, form)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s; want 400", resp.StatusCode, body)
			}
			if got := decodeOAuthErr(t, body); got != c.wantError {
				t.Errorf("error = %q, want %q", got, c.wantError)
			}
		})
	}
}

// TestTokenExchange_RejectsExpiredSubjectToken covers the RFC 8693
// TAMPERING/EoP branch (slice 422 threat model, AC-2): a subject_token
// whose signature is VALID (minted by the local signer) but whose
// claims fail temporal validation (expired) MUST be refused with 401 +
// invalid_token. This is distinct from the bad-signature branch
// (already covered in token_test.go): here the signature verifies but
// jwt.Validate rejects the claims, so the deny happens at the
// claim-validation gate. A regression that skipped this gate would let
// an expired token mint a fresh tenant-scoped token — an EoP.
func TestTokenExchange_RejectsExpiredSubjectToken(t *testing.T) {
	t.Parallel()
	srv, signer, _ := newTokenTestServer(t)

	target := uuid.New()
	// expires_at one hour BEFORE the handler's pinned clock → expired.
	expired := signExchangeSubject(t, signer, exchangeSubjectParams{
		subject:   "user:vciso",
		issuer:    testIssuer,
		audience:  testIssuer,
		expiresAt: pinnedNow().Add(-time.Hour),
		tenants:   []uuid.UUID{target},
	})

	resp, body := postExchange(t, srv.URL, expired, target)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s; want 401", resp.StatusCode, body)
	}
	if got := decodeOAuthErr(t, body); got != "invalid_token" {
		t.Errorf("error = %q, want invalid_token", got)
	}
}

// TestTokenExchange_RejectsWrongIssuerSubjectToken covers the same
// claim-validation deny branch via an issuer mismatch: a subject_token
// addressed to a DIFFERENT issuer must not be honored, even when its
// signature verifies locally. Pins the RFC error code invalid_token.
func TestTokenExchange_RejectsWrongIssuerSubjectToken(t *testing.T) {
	t.Parallel()
	srv, signer, _ := newTokenTestServer(t)

	target := uuid.New()
	wrongIssuer := signExchangeSubject(t, signer, exchangeSubjectParams{
		subject:   "user:vciso",
		issuer:    "https://attacker.example.test",
		audience:  testIssuer,
		expiresAt: pinnedNow().Add(time.Hour),
		tenants:   []uuid.UUID{target},
	})

	resp, body := postExchange(t, srv.URL, wrongIssuer, target)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s; want 401", resp.StatusCode, body)
	}
	if got := decodeOAuthErr(t, body); got != "invalid_token" {
		t.Errorf("error = %q, want invalid_token", got)
	}
}

// TestTokenExchange_RejectsWrongAudienceSubjectToken covers the
// audience arm of the claim-validation deny branch: a subject_token
// whose audience does not include the atlas issuer is refused. This is
// the cross-audience confused-deputy guard — a token minted for some
// other relying party cannot be replayed into the AS to obtain a
// tenant-scoped atlas token.
func TestTokenExchange_RejectsWrongAudienceSubjectToken(t *testing.T) {
	t.Parallel()
	srv, signer, _ := newTokenTestServer(t)

	target := uuid.New()
	wrongAud := signExchangeSubject(t, signer, exchangeSubjectParams{
		subject:   "user:vciso",
		issuer:    testIssuer,
		audience:  "https://some-other-rp.example.test",
		expiresAt: pinnedNow().Add(time.Hour),
		tenants:   []uuid.UUID{target},
	})

	resp, body := postExchange(t, srv.URL, wrongAud, target)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s; want 401", resp.StatusCode, body)
	}
	if got := decodeOAuthErr(t, body); got != "invalid_token" {
		t.Errorf("error = %q, want invalid_token", got)
	}
}

// TestTokenExchange_DeniedTenantSwitchLeaksNoInternalDetail covers the
// slice 422 INFORMATION-DISCLOSURE mitigation composed with the
// headline EoP deny: when a non-super_admin requests a tenant outside
// its available_tenants, the refusal body carries ONLY the RFC error
// code + a generic description — never an internal detail (no SQL text,
// no stack, no tenant enumeration). Composes with the slice-367 errleak
// discipline.
func TestTokenExchange_DeniedTenantSwitchLeaksNoInternalDetail(t *testing.T) {
	t.Parallel()
	srv, signer, _ := newTokenTestServer(t)

	allowed := uuid.New()
	denied := uuid.New() // deliberately NOT in available_tenants
	subject := signExchangeSubject(t, signer, exchangeSubjectParams{
		subject:   "user:scoped",
		issuer:    testIssuer,
		audience:  testIssuer,
		expiresAt: pinnedNow().Add(time.Hour),
		tenants:   []uuid.UUID{allowed},
	})

	resp, body := postExchange(t, srv.URL, subject, denied)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s; want 403", resp.StatusCode, body)
	}
	if got := decodeOAuthErr(t, body); got != "invalid_target" {
		t.Errorf("error = %q, want invalid_target", got)
	}
	// Information-disclosure assertion: the response body must NOT echo
	// the denied tenant id (no enumeration oracle) nor any internal
	// detail markers.
	lower := strings.ToLower(string(body))
	if strings.Contains(lower, denied.String()) {
		t.Errorf("denied-tenant body leaks the target tenant id: %s", body)
	}
	for _, marker := range []string{"sql", "pgx", "panic", "goroutine", "postgres", "select "} {
		if strings.Contains(lower, marker) {
			t.Errorf("denied-tenant body leaks internal detail %q: %s", marker, body)
		}
	}
}

// ===== /oauth/token — grant dispatch + form-parsing DENY branches =====

// TestToken_RejectsUnparseableForm covers RFC 6749 §3.2: a body that
// declares the form content-type but is not parseable as a form is
// rejected 400 + invalid_request. A semicolon-delimited body forces
// ParseForm to fail. This guards the dispatch from reading grant_type
// off a half-parsed form.
func TestToken_RejectsUnparseableForm(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTokenTestServer(t)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+oauth.PathToken,
		strings.NewReader("%zz=bad;invalid%"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	body := make([]byte, 4096)
	n, _ := resp.Body.Read(body)
	if got := decodeOAuthErr(t, body[:n]); got != "invalid_request" {
		t.Errorf("error = %q, want invalid_request", got)
	}
}

// TestClientCredentials_MissingCredentials covers the RFC 6749 §4.4
// request-validation gate: client_credentials with a missing client_id
// or client_secret is rejected 400 + invalid_request BEFORE the rate
// limiter and the DB-backed Verify. This is the pre-DB half of the
// client-auth surface; the Verify-failure invalid_client (401) path is
// covered by the integration suite (it needs a real client row to
// distinguish wrong-secret from unknown-client without an oracle).
func TestClientCredentials_MissingCredentials(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTokenTestServer(t)

	cases := []struct {
		name   string
		mutate func(url.Values)
	}{
		{"missing-both", func(url.Values) {}},
		{"missing-secret", func(f url.Values) { f.Set("client_id", "machine-client") }},
		{"missing-id", func(f url.Values) { f.Set("client_secret", "placeholder-secret") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			form := url.Values{}
			form.Set("grant_type", oauth.GrantTypeClientCredentials)
			c.mutate(form)
			resp, body := postForm(t, srv.URL, form)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s; want 400", resp.StatusCode, body)
			}
			if got := decodeOAuthErr(t, body); got != "invalid_request" {
				t.Errorf("error = %q, want invalid_request", got)
			}
		})
	}
}

// ===== /oauth/introspect + /oauth/revoke — malformed-form DENY =====

// TestIntrospect_RejectsUnparseableForm covers RFC 7662 §2.1: a
// malformed form body on the introspection endpoint is rejected 400 +
// invalid_request before any inspector authentication.
func TestIntrospect_RejectsUnparseableForm(t *testing.T) {
	t.Parallel()
	srv := newIntrospectUnitServer(t)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+oauth.PathIntrospect,
		strings.NewReader("%zz=bad;invalid%"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	body := make([]byte, 4096)
	n, _ := resp.Body.Read(body)
	if got := decodeOAuthErr(t, body[:n]); got != "invalid_request" {
		t.Errorf("error = %q, want invalid_request", got)
	}
}

// TestRevoke_RejectsUnparseableForm covers RFC 7009 §2.1: a malformed
// form body on the revocation endpoint is rejected 400 +
// invalid_request before any caller authentication.
func TestRevoke_RejectsUnparseableForm(t *testing.T) {
	t.Parallel()
	srv, _ := newRevokeUnitServer(t)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+oauth.PathRevoke,
		strings.NewReader("%zz=bad;invalid%"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	body := make([]byte, 4096)
	n, _ := resp.Body.Read(body)
	if got := decodeOAuthErr(t, body[:n]); got != "invalid_request" {
		t.Errorf("error = %q, want invalid_request", got)
	}
}

// TestRevoke_BasicAuthWrongSecretDoesNotFallThrough covers the RFC
// 7009 §2.1 client-auth path (a) DENY branch: a request carrying a
// Basic Authorization header is an explicit "I am a client" intent.
// When the (nil-pool) client store cannot verify the credential, the
// handler MUST reject 401 + invalid_client and MUST NOT fall through to
// the self-revoke bearer path — falling through would be a spoofing
// bypass. The nil-pool client store returns a verification failure
// without a DB round-trip, so this branch is unit-reachable.
func TestRevoke_BasicAuthWrongSecretDoesNotFallThrough(t *testing.T) {
	t.Parallel()
	srv, _ := newRevokeUnitServer(t)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+oauth.PathRevoke,
		strings.NewReader(url.Values{"token": {"atlas-opaque-target"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// Present-but-empty secret keeps oauthclient.Verify on its no-DB
	// short-circuit (returns the opaque verification failure) so the
	// branch is reachable without Postgres, while still declaring the
	// "I am a client" Basic intent.
	req.SetBasicAuth("machine-client", "")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	body := make([]byte, 4096)
	n, _ := resp.Body.Read(body)
	if got := decodeOAuthErr(t, body[:n]); got != "invalid_client" {
		t.Errorf("error = %q, want invalid_client", got)
	}
}

// ===== /oauth/device_authorization — malformed-form DENY =====

// TestDeviceAuth_RejectsUnparseableForm covers RFC 8628 §3.1: a
// malformed form body on the device-authorization endpoint is rejected
// 400 + invalid_request before the client lookup.
func TestDeviceAuth_RejectsUnparseableForm(t *testing.T) {
	t.Parallel()
	srv := newDeviceAuthServer(t)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+oauth.PathDeviceAuthorization,
		strings.NewReader("%zz=bad;invalid%"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	body := make([]byte, 4096)
	n, _ := resp.Body.Read(body)
	if got := decodeOAuthErr(t, body[:n]); got != "invalid_request" {
		t.Errorf("error = %q, want invalid_request", got)
	}
}

// ===== constructor invariants (fail-loud-at-startup DENY branches) =====

// TestEndpointConstructors_PanicOnMissingDeps covers the defensive
// constructor panics across the AS endpoint family. A missing required
// dependency at startup is a programmer error the AS surfaces loudly
// (panic) rather than silently 500-ing on every request — the
// fail-loud posture prevents a half-wired AS from accepting traffic.
// Each case asserts the panic fires for exactly one missing dependency.
func TestEndpointConstructors_PanicOnMissingDeps(t *testing.T) {
	t.Parallel()

	ks, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	signer := tokensign.New(ks)
	revoked := revocation.New(nil)
	clients := oauthclient.New(nil)

	cases := []struct {
		name string
		fn   func()
	}{
		{
			name: "token-nil-signer",
			fn: func() {
				oauth.NewTokenEndpoint(nil, clients, oauth.TokenEndpointConfig{Issuer: testIssuer})
			},
		},
		{
			name: "token-empty-issuer",
			fn: func() {
				oauth.NewTokenEndpoint(signer, clients, oauth.TokenEndpointConfig{Issuer: ""})
			},
		},
		{
			name: "introspect-nil-signer",
			fn: func() {
				oauth.NewIntrospectionEndpoint(nil, revoked, clients,
					oauth.IntrospectionEndpointConfig{Issuer: testIssuer})
			},
		},
		{
			name: "introspect-nil-revoked",
			fn: func() {
				oauth.NewIntrospectionEndpoint(signer, nil, clients,
					oauth.IntrospectionEndpointConfig{Issuer: testIssuer})
			},
		},
		{
			name: "introspect-nil-clients",
			fn: func() {
				oauth.NewIntrospectionEndpoint(signer, revoked, nil,
					oauth.IntrospectionEndpointConfig{Issuer: testIssuer})
			},
		},
		{
			name: "introspect-empty-issuer",
			fn: func() {
				oauth.NewIntrospectionEndpoint(signer, revoked, clients,
					oauth.IntrospectionEndpointConfig{Issuer: ""})
			},
		},
		{
			name: "revoke-nil-signer",
			fn: func() {
				oauth.NewRevocationEndpoint(nil, revoked, clients,
					oauth.RevocationEndpointConfig{Issuer: testIssuer})
			},
		},
		{
			name: "revoke-nil-revoked",
			fn: func() {
				oauth.NewRevocationEndpoint(signer, nil, clients,
					oauth.RevocationEndpointConfig{Issuer: testIssuer})
			},
		},
		{
			name: "revoke-empty-issuer",
			fn: func() {
				oauth.NewRevocationEndpoint(signer, revoked, clients,
					oauth.RevocationEndpointConfig{Issuer: ""})
			},
		},
		{
			name: "deviceauth-nil-clients",
			fn: func() {
				oauth.NewDeviceAuthorizationEndpoint(nil, oauth.NewDeviceCodeStore(nil),
					oauth.DeviceAuthorizationConfig{Issuer: testIssuer})
			},
		},
		{
			name: "deviceauth-nil-codes",
			fn: func() {
				oauth.NewDeviceAuthorizationEndpoint(clients, nil,
					oauth.DeviceAuthorizationConfig{Issuer: testIssuer})
			},
		},
		{
			name: "deviceauth-empty-issuer",
			fn: func() {
				oauth.NewDeviceAuthorizationEndpoint(clients, oauth.NewDeviceCodeStore(nil),
					oauth.DeviceAuthorizationConfig{Issuer: ""})
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("expected panic for %s, got none", c.name)
				}
			}()
			c.fn()
		})
	}
}

// ===== shared subject-token minting helper =====

// exchangeSubjectParams parameterizes a self-signed RFC 8693
// subject_token for the token-exchange DENY tests. Every field is a
// neutral test value; the signature is produced by the real
// fsstore-backed signer so no static credential literal appears.
type exchangeSubjectParams struct {
	subject   string
	issuer    string
	audience  string
	expiresAt time.Time
	tenants   []uuid.UUID
}

// signExchangeSubject mints a subject_token with the supplied claims.
func signExchangeSubject(t *testing.T, signer *tokensign.Signer, p exchangeSubjectParams) string {
	t.Helper()
	tok, err := signer.Sign(context.Background(), jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    p.issuer,
			Subject:   p.subject,
			Audience:  []string{p.audience},
			ExpiresAt: p.expiresAt.Unix(),
			IssuedAt:  pinnedNow().Add(-time.Minute).Unix(),
			NotBefore: pinnedNow().Add(-time.Minute).Unix(),
			ID:        uuid.NewString(),
		},
		CurrentTenantID:  firstOrNil(p.tenants),
		AvailableTenants: p.tenants,
		SuperAdmin:       false,
	})
	if err != nil {
		t.Fatalf("Sign subject: %v", err)
	}
	return tok
}

// postExchange issues a token-exchange request with the supplied
// subject token + target tenant and returns the response.
func postExchange(t *testing.T, srvURL, subjectToken string, target uuid.UUID) (*http.Response, []byte) {
	t.Helper()
	form := url.Values{}
	form.Set("grant_type", oauth.GrantTypeTokenExchange)
	form.Set("subject_token", subjectToken)
	form.Set("subject_token_type", oauth.SubjectTokenTypeJWT)
	form.Set("atlas:target_tenant_id", target.String())
	return postForm(t, srvURL, form)
}

func firstOrNil(list []uuid.UUID) uuid.UUID {
	if len(list) == 0 {
		return uuid.Nil
	}
	return list[0]
}

// keep the httptest import anchored even as the harness evolves.
var _ = httptest.NewServer
