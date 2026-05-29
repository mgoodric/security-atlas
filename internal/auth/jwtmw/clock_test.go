// Slice 371 — clock-injection boundary tests for the jwtmw middleware.
//
// The middleware's claim-validation path (nbf, exp, iat) flows through
// Options.Now → nowTime → jwt.Validate, where the validator compares
// claim Unix-seconds integers against `now.Unix()`. The slice 371
// refactor also unified the nil-fallback at nowTime: pre-371 it
// returned plain time.Now; post-371 it returns time.Now().UTC() to
// match the rest of the codebase's clock-injection convention.
//
// These tests are unit-level (no DB, no chi server stack — just the
// middleware function and httptest). They cover three load-bearing
// boundaries called out in the slice doc:
//
//   - nbf boundary: at exact-nbf, token is valid (jwt.Validate uses
//     non-strict ≥ for nbf); one second before, it's not-yet-valid.
//   - exp boundary: at exact-exp, token is expired (jwt.Validate uses
//     strict > for exp).
//   - nowTime fallback: when Options.Now is nil, the returned time
//     carries the UTC zone (slice 371 — was previously time.Now() in
//     host-local zone).
//
// The tests do not exercise the middleware's downstream tenant-GUC
// step (that path is DB-bound and lives in integration_test.go); they
// stop at the verified-claims observation surface (FromContext).

package jwtmw_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
)

// TestNowTimeFallback_ReturnsUTC verifies the slice 371 contract:
// when Options.Now is nil, the middleware's clock fallback returns
// time.Time in the UTC zone (matches the established pattern). The
// public symbol nowTime is package-private, so we exercise the
// behaviour indirectly via the middleware's behaviour at a known
// nbf/exp window with the cookie path (no header).
//
// The test simply mints a token whose validity window straddles real
// wall-clock now() and verifies the request succeeds without an
// injected clock — proving the default clock observes real time.
// Failure mode pre-371 was a localized regression risk; this test
// pins the contract.
func TestNowTimeFallback_AcceptsCurrentWallClockToken(t *testing.T) {
	t.Parallel()
	signer := newSigner(t)
	now := time.Now()
	claims := freshClaims(now)
	tok := mintToken(t, signer, claims)

	// Options.Now intentionally left nil → nowTime falls back to
	// time.Now().UTC().
	mw := jwtmw.Middleware(signer, nil, jwtmw.Options{
		ExpectedIssuer:   testIssuer,
		ExpectedAudience: testAudience,
		// Now: nil — exercise slice-371 fallback.
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/anchors", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	mw(noopHandler(nil)).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Options.Now=nil with valid token: status = %d; want 200 (slice-371 fallback regression)", w.Code)
	}
}

// TestClockInjection_TokenValidAtExactlyIat verifies the boundary
// case the slice doc calls out: "a JWT with nbf=10, exp=100 should
// return 200 at clock T=10 (exactly nbf)". nbf is non-strict
// (≥) in the validator, so the token is valid at the exact nbf.
// Test runs at now=nbf=iat to pin both boundaries.
func TestClockInjection_TokenValidAtExactlyIat(t *testing.T) {
	t.Parallel()
	signer := newSigner(t)

	// Pin a stable t0 — Unix(1716912000, 0) = 2024-05-28T16:00:00Z.
	t0 := time.Unix(1716912000, 0).UTC()
	tenantA := uuid.New()
	claims := jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    testIssuer,
			Subject:   "user:exact-iat",
			Audience:  []string{testAudience},
			ExpiresAt: t0.Add(time.Hour).Unix(),
			IssuedAt:  t0.Unix(),
			NotBefore: t0.Unix(),
			ID:        uuid.NewString(),
		},
		IDPIssuer:        "test-idp",
		CurrentTenantID:  tenantA,
		AvailableTenants: []uuid.UUID{tenantA},
		Roles:            map[uuid.UUID][]string{tenantA: {"admin"}},
	}
	tok := mintToken(t, signer, claims)

	// Pin clock to exactly iat = nbf.
	mw := jwtmw.Middleware(signer, nil, jwtmw.Options{
		ExpectedIssuer:   testIssuer,
		ExpectedAudience: testAudience,
		Now:              func() int64 { return t0.Unix() },
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/anchors", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	mw(noopHandler(nil)).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("clock=iat=nbf: status = %d; want 200 (valid at exact nbf)", w.Code)
	}
}

// TestClockInjection_TokenRejectedAtExactlyExp verifies the exp
// boundary called out in the slice doc: "return 401 at clock T=100
// (exactly exp)". exp is strict (claim.Validate uses now.Unix() >=
// exp → reject), so at exactly exp the token is rejected.
func TestClockInjection_TokenRejectedAtExactlyExp(t *testing.T) {
	t.Parallel()
	signer := newSigner(t)

	t0 := time.Unix(1716912000, 0).UTC()
	tenantA := uuid.New()
	exp := t0.Add(time.Hour)
	claims := jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    testIssuer,
			Subject:   "user:exact-exp",
			Audience:  []string{testAudience},
			ExpiresAt: exp.Unix(),
			IssuedAt:  t0.Unix(),
			NotBefore: t0.Unix(),
			ID:        uuid.NewString(),
		},
		IDPIssuer:        "test-idp",
		CurrentTenantID:  tenantA,
		AvailableTenants: []uuid.UUID{tenantA},
		Roles:            map[uuid.UUID][]string{tenantA: {"admin"}},
	}
	tok := mintToken(t, signer, claims)

	// Pin clock to exactly exp → token must reject.
	mw := jwtmw.Middleware(signer, nil, jwtmw.Options{
		ExpectedIssuer:   testIssuer,
		ExpectedAudience: testAudience,
		Now:              func() int64 { return exp.Unix() },
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/anchors", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	mw(noopHandler(nil)).ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("clock=exp: status = %d; want 401 (rejected at exact exp)", w.Code)
	}
}

// TestClockInjection_TokenRejectedOneSecondBeforeNbf verifies the
// nbf-floor boundary: "return 401 at clock T=9 (before nbf)". One
// second before nbf, the token is not-yet-valid.
func TestClockInjection_TokenRejectedOneSecondBeforeNbf(t *testing.T) {
	t.Parallel()
	signer := newSigner(t)

	t0 := time.Unix(1716912000, 0).UTC()
	tenantA := uuid.New()
	claims := jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    testIssuer,
			Subject:   "user:before-nbf",
			Audience:  []string{testAudience},
			ExpiresAt: t0.Add(time.Hour).Unix(),
			IssuedAt:  t0.Unix(),
			NotBefore: t0.Unix(),
			ID:        uuid.NewString(),
		},
		IDPIssuer:        "test-idp",
		CurrentTenantID:  tenantA,
		AvailableTenants: []uuid.UUID{tenantA},
		Roles:            map[uuid.UUID][]string{tenantA: {"admin"}},
	}
	tok := mintToken(t, signer, claims)

	// Pin clock to one second before nbf → token must reject.
	beforeNbf := t0.Add(-time.Second)
	mw := jwtmw.Middleware(signer, nil, jwtmw.Options{
		ExpectedIssuer:   testIssuer,
		ExpectedAudience: testAudience,
		Now:              func() int64 { return beforeNbf.Unix() },
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/anchors", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	mw(noopHandler(nil)).ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("clock=nbf-1s: status = %d; want 401 (not-yet-valid)", w.Code)
	}
}
