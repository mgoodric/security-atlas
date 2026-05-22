//go:build integration

// integration_test.go — slice 190 R2 eviction integration test
// (AC-25, load-bearing). The slice cannot ship without these tests
// green.
//
// AC-25 verifies the "eventual eviction + explicit revocation"
// shape:
//
//   1. Mint a JWT scoped to tenant A. The first request succeeds.
//   2. Remove the user from tenant A (simulate admin action). The
//      next request with the SAME JWT still succeeds — JWT claims
//      are trusted until expiry; this is the standard OAuth
//      eventual-eviction semantic.
//   3. Revoke the JWT via the revocation store. The next request
//      with the same JWT returns 401.
//
// AC-26 verifies the cross-tenant header override rejection:
//   1. Mint a JWT with current_tenant_id = tenant A.
//   2. Send a request with a malicious X-Atlas-Tenant: tenant_B
//      header. The middleware MUST ignore the header — the verified
//      JWT claim is the only source of tenant identity. The handler
//      sees the original tenant A.
//
// These tests do NOT spin up the full atlas Server (which requires
// the DB schema bootstrap, every migration, and most of the slice
// 034/035 wiring). Instead they exercise the jwtmw middleware
// composed with a stub downstream handler that records the tenant
// the request actually carried — the same shape as the legacy
// bearer middleware integration tests.

package jwtmw_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
	"github.com/mgoodric/security-atlas/internal/auth/keystore/fsstore"
	"github.com/mgoodric/security-atlas/internal/auth/revocation"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// openIntegrationPool opens the atlas_app pool used for the
// revocation table. Skips when DATABASE_URL_APP is unset.
func openIntegrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL_APP")
	if dsn == "" {
		t.Skip("DATABASE_URL_APP not set; skipping R2 eviction test")
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

// tenantRecorder is the downstream handler the middleware wraps in
// these tests. It captures the tenant id pulled from the request
// context — the exact value RLS would use for `app.current_tenant`.
// The test then asserts the captured value matches the JWT's
// CurrentTenantID claim, NOT any header value.
func tenantRecorder(captured *string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tID, err := tenancy.TenantFromContext(r.Context())
		if err != nil {
			*captured = ""
		} else {
			*captured = tID
		}
		w.WriteHeader(http.StatusOK)
	})
}

// TestR2Eviction_EventualThenRevoke is the AC-25 load-bearing test.
func TestR2Eviction_EventualThenRevoke(t *testing.T) {
	pool := openIntegrationPool(t)
	ks, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	signer := tokensign.New(ks)
	revoked := revocation.New(pool)

	tenantA := uuid.New()
	tenantB := uuid.New()

	now := time.Now()
	claims := jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    testIssuer,
			Subject:   "user:alice-r2",
			Audience:  []string{testAudience},
			ExpiresAt: now.Add(time.Hour).Unix(),
			IssuedAt:  now.Unix(),
			NotBefore: now.Unix(),
			ID:        uuid.NewString(),
		},
		IDPIssuer:        "test-idp",
		CurrentTenantID:  tenantA,
		AvailableTenants: []uuid.UUID{tenantA, tenantB},
		Roles:            map[uuid.UUID][]string{tenantA: {"admin"}, tenantB: {"viewer"}},
		SuperAdmin:       false,
	}
	tok, err := signer.Sign(context.Background(), claims)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	var captured string
	mw := jwtmw.Middleware(signer, revoked, jwtmw.Options{
		ExpectedIssuer:   testIssuer,
		ExpectedAudience: testAudience,
		Now:              func() int64 { return now.Unix() },
	})
	h := mw(tenantRecorder(&captured))

	doRequest := func(name string) (int, string) {
		req := httptest.NewRequest(http.MethodGet, "/v1/anchors", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		w := httptest.NewRecorder()
		captured = ""
		h.ServeHTTP(w, req)
		t.Logf("%s: status=%d tenant=%s", name, w.Code, captured)
		return w.Code, captured
	}

	// Step 1: first request succeeds, tenant captured matches JWT
	// claim.
	code, gotTenant := doRequest("step 1 (initial)")
	if code != http.StatusOK {
		t.Fatalf("step 1 status = %d, want 200", code)
	}
	if gotTenant != tenantA.String() {
		t.Fatalf("step 1 tenant = %q, want %q", gotTenant, tenantA.String())
	}

	// Step 2: simulate admin action — remove user from tenant A.
	// In a real deployment this would be an UPDATE on user_roles
	// where (user_id, tenant_id) = (alice, tenantA). For the R2
	// semantic we don't need to touch the DB; the JWT still carries
	// the same tenant claim, so the middleware still accepts it
	// (eventual eviction).
	code, gotTenant = doRequest("step 2 (post user removal)")
	if code != http.StatusOK {
		t.Fatalf("step 2 status = %d, want 200 (eventual eviction)", code)
	}
	if gotTenant != tenantA.String() {
		t.Fatalf("step 2 tenant = %q, want %q (JWT claim is still trusted)", gotTenant, tenantA.String())
	}

	// Step 3: explicit revocation. The JWT is registered in the
	// revocation store; the next request must 401.
	if err := revoked.Revoke(context.Background(), claims.ID,
		time.Unix(claims.ExpiresAt, 0), "user:test-admin", ""); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	code, _ = doRequest("step 3 (post revoke)")
	if code != http.StatusUnauthorized {
		t.Fatalf("step 3 status = %d, want 401 after revoke", code)
	}
}

// TestR2Eviction_CrossTenantHeaderOverride is the AC-26 test. A
// malicious request that ships an X-Atlas-Tenant header MUST be
// ignored — the JWT claim is the only source of tenant identity.
// P0-190-3 is the binding anti-criterion.
func TestR2Eviction_CrossTenantHeaderOverride(t *testing.T) {
	pool := openIntegrationPool(t)
	ks, err := fsstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("fsstore.Open: %v", err)
	}
	signer := tokensign.New(ks)
	revoked := revocation.New(pool)

	tenantA := uuid.New()
	tenantB := uuid.New()

	now := time.Now()
	claims := jwt.AtlasClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    testIssuer,
			Subject:   "user:alice-cross",
			Audience:  []string{testAudience},
			ExpiresAt: now.Add(time.Hour).Unix(),
			IssuedAt:  now.Unix(),
			NotBefore: now.Unix(),
			ID:        uuid.NewString(),
		},
		IDPIssuer:        "test-idp",
		CurrentTenantID:  tenantA,
		AvailableTenants: []uuid.UUID{tenantA, tenantB},
		Roles:            map[uuid.UUID][]string{tenantA: {"admin"}},
		SuperAdmin:       false,
	}
	tok, err := signer.Sign(context.Background(), claims)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	var captured string
	mw := jwtmw.Middleware(signer, revoked, jwtmw.Options{
		ExpectedIssuer:   testIssuer,
		ExpectedAudience: testAudience,
		Now:              func() int64 { return now.Unix() },
	})
	h := mw(tenantRecorder(&captured))

	req := httptest.NewRequest(http.MethodGet, "/v1/anchors", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("X-Atlas-Tenant", tenantB.String())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if captured != tenantA.String() {
		t.Fatalf("tenant = %q (from header override?), want %q (JWT claim)",
			captured, tenantA.String())
	}
	if captured == tenantB.String() {
		t.Fatalf("CRITICAL: header override succeeded — privilege escalation P0-190-3 violated")
	}
}
