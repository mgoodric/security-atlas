// tenants_test.go — slice 192 unit tests for GET /v1/me/tenants.
//
// The handler reads jwtmw.FromContext(ctx) for verified claims, so
// these tests inject claims via jwtmw.WithClaimsForTest. The
// authPool is nil — name enrichment is skipped (the test asserts
// the IDs + current flag are honest; name enrichment integration
// is covered by the integration test against a real Postgres).
//
// ACs covered:
//   - AC-1: handler exists at internal/api/me/tenants.go
//   - AC-2: response carries tenants[].current flagging
//   - AC-3: bounded to JWT claim (no fallthrough to a table scan)
//   - AC-17: 1-tenant vs N-tenant return shape correctness
//   - P0-192-2: handler reads JWT claim only, no full SELECT
package me

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
)

// withClaims is a test helper to inject AtlasClaims for handlers
// that read jwtmw.FromContext.
func withClaimsRequest(claims *jwt.AtlasClaims) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/v1/me/tenants", nil)
	ctx := jwtmw.WithClaimsForTest(req.Context(), claims)
	return req.WithContext(ctx)
}

// TestListTenants_NoClaims asserts 401 when no JWT context is set
// — production this is guarded by jwtmw upstream, but the handler
// MUST fail closed if reached without a claim.
func TestListTenants_NoClaims(t *testing.T) {
	h := NewTenants(nil)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/me/tenants", nil)
	h.ListTenants(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

// TestListTenants_EmptyAvailable asserts that a JWT with zero
// available_tenants returns an empty list (the frontend hides the
// switcher chrome).
func TestListTenants_EmptyAvailable(t *testing.T) {
	claims := &jwt.AtlasClaims{
		AvailableTenants: nil,
		CurrentTenantID:  uuid.Nil,
	}
	h := NewTenants(nil)
	rr := httptest.NewRecorder()
	h.ListTenants(rr, withClaimsRequest(claims))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var body listTenantsResponse
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Tenants) != 0 {
		t.Fatalf("expected zero tenants, got %d", len(body.Tenants))
	}
}

// TestListTenants_SingleTenant asserts a 1-tenant claim returns a
// 1-entry response with current=true. The frontend will then hide
// the switcher (canvas §11 #13) — but the BACKEND returns honest
// data; the hide is frontend-side enforcement.
func TestListTenants_SingleTenant(t *testing.T) {
	tenant := uuid.New()
	claims := &jwt.AtlasClaims{
		AvailableTenants: []uuid.UUID{tenant},
		CurrentTenantID:  tenant,
	}
	h := NewTenants(nil)
	rr := httptest.NewRecorder()
	h.ListTenants(rr, withClaimsRequest(claims))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var body listTenantsResponse
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Tenants) != 1 {
		t.Fatalf("expected 1 tenant, got %d", len(body.Tenants))
	}
	row := body.Tenants[0]
	if row.ID != tenant.String() {
		t.Fatalf("expected tenant id %s, got %s", tenant, row.ID)
	}
	if !row.Current {
		t.Fatal("expected current=true for the single tenant")
	}
}

// TestListTenants_MultiTenant asserts an N-tenant claim returns N
// entries with the current flag set on exactly the
// current_tenant_id. Authpool is nil so names are empty — this is
// the test-harness path; integration tests cover name enrichment.
func TestListTenants_MultiTenant(t *testing.T) {
	a := uuid.New()
	b := uuid.New()
	c := uuid.New()
	claims := &jwt.AtlasClaims{
		AvailableTenants: []uuid.UUID{a, b, c},
		CurrentTenantID:  b,
	}
	h := NewTenants(nil)
	rr := httptest.NewRecorder()
	h.ListTenants(rr, withClaimsRequest(claims))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var body listTenantsResponse
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Tenants) != 3 {
		t.Fatalf("expected 3 tenants, got %d", len(body.Tenants))
	}
	// Verify ordering matches the claim order (the handler iterates
	// claims.AvailableTenants in input order — important for the
	// frontend's deterministic UI).
	ids := []string{body.Tenants[0].ID, body.Tenants[1].ID, body.Tenants[2].ID}
	want := []string{a.String(), b.String(), c.String()}
	for i := range ids {
		if ids[i] != want[i] {
			t.Fatalf("position %d: want %s, got %s", i, want[i], ids[i])
		}
	}
	if body.Tenants[0].Current || !body.Tenants[1].Current || body.Tenants[2].Current {
		t.Fatalf("current flag mismatch: %v %v %v",
			body.Tenants[0].Current, body.Tenants[1].Current, body.Tenants[2].Current)
	}
}

// TestListTenants_CurrentNotInAvailable covers the
// membership-removed-style transition: the JWT was issued when the
// caller's current_tenant was in available_tenants, then the
// available list shrank (= they were removed) but the JWT still
// carries the old current. The handler must still return a list
// without crashing; current=false on every row, and the frontend's
// MembershipRemovedBanner kicks in.
func TestListTenants_CurrentNotInAvailable(t *testing.T) {
	a := uuid.New()
	b := uuid.New()
	removed := uuid.New()
	claims := &jwt.AtlasClaims{
		AvailableTenants: []uuid.UUID{a, b},
		CurrentTenantID:  removed,
	}
	h := NewTenants(nil)
	rr := httptest.NewRecorder()
	h.ListTenants(rr, withClaimsRequest(claims))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var body listTenantsResponse
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Tenants) != 2 {
		t.Fatalf("expected 2 tenants, got %d", len(body.Tenants))
	}
	for i, row := range body.Tenants {
		if row.Current {
			t.Fatalf("row %d: current must be false when current_tenant_id is not in available list", i)
		}
	}
}
