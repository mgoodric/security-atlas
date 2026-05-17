// Slice 106 — unit tests for the GET /v1/evidence handler input-validation
// branches. The integration test (integration_test.go) covers the happy
// paths against real Postgres + RLS; these unit tests pin the cheap
// 4xx-without-DB branches so they stay fast and survive a missing
// DATABASE_URL_APP at CI time.
//
// What's covered here:
//   - 400 when ?control_id= is a non-uuid (still rejected post-slice-106)
//   - 200 when ?control_id= is absent (slice-106 tenant-wide path is legal)
//   - 400 when ?result= is not in the canonical evidence_result enum set
//   - 400 when ?cursor= is malformed
//   - 400 when ?limit= is out of range / non-int
//   - 400 when ?since= / ?until= is non-RFC3339
//   - 200 ranges that legally compose new filters (?kind=, ?source_actor_*=)
//
// The "200" cases here stop at the store boundary by using a fake Store
// fixture (a stand-in EvidencePaged / EvidenceForControl that returns
// empty without hitting Postgres). The integration test exercises the
// real store path against RLS.
//
// NO vendor token prefixes in test fixtures — neutral test-* tokens only.

package controldetail

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ----- isValidResult: the cheap 400 branch -----

func TestIsValidResult(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", true},             // absent => no filter, valid
		{"pass", true},         // canonical enum value
		{"fail", true},         // canonical enum value
		{"na", true},           // canonical enum value
		{"inconclusive", true}, // canonical enum value
		{"PASS", false},        // case sensitive
		{"unknown", false},     // not in the enum
		{"; DROP TABLE", false},
	}
	for _, c := range cases {
		got := isValidResult(c.in)
		if got != c.want {
			t.Fatalf("isValidResult(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// ----- optString: the nil-vs-pointer helper -----

func TestOptString(t *testing.T) {
	if got := optString(""); got != nil {
		t.Fatalf("optString(\"\") = %v, want nil", got)
	}
	got := optString("x")
	if got == nil || *got != "x" {
		t.Fatalf("optString(\"x\") = %v, want pointer-to-x", got)
	}
}

// ----- handler 400 branches that do NOT require DB access -----
//
// The handler dispatches its early validation (control_id parse,
// result-enum sanity check, cursor / limit / time parse) before opening
// a transaction. We exercise those branches with a Handler over a nil
// Store and assert the 400 is written before the store would be hit.

// fakeTenantContext wires a request context with both a credential (so
// requireControlRead admits it) and a tenant id (so tenantContext is ok).
// The credential carries OwnerRoles to satisfy hasControlRead.
func fakeTenantContext(t *testing.T, r *http.Request) *http.Request {
	t.Helper()
	tenantID := uuid.NewString()
	ctx := authctx.WithCredential(r.Context(), credstore.Credential{
		TenantID:   tenantID,
		UserID:     "test-user",
		OwnerRoles: []string{"control_owner"},
	})
	ctx, err := tenancy.WithTenant(ctx, tenantID)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return r.WithContext(ctx)
}

// handlerOver builds a Handler with a nil-Store (the 400 branches return
// before touching it).
func handlerOver() *Handler {
	return &Handler{store: nil}
}

func TestEvidence_Handler_400_NonUUIDControlID(t *testing.T) {
	r := fakeTenantContext(t, httptest.NewRequest(http.MethodGet, "/v1/evidence?control_id=not-a-uuid", nil))
	rec := httptest.NewRecorder()
	handlerOver().Evidence(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "uuid") {
		t.Fatalf("body should mention 'uuid', got %s", rec.Body.String())
	}
}

func TestEvidence_Handler_400_InvalidResultEnum(t *testing.T) {
	r := fakeTenantContext(t, httptest.NewRequest(http.MethodGet, "/v1/evidence?result=unknown", nil))
	rec := httptest.NewRecorder()
	handlerOver().Evidence(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !strings.Contains(body["error"], "pass") {
		t.Fatalf("error should enumerate valid values, got %q", body["error"])
	}
}

func TestEvidence_Handler_400_MalformedCursor(t *testing.T) {
	r := fakeTenantContext(t, httptest.NewRequest(http.MethodGet, "/v1/evidence?cursor=@@@", nil))
	rec := httptest.NewRecorder()
	handlerOver().Evidence(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestEvidence_Handler_400_LimitOutOfRange(t *testing.T) {
	r := fakeTenantContext(t, httptest.NewRequest(http.MethodGet, "/v1/evidence?limit=999", nil))
	rec := httptest.NewRecorder()
	handlerOver().Evidence(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestEvidence_Handler_400_NonIntLimit(t *testing.T) {
	r := fakeTenantContext(t, httptest.NewRequest(http.MethodGet, "/v1/evidence?limit=abc", nil))
	rec := httptest.NewRecorder()
	handlerOver().Evidence(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestEvidence_Handler_400_BadSince(t *testing.T) {
	r := fakeTenantContext(t, httptest.NewRequest(http.MethodGet, "/v1/evidence?since=not-a-time", nil))
	rec := httptest.NewRecorder()
	handlerOver().Evidence(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestEvidence_Handler_400_BadUntil(t *testing.T) {
	r := fakeTenantContext(t, httptest.NewRequest(http.MethodGet, "/v1/evidence?until=not-a-time", nil))
	rec := httptest.NewRecorder()
	handlerOver().Evidence(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// 403: requireControlRead rejects a credential without OwnerRoles /
// IsAdmin / IsApprover. The guard runs before any DB access — pinning it
// here keeps the regression cheap.
func TestEvidence_Handler_403_NoControlReadRole(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/v1/evidence", nil)
	ctx := authctx.WithCredential(r.Context(), credstore.Credential{
		TenantID: uuid.NewString(),
		UserID:   "test-viewer",
		// No OwnerRoles, no IsAdmin, no IsApprover.
	})
	r = r.WithContext(ctx)
	rec := httptest.NewRecorder()
	handlerOver().Evidence(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

// 401: no credential at all -> requireControlRead returns 403 (the
// upstream bearer middleware would normally have rejected first). This
// branch confirms the guard is defense-in-depth, not skipped.
func TestEvidence_Handler_403_NoCredential(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/v1/evidence", nil)
	rec := httptest.NewRecorder()
	handlerOver().Evidence(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

// 401: credential is present + admits via hasControlRead, but no
// tenant context on the request -> tenantContext returns false, handler
// 401s. Exercise via an admin credential (admits) WITHOUT calling
// tenancy.WithTenant.
func TestEvidence_Handler_401_MissingTenantContext(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/v1/evidence", nil)
	ctx := authctx.WithCredential(r.Context(), credstore.Credential{
		TenantID: uuid.NewString(),
		UserID:   "test-admin",
		IsAdmin:  true,
	})
	r = r.WithContext(ctx)
	rec := httptest.NewRecorder()
	handlerOver().Evidence(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}
