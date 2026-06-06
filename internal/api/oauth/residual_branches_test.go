// residual_branches_test.go — slice 456 unit coverage for the RESIDUAL
// error / signer-failure / rate-limiter arms the slice-422
// error_branches_test.go left untested.
//
// THREAT FRAMING (slice 456 threat model): two surfaces.
//
//   - Signer-failure 500 arms (AVAILABILITY): when the keystore cannot
//     mint a JWT, the handler MUST surface server_error (500) with ONLY
//     the RFC error code + a generic description — never an internal
//     detail (no curve name, no "keystore", no stack). Composes with the
//     slice-367 errleak discipline. The unit-reachable site is the
//     token-exchange path (its Verify succeeds on a self-minted subject
//     token while the Sign step is driven to fail); the
//     client_credentials / authorization_code / device_code sign sites
//     need a DB-backed redemption first and live in the integration tier.
//
//   - Best-effort audit-write arms (REPUDIATION): a silently-dropped
//     audit row weakens forensic reconstruction of a tenant switch but
//     MUST NOT block nor corrupt the token response (D3). The nil-pool
//     early return is unit-reachable here; the DB-state-dependent
//     failure arms (BeginTx / Exec failure) live in the integration tier.
//
// The rate-limiter arms are not security DENY branches — they are cheap
// statement coverage of the token-bucket overflow cap + the
// WindowSeconds / max edge arms via the slice-456 exported seams.
//
// No JWT/vendor-shaped fixture literals: the verify-ok/sign-fail subject
// token is minted in-process by the real fsstore-backed signer; the
// sign-failure is driven by a keystore returning a non-ES256 (P-384)
// signing key, NOT a pasted literal.

package oauth_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/oauth"
	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/keystore"
	"github.com/mgoodric/security-atlas/internal/auth/keystore/fsstore"
	"github.com/mgoodric/security-atlas/internal/auth/oauthcode"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
)

// verifyOKSignFailStore is a keystore that VERIFIES against a real
// fsstore's keys but whose ACTIVE SIGNING key is a non-ES256 (P-384)
// key. tokensign.Sign rejects the P-384 curve (isES256Key == false) so
// every Sign returns an error, while Verify still resolves the real
// verification keys by kid — so a subject_token minted by the inner
// fsstore signer verifies, then the exchange's own Sign step fails.
//
// This is the deterministic, no-DB way to reach the token-exchange
// signer-failure 500 arm: a path where Verify succeeds but Sign fails.
type verifyOKSignFailStore struct {
	inner       keystore.KeyStore
	badSigningK keystore.SigningKey
}

func newVerifyOKSignFailStore(t *testing.T, inner keystore.KeyStore) *verifyOKSignFailStore {
	t.Helper()
	// P-384 keypair — valid ECDSA, but NOT the ES256-required P-256.
	priv, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatalf("generate P-384 key: %v", err)
	}
	return &verifyOKSignFailStore{
		inner:       inner,
		badSigningK: keystore.SigningKey{KeyID: "sign-fail-p384", Key: priv},
	}
}

func (s *verifyOKSignFailStore) Get(ctx context.Context) (keystore.SigningKey, []keystore.VerificationKey, error) {
	_, vks, err := s.inner.Get(ctx)
	if err != nil {
		return keystore.SigningKey{}, nil, err
	}
	// Real verification keys (so Verify of a real subject_token works) +
	// a bad signing key (so Sign fails the ES256 curve check).
	return s.badSigningK, vks, nil
}

func (s *verifyOKSignFailStore) Rotate(ctx context.Context) error { return s.inner.Rotate(ctx) }

// newSignFailTokenServer builds a token endpoint whose Verify works
// (against the real inner fsstore keys) but whose Sign fails. Returns
// the server plus the REAL signer the test uses to mint a valid
// subject_token (which the sign-fail server can still verify).
func newSignFailTokenServer(t *testing.T) (*httptest.Server, *tokensign.Signer) {
	t.Helper()
	ks, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	realSigner := tokensign.New(ks)
	signFailSigner := tokensign.New(newVerifyOKSignFailStore(t, ks))

	ep := oauth.NewTokenEndpoint(signFailSigner, nil, oauth.TokenEndpointConfig{
		Issuer:        testIssuer,
		RatePerMinute: 600,
		Now:           pinnedNow,
	})
	h := oauth.New(ks, oauth.Config{Issuer: testIssuer})
	h.AttachTokenEndpoint(ep)
	srv := httptest.NewServer(routerFor(h))
	t.Cleanup(srv.Close)
	return srv, realSigner
}

// TestTokenExchange_SignerFailureReturnsServerError covers AC-2: the
// token-exchange Sign(...) → server_error (500) arm (token.go:366). A
// subject_token that passes signature + claim + allowlist validation is
// presented; the handler reaches the mint step, which fails because the
// active signing key is non-ES256. The handler MUST surface 500 +
// server_error and MUST NOT leak any internal detail (slice 367).
func TestTokenExchange_SignerFailureReturnsServerError(t *testing.T) {
	t.Parallel()
	srv, realSigner := newSignFailTokenServer(t)

	target := uuid.New()
	// Valid subject token (minted by the REAL signer; verifies against
	// the inner fsstore keys the sign-fail store still exposes), with the
	// target tenant in available_tenants so the allowlist check passes
	// and execution reaches the Sign step.
	subject := signExchangeSubject(t, realSigner, exchangeSubjectParams{
		subject:   "user:vciso",
		issuer:    testIssuer,
		audience:  testIssuer,
		expiresAt: pinnedNow().Add(time.Hour),
		tenants:   []uuid.UUID{target},
	})

	resp, body := postExchange(t, srv.URL, subject, target)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s; want 500", resp.StatusCode, body)
	}
	if got := decodeOAuthErr(t, body); got != "server_error" {
		t.Errorf("error = %q, want server_error", got)
	}
	// Information-disclosure assertion: the 500 body must carry only the
	// RFC code + generic description, never an internal detail.
	lower := strings.ToLower(string(body))
	for _, marker := range []string{"keystore", "es256", "curve", "p-384", "p384", "tokensign", "sql", "pgx", "panic", "goroutine"} {
		if strings.Contains(lower, marker) {
			t.Errorf("server_error body leaks internal detail %q: %s", marker, body)
		}
	}
}

// ===== rate-limiter residual arms (AC-3) =====

// TestLimiter_OverflowCap covers token.go:574 — the refill cap. After a
// long idle, the accumulated refill is clamped to the bucket size (rate)
// rather than allowed to grow unbounded; a burst of `rate` calls all
// succeed but the (rate+1)th in the same instant is refused. Driving an
// advancing clock then a long idle reaches the `tokens > rate` clamp.
func TestLimiter_OverflowCap(t *testing.T) {
	t.Parallel()
	clk := &fakeClock{now: pinnedNow()}
	lim := oauth.ExportNewTokenBucketLimiter(5, clk.Now)

	// First call seeds the bucket (rate-1 tokens remaining).
	if !oauth.ExportLimiterAllow(lim, "client-a") {
		t.Fatal("first Allow should succeed")
	}
	// Idle a long time so the refill computation overflows the cap; the
	// clamp at token.go:574 keeps tokens == rate. After the idle the
	// bucket is full again.
	clk.advance(10 * time.Minute)
	allowed := 0
	for i := 0; i < 5; i++ {
		if oauth.ExportLimiterAllow(lim, "client-a") {
			allowed++
		}
	}
	if allowed != 5 {
		t.Errorf("after long idle, allowed %d of 5 burst calls; want 5 (bucket refilled + capped)", allowed)
	}
	// The bucket is now empty (no clock advance) — the next call is refused.
	if oauth.ExportLimiterAllow(lim, "client-a") {
		t.Error("6th call in the same instant should be rate-limited")
	}
}

// TestLimiter_WindowSecondsEdges covers token.go:590/597 — the
// WindowSeconds rate<=0 guard and the max(1, ...) floor. A non-positive
// rate yields the 60s default; a very high rate floors the window at 1s.
func TestLimiter_WindowSecondsEdges(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		rate int
		want int
	}{
		{"zero-rate-defaults-60", 0, 60},
		{"negative-rate-defaults-60", -5, 60},
		{"high-rate-floors-at-1", 600, 1},
		{"sixty-per-min-is-1", 60, 1},
		{"thirty-per-min-is-2", 30, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lim := oauth.ExportNewTokenBucketLimiter(c.rate, pinnedNow)
			if got := oauth.ExportLimiterWindowSeconds(lim); got != c.want {
				t.Errorf("WindowSeconds(rate=%d) = %d, want %d", c.rate, got, c.want)
			}
		})
	}
}

// TestNewTokenEndpoint_RateDefaultsWhenNonPositive covers token.go:150 —
// the constructor's rate<=0 fallback to DefaultTokenRatePerMin. A zero
// RatePerMinute config must not produce a zero-capacity limiter (which
// would refuse every request); it falls back to the default. We assert
// the resulting limiter advertises the default-rate window (1s for
// 60/min), proving the fallback took effect.
func TestNewTokenEndpoint_RateDefaultsWhenNonPositive(t *testing.T) {
	t.Parallel()
	srv, _, _ := newTokenTestServerRate(t, 0)
	// A client_credentials request with no client store returns 503, but
	// the rate limiter runs FIRST — a zero-capacity limiter would have
	// returned 429. Reaching the 503 proves the limiter allowed the call,
	// i.e. the default rate took effect.
	form := url.Values{}
	form.Set("grant_type", oauth.GrantTypeClientCredentials)
	form.Set("client_id", "machine-client")
	form.Set("client_secret", "placeholder-secret")
	resp, body := postForm(t, srv.URL, form)
	if resp.StatusCode == http.StatusTooManyRequests {
		t.Fatalf("got 429 — zero RatePerMinute produced a zero-capacity limiter (fallback failed); body=%s", body)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s; want 503 (nil client store, limiter passed)", resp.StatusCode, body)
	}
}

// newTokenTestServerRate is newTokenTestServer with an explicit rate so
// the constructor fallback can be exercised.
func newTokenTestServerRate(t *testing.T, rate int) (*httptest.Server, *tokensign.Signer, *oauth.TokenEndpoint) {
	t.Helper()
	ks, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	signer := tokensign.New(ks)
	ep := oauth.NewTokenEndpoint(signer, nil, oauth.TokenEndpointConfig{
		Issuer:        testIssuer,
		RatePerMinute: rate,
		Now:           pinnedNow,
	})
	h := oauth.New(ks, oauth.Config{Issuer: testIssuer})
	h.AttachTokenEndpoint(ep)
	srv := httptest.NewServer(routerFor(h))
	t.Cleanup(srv.Close)
	return srv, signer, ep
}

// ===== best-effort audit-write nil-pool arms (AC-1, unit half) =====

// TestWriteAudit_NilPoolIsNoop covers token.go:392 — the nil-pool early
// return in writeAudit. With no audit pool wired (the unit default), the
// best-effort write is a silent no-op; it MUST NOT panic (D3). The
// DB-state-dependent failure arms (BeginTx / Exec failure) are covered
// in residual_audit_integration_test.go.
func TestWriteAudit_NilPoolIsNoop(t *testing.T) {
	t.Parallel()
	ks, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	signer := tokensign.New(ks)
	ep := oauth.ExportTokenEndpointForAudit(signer, nil, pinnedNow)

	req := httptest.NewRequest(http.MethodPost, "/oauth/token", nil)
	req.RemoteAddr = "192.0.2.10:54321"
	// MUST NOT panic; the nil-pool guard returns immediately.
	oauth.ExportWriteAudit(ep, req, jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:  testIssuer,
			Subject: "user:vciso",
			ID:      uuid.NewString(),
		},
		CurrentTenantID: uuid.New(),
	}, uuid.New())
}

// TestWriteAuthCodeAudit_NilPoolIsNoop covers pkce.go:90 — the nil-pool
// early return in writeAuthCodeAudit.
func TestWriteAuthCodeAudit_NilPoolIsNoop(t *testing.T) {
	t.Parallel()
	ks, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	signer := tokensign.New(ks)
	ep := oauth.ExportTokenEndpointForAudit(signer, nil, pinnedNow)

	req := httptest.NewRequest(http.MethodPost, "/oauth/token", nil)
	oauth.ExportWriteAuthCodeAudit(ep, req, oauthcode.AuthCode{
		Code:            "atlas-test-authcode",
		UserID:          uuid.New(),
		CurrentTenantID: uuid.New(),
		IDPIssuer:       "https://idp.example.test",
	})
}

// ===== test clock =====

// fakeClock is a mutable, monotonic test clock for the rate-limiter
// overflow arm (the limiter holds a clock closure; advancing it between
// Allow calls drives the refill math without sleeping).
type fakeClock struct {
	now time.Time
}

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) advance(d time.Duration) { c.now = c.now.Add(d) }
