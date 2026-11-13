package auth

// Unit tests for #1727: POST /auth/local/login defaults tenant_id to the
// only tenant on a single-tenant install, refuses the all-zeros tenant, and
// answers a multi-tenant (or empty) install with a 400 that names nothing.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/tenancy"
)

type fakeImplicitTenant struct {
	id  uuid.UUID
	err error
}

func (f fakeImplicitTenant) ImplicitTenant(context.Context) (uuid.UUID, error) { return f.id, f.err }

func TestLoginTenant(t *testing.T) {
	t.Parallel()
	only := uuid.MustParse("6f1c2b8e-1d3a-4c55-9e0f-3b2a1c4d5e6f")
	other := uuid.MustParse("0a9b8c7d-6e5f-4a3b-8c1d-2e3f4a5b6c7d")
	ambiguous := &tenancy.AmbiguousTenantError{Tenants: []tenancy.TenantRef{
		{ID: only, Slug: "alpha-org", Name: "Alpha Org"},
		{ID: other, Slug: "beta-org", Name: "Beta Org"},
	}}

	cases := []struct {
		name       string
		raw        string
		resolver   ImplicitTenantResolver
		wantID     uuid.UUID
		wantStatus int
		wantMsg    string
	}{
		{name: "single tenant default", resolver: fakeImplicitTenant{id: only}, wantID: only},
		{name: "explicit tenant wins over resolver", raw: other.String(), resolver: fakeImplicitTenant{id: only}, wantID: other},
		{name: "multi tenant 400", resolver: fakeImplicitTenant{err: ambiguous}, wantStatus: http.StatusBadRequest, wantMsg: ErrMsgTenantIDRequired},
		{name: "no tenant 400 same message", resolver: fakeImplicitTenant{err: tenancy.ErrNoTenants}, wantStatus: http.StatusBadRequest, wantMsg: ErrMsgTenantIDRequired},
		{name: "resolver returning all-zeros refused", resolver: fakeImplicitTenant{id: uuid.Nil}, wantStatus: http.StatusBadRequest, wantMsg: ErrMsgTenantIDRequired},
		{name: "explicit all-zeros refused", raw: uuid.Nil.String(), resolver: fakeImplicitTenant{id: only}, wantStatus: http.StatusBadRequest, wantMsg: "invalid tenant_id"},
		{name: "malformed tenant_id", raw: "not-a-uuid", resolver: fakeImplicitTenant{id: only}, wantStatus: http.StatusBadRequest, wantMsg: "invalid tenant_id"},
		{name: "no resolver keeps tenant_id required", wantStatus: http.StatusBadRequest, wantMsg: ErrMsgTenantIDRequired},
		{name: "resolver failure is a generic 500", resolver: fakeImplicitTenant{err: errors.New("db down")}, wantStatus: http.StatusInternalServerError, wantMsg: "login failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			id, status, msg := loginTenant(context.Background(), tc.raw, tc.resolver)
			if id != tc.wantID || status != tc.wantStatus || msg != tc.wantMsg {
				t.Fatalf("loginTenant(%q) = (%s, %d, %q), want (%s, %d, %q)",
					tc.raw, id, status, msg, tc.wantID, tc.wantStatus, tc.wantMsg)
			}
		})
	}
}

// The multi-tenant 400 reaches an unauthenticated caller, so the body must
// not name or count the tenants.
func TestLocalLogin_MultiTenant400NamesNoTenant(t *testing.T) {
	t.Parallel()
	a, b := uuid.New(), uuid.New()
	h := &Handler{implicitTenant: fakeImplicitTenant{err: &tenancy.AmbiguousTenantError{Tenants: []tenancy.TenantRef{
		{ID: a, Slug: "alpha-org", Name: "Alpha Org"},
		{ID: b, Slug: "beta-org", Name: "Beta Org"},
	}}}}
	req := httptest.NewRequest(http.MethodPost, "/auth/local/login",
		strings.NewReader(`{"email":"a@example.com","password":"x"}`))
	rec := httptest.NewRecorder()
	h.LocalLogin(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, ErrMsgTenantIDRequired) {
		t.Errorf("body %q lacks %q", body, ErrMsgTenantIDRequired)
	}
	for _, leak := range []string{a.String(), b.String(), "alpha-org", "beta-org", "Alpha Org", "Beta Org", "2"} {
		if strings.Contains(body, leak) {
			t.Errorf("400 body discloses %q: %s", leak, body)
		}
	}
}

func TestLocalLogin_ExplicitAllZerosTenantRefused(t *testing.T) {
	t.Parallel()
	h := &Handler{implicitTenant: fakeImplicitTenant{id: uuid.New()}}
	req := httptest.NewRequest(http.MethodPost, "/auth/local/login",
		strings.NewReader(`{"tenant_id":"00000000-0000-0000-0000-000000000000","email":"a@example.com","password":"x"}`))
	rec := httptest.NewRecorder()
	h.LocalLogin(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
