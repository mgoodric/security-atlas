// tenancy_test.go — #1763 unit tests for GET /v1/me/tenancy. The
// count itself is platform.Status.TenantCount, exercised against real
// Postgres in internal/platform (TestStatus_TenantCount); these tests
// pin the handler's response shape and its failure modes.
package me

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
)

type fakeCounter struct {
	n   int
	err error
}

func (f fakeCounter) TenantCount(context.Context) (int, error) { return f.n, f.err }

func tenancyRequest(authed bool) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/v1/me/tenancy", nil)
	if authed {
		ctx := authctx.WithCredential(req.Context(), credstore.Credential{ID: "test", TenantID: "t"})
		req = req.WithContext(ctx)
	}
	return req
}

func TestTenancy_ReportsCount(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		n     int
		multi bool
	}{{1, false}, {2, true}, {5, true}} {
		rec := httptest.NewRecorder()
		NewTenancy(fakeCounter{n: tc.n}).Get(rec, tenancyRequest(true))
		if rec.Code != http.StatusOK {
			t.Fatalf("n=%d: status %d, want 200", tc.n, rec.Code)
		}
		var got tenancyResponse
		if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.TenantCount != tc.n || got.MultiTenant != tc.multi {
			t.Fatalf("n=%d: got %+v, want tenant_count=%d multi_tenant=%v", tc.n, got, tc.n, tc.multi)
		}
	}
}

func TestTenancy_Unauthenticated401(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	NewTenancy(fakeCounter{n: 1}).Get(rec, tenancyRequest(false))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
}

func TestTenancy_NoCounter503(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	NewTenancy(nil).Get(rec, tenancyRequest(true))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", rec.Code)
	}
}

func TestTenancy_CountError500(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	NewTenancy(fakeCounter{err: errors.New("boom")}).Get(rec, tenancyRequest(true))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}
}
