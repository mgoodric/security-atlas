package jwtmw_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
	"github.com/mgoodric/security-atlas/internal/auth/keystore/fsstore"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
)

const (
	testIssuer   = "https://atlas.example.test"
	testAudience = "https://atlas.example.test"
)

// newSigner builds a fresh ES256 signer backed by a temp-dir fsstore.
// Returns the signer + a function that mints a JWT with the supplied
// claim shape (the test's clock is pinned via the signer-bound clock,
// so the tests deterministically control exp/nbf).
func newSigner(t *testing.T) *tokensign.Signer {
	t.Helper()
	store, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	return tokensign.New(store)
}

func mintToken(t *testing.T, signer *tokensign.Signer, claims jwt.AtlasClaims) string {
	t.Helper()
	tok, err := signer.Sign(context.Background(), claims)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return tok
}

// freshClaims builds a minimal-but-valid claim set ready for the
// validator. Tests override fields they care about.
func freshClaims(now time.Time) jwt.AtlasClaims {
	tenantA := uuid.New()
	return jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    testIssuer,
			Subject:   "user:alice",
			Audience:  []string{testAudience},
			ExpiresAt: now.Add(time.Hour).Unix(),
			IssuedAt:  now.Unix(),
			NotBefore: now.Unix(),
			ID:        uuid.NewString(),
		},
		IDPIssuer:        "test-idp",
		CurrentTenantID:  tenantA,
		AvailableTenants: []uuid.UUID{tenantA},
		Roles:            map[uuid.UUID][]string{tenantA: {"admin"}},
		SuperAdmin:       false,
	}
}

// nowAt returns a clock pinned to `t`. Matches the jwtmw.Options.Now
// shape (int64 Unix-seconds).
func nowAt(t time.Time) func() int64 {
	return func() int64 { return t.Unix() }
}

// noopHandler is the chi handler the middleware wraps in tests. It
// records that it ran via a closure-captured flag the test inspects.
func noopHandler(invoked *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if invoked != nil {
			*invoked = true
		}
		w.WriteHeader(http.StatusOK)
	})
}

// claimsCaptureHandler captures the claims set by Middleware via
// FromContext so the test can assert AC-5.
func claimsCaptureHandler(captured **jwt.AtlasClaims) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*captured = jwtmw.FromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
}

// TestMiddleware_NoAuth_PassesThrough: with neither header nor
// cookie, the middleware delegates to the next handler. Documents
// the coexistence contract: the legacy bearer middleware handles
// missing-JWT requests.
func TestMiddleware_NoAuth_PassesThrough(t *testing.T) {
	t.Parallel()
	signer := newSigner(t)

	var invoked bool
	mw := jwtmw.Middleware(signer, nil, jwtmw.Options{
		ExpectedIssuer:   testIssuer,
		ExpectedAudience: testAudience,
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/anchors", nil)
	w := httptest.NewRecorder()
	mw(noopHandler(&invoked)).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (pass-through)", w.Code)
	}
	if !invoked {
		t.Fatal("next handler not invoked on no-auth request")
	}
}

// TestMiddleware_ValidJWT_PassesThroughWithClaims: the happy path.
func TestMiddleware_ValidJWT_PassesThroughWithClaims(t *testing.T) {
	t.Parallel()
	signer := newSigner(t)
	now := time.Now()
	claims := freshClaims(now)
	tok := mintToken(t, signer, claims)

	var captured *jwt.AtlasClaims
	mw := jwtmw.Middleware(signer, nil, jwtmw.Options{
		ExpectedIssuer:   testIssuer,
		ExpectedAudience: testAudience,
		Now:              nowAt(now),
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/anchors", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	mw(claimsCaptureHandler(&captured)).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if captured == nil {
		t.Fatal("FromContext returned nil after successful auth")
	}
	if captured.Subject != "user:alice" {
		t.Errorf("Subject = %q, want %q", captured.Subject, "user:alice")
	}
}

// TestMiddleware_InvalidSignature_Returns401: any tampering with the
// token MUST produce a 401, NOT a fall-through (P0-190-1 risk).
func TestMiddleware_InvalidSignature_Returns401(t *testing.T) {
	t.Parallel()
	signer := newSigner(t)
	now := time.Now()
	tok := mintToken(t, signer, freshClaims(now))

	// Tamper with the payload — flip a byte in the middle of the
	// signature section. The result is still JWT-shaped (3 dot-
	// separated parts beginning with eyJ) so the middleware will
	// try to verify it and fail. We pick a middle byte (not the
	// last) because the last base64url char of an ES256 signature
	// encodes only 2 useful bits — flipping it may produce an
	// equivalent signature after canonicalization, defeating the
	// "tamper" intent.
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("minted token shape: %d parts, want 3", len(parts))
	}
	sig := parts[2]
	if len(sig) < 4 {
		t.Fatalf("signature too short: %d chars", len(sig))
	}
	mid := len(sig) / 2
	flipped := sig[:mid] + flipBase64URLChar(sig[mid]) + sig[mid+1:]
	tamperedTok := parts[0] + "." + parts[1] + "." + flipped

	var invoked bool
	mw := jwtmw.Middleware(signer, nil, jwtmw.Options{
		ExpectedIssuer:   testIssuer,
		ExpectedAudience: testAudience,
		Now:              nowAt(now),
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/anchors", nil)
	req.Header.Set("Authorization", "Bearer "+tamperedTok)
	w := httptest.NewRecorder()
	mw(noopHandler(&invoked)).ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if invoked {
		t.Fatal("next handler invoked on invalid signature — auth bypass risk")
	}
	if got := w.Header().Get("WWW-Authenticate"); !strings.Contains(got, `realm="atlas"`) {
		t.Errorf("WWW-Authenticate = %q, missing realm", got)
	}
}

// TestMiddleware_ExpiredToken_Returns401: claim validation rejects
// expired tokens.
func TestMiddleware_ExpiredToken_Returns401(t *testing.T) {
	t.Parallel()
	signer := newSigner(t)
	mintTime := time.Now().Add(-2 * time.Hour)
	claims := freshClaims(mintTime)
	tok := mintToken(t, signer, claims)

	mw := jwtmw.Middleware(signer, nil, jwtmw.Options{
		ExpectedIssuer:   testIssuer,
		ExpectedAudience: testAudience,
		Now:              nowAt(time.Now()),
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/anchors", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	mw(noopHandler(nil)).ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

// TestMiddleware_WrongAudience_Returns401: audience mismatch is a 401.
func TestMiddleware_WrongAudience_Returns401(t *testing.T) {
	t.Parallel()
	signer := newSigner(t)
	now := time.Now()
	claims := freshClaims(now)
	tok := mintToken(t, signer, claims)

	mw := jwtmw.Middleware(signer, nil, jwtmw.Options{
		ExpectedIssuer:   testIssuer,
		ExpectedAudience: "https://other.example.test",
		Now:              nowAt(now),
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/anchors", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	mw(noopHandler(nil)).ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

// TestMiddleware_OpaqueLegacyBearer_PassesThrough: a `Bearer atlas_*`
// header MUST be ignored by the JWT middleware so the legacy bearer
// middleware can pick it up. This is the coexistence contract.
func TestMiddleware_OpaqueLegacyBearer_PassesThrough(t *testing.T) {
	t.Parallel()
	signer := newSigner(t)

	var invoked bool
	mw := jwtmw.Middleware(signer, nil, jwtmw.Options{
		ExpectedIssuer:   testIssuer,
		ExpectedAudience: testAudience,
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/anchors", nil)
	req.Header.Set("Authorization", "Bearer atlas_abcdef0123456789")
	w := httptest.NewRecorder()
	mw(noopHandler(&invoked)).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (legacy bearer pass-through)", w.Code)
	}
	if !invoked {
		t.Fatal("next handler not invoked on legacy-bearer pass-through")
	}
}

// TestMiddleware_JWTCookie_ValidJWTAccepted: the cookie-bearing
// browser flow (slice 189) lands tokens in a cookie; the middleware
// reads them from there when the Authorization header is absent.
func TestMiddleware_JWTCookie_ValidJWTAccepted(t *testing.T) {
	t.Parallel()
	signer := newSigner(t)
	now := time.Now()
	tok := mintToken(t, signer, freshClaims(now))

	var invoked bool
	mw := jwtmw.Middleware(signer, nil, jwtmw.Options{
		ExpectedIssuer:   testIssuer,
		ExpectedAudience: testAudience,
		CookieName:       jwtmw.DefaultCookieName,
		Now:              nowAt(now),
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/anchors", nil)
	req.AddCookie(&http.Cookie{Name: jwtmw.DefaultCookieName, Value: tok})
	w := httptest.NewRecorder()
	mw(noopHandler(&invoked)).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !invoked {
		t.Fatal("next handler not invoked on cookie auth")
	}
}

// TestMiddleware_HeaderPrecedence_OverCookie: when both are present
// and the header is valid, the header is used. Decision D1.
func TestMiddleware_HeaderPrecedence_OverCookie(t *testing.T) {
	t.Parallel()
	signer := newSigner(t)
	now := time.Now()

	headerClaims := freshClaims(now)
	headerClaims.Subject = "user:alice-header"
	headerTok := mintToken(t, signer, headerClaims)

	cookieClaims := freshClaims(now)
	cookieClaims.Subject = "user:alice-cookie"
	cookieTok := mintToken(t, signer, cookieClaims)

	var captured *jwt.AtlasClaims
	mw := jwtmw.Middleware(signer, nil, jwtmw.Options{
		ExpectedIssuer:   testIssuer,
		ExpectedAudience: testAudience,
		CookieName:       jwtmw.DefaultCookieName,
		Now:              nowAt(now),
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/anchors", nil)
	req.Header.Set("Authorization", "Bearer "+headerTok)
	req.AddCookie(&http.Cookie{Name: jwtmw.DefaultCookieName, Value: cookieTok})
	w := httptest.NewRecorder()
	mw(claimsCaptureHandler(&captured)).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if captured == nil {
		t.Fatal("FromContext returned nil")
	}
	if captured.Subject != "user:alice-header" {
		t.Errorf("Subject = %q, want header-sourced %q", captured.Subject, "user:alice-header")
	}
}

// TestMiddleware_HeaderShapeFilter_OnlyJWT: a non-JWT shaped header
// must be ignored so the legacy bearer path can handle it.
func TestMiddleware_HeaderShapeFilter_OnlyJWT(t *testing.T) {
	t.Parallel()
	signer := newSigner(t)

	var invoked bool
	mw := jwtmw.Middleware(signer, nil, jwtmw.Options{
		ExpectedIssuer:   testIssuer,
		ExpectedAudience: testAudience,
	})

	// `Bearer eyJ` shape but with no real signature — still
	// JWT-shaped, so we go down the JWT path and 401.
	req := httptest.NewRequest(http.MethodGet, "/v1/anchors", nil)
	req.Header.Set("Authorization", "Bearer eyJabc.eyJdef.sig")
	w := httptest.NewRecorder()
	mw(noopHandler(&invoked)).ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("malformed-JWT-shaped: status = %d, want 401 (no fall-through)", w.Code)
	}
	if invoked {
		t.Fatal("next handler invoked on malformed JWT — auth bypass risk")
	}
}

// flipBase64URLChar swaps a single base64url char so the resulting
// signature does not verify. Returns a different valid char.
func flipBase64URLChar(c byte) string {
	if c == 'A' {
		return "B"
	}
	return "A"
}

// TestFromContext_Nil: FromContext returns nil for a context that did
// not pass through the middleware.
func TestFromContext_Nil(t *testing.T) {
	t.Parallel()
	if got := jwtmw.FromContext(context.Background()); got != nil {
		t.Fatalf("FromContext on bare context = %v, want nil", got)
	}
}
