package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/platform"
)

// fakePlatformStatus is a hand-rolled PlatformStatus that returns the
// configured first_install + mark_signin behaviour. Lets the handler
// tests run without a database.
type fakePlatformStatus struct {
	firstInstall    bool
	firstInstallErr error
	markErr         error
	markDid         bool
	markCalls       int

	// Slice 210 — bootstrap-tenant-id lookup. Tracks call count so the
	// slice-210 P0-A3 test can assert the handler does NOT call this
	// method on the post-first-install path.
	bootstrapTenantID    uuid.UUID
	bootstrapTenantErr   error
	bootstrapTenantCalls int

	// #1756 — tenant count behind single_tenant.
	tenantCount    int
	tenantCountErr error
}

func (f *fakePlatformStatus) IsFirstInstall(_ context.Context) (bool, error) {
	return f.firstInstall, f.firstInstallErr
}

func (f *fakePlatformStatus) MarkFirstSignin(_ context.Context, _ time.Time) (bool, error) {
	f.markCalls++
	return f.markDid, f.markErr
}

func (f *fakePlatformStatus) BootstrapTenantID(_ context.Context) (uuid.UUID, error) {
	f.bootstrapTenantCalls++
	return f.bootstrapTenantID, f.bootstrapTenantErr
}

func (f *fakePlatformStatus) TenantCount(_ context.Context) (int, error) {
	return f.tenantCount, f.tenantCountErr
}

// #1756 — single_tenant is reported after first install too, from the
// tenant count, and never names the tenant.
func TestHandleInstallState_SingleTenant(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		fake fakePlatformStatus
		want bool
	}{
		{name: "one tenant after first install", fake: fakePlatformStatus{tenantCount: 1}, want: true},
		{name: "one tenant during first install", fake: fakePlatformStatus{firstInstall: true, tenantCount: 1}, want: true},
		{name: "two tenants", fake: fakePlatformStatus{tenantCount: 2}, want: false},
		{name: "no tenant", fake: fakePlatformStatus{tenantCount: 0}, want: false},
		{name: "count failure degrades to false", fake: fakePlatformStatus{tenantCount: 1, tenantCountErr: errors.New("db down")}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := tc.fake
			srv := New(Config{})
			srv.AttachPlatformStatus(&fake)
			rec := httptest.NewRecorder()
			srv.handleInstallState(rec, httptest.NewRequest(http.MethodGet, "/v1/install-state", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d; want 200", rec.Code)
			}
			var body struct {
				SingleTenant *bool `json:"single_tenant"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.SingleTenant == nil || *body.SingleTenant != tc.want {
				t.Fatalf("single_tenant = %v; want %v (body %q)", body.SingleTenant, tc.want, rec.Body.String())
			}
			if !tc.fake.firstInstall && strings.Contains(rec.Body.String(), "tenant_id") {
				t.Fatalf("post-first-install body names a tenant: %q", rec.Body.String())
			}
		})
	}
}

func TestHandleInstallState_FreshInstall(t *testing.T) {
	srv := New(Config{})
	srv.AttachPlatformStatus(&fakePlatformStatus{firstInstall: true})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/install-state", nil)
	srv.handleInstallState(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q; want no-store", cc)
	}
	var body installStateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.FirstInstall {
		t.Fatalf("first_install = false; want true")
	}
}

func TestHandleInstallState_AlreadySignedIn(t *testing.T) {
	srv := New(Config{})
	srv.AttachPlatformStatus(&fakePlatformStatus{firstInstall: false})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/install-state", nil)
	srv.handleInstallState(rec, req)

	var body installStateResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.FirstInstall {
		t.Fatalf("first_install = true; want false after sign-in")
	}
}

func TestHandleInstallState_NotConfigured_503(t *testing.T) {
	srv := New(Config{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/install-state", nil)
	srv.handleInstallState(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503 when PlatformStatus is nil", rec.Code)
	}
}

func TestHandleInstallState_ReadError_503(t *testing.T) {
	srv := New(Config{})
	srv.AttachPlatformStatus(&fakePlatformStatus{firstInstallErr: errors.New("boom")})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/install-state", nil)
	srv.handleInstallState(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503 on read failure", rec.Code)
	}
}

// Slice 210 — fresh-install responses include the bootstrap tenant id
// so the slice 209 login form can auto-populate its hidden tenant_id
// field without forcing the operator to type a UUID.
func TestHandleInstallState_FreshInstall_IncludesTenantID(t *testing.T) {
	want := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	srv := New(Config{})
	srv.AttachPlatformStatus(&fakePlatformStatus{
		firstInstall:      true,
		bootstrapTenantID: want,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/install-state", nil)
	srv.handleInstallState(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	var body installStateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.FirstInstall {
		t.Fatalf("first_install = false; want true")
	}
	if body.TenantID != want.String() {
		t.Fatalf("tenant_id = %q; want %q", body.TenantID, want.String())
	}
}

// Slice 210 — when the bootstrap-tenant lookup errors, the endpoint
// stays HTTP 200 with `tenant_id` omitted (degrade gracefully).
// `omitempty` keeps the response shape clean — slice 209's FE checks
// `body.tenant_id` truthiness so both undefined-field and explicit-empty
// would behave identically, but `omitempty` is the cleaner contract.
func TestHandleInstallState_FreshInstall_BootstrapTenantErrorOmits(t *testing.T) {
	srv := New(Config{})
	srv.AttachPlatformStatus(&fakePlatformStatus{
		firstInstall:       true,
		bootstrapTenantErr: errors.New("primary lookup failed"),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/install-state", nil)
	srv.handleInstallState(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 on tenant-lookup failure (degraded-gracefully)", rec.Code)
	}
	// Check the raw body — omitempty must drop the field entirely.
	if strings.Contains(rec.Body.String(), "tenant_id") {
		t.Fatalf("body contains tenant_id; want field omitted: %q", rec.Body.String())
	}
	var body installStateResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if !body.FirstInstall {
		t.Fatalf("first_install = false; want true")
	}
	if body.TenantID != "" {
		t.Fatalf("tenant_id = %q; want empty", body.TenantID)
	}
}

// Slice 210 — when the bootstrap-tenant lookup returns uuid.Nil
// (no error, but no resolvable tenant — empty install), the endpoint
// stays HTTP 200 with `tenant_id` omitted. Same observable shape as
// the error path; both are graceful-degradation states.
func TestHandleInstallState_FreshInstall_BootstrapTenantNilOmits(t *testing.T) {
	srv := New(Config{})
	srv.AttachPlatformStatus(&fakePlatformStatus{
		firstInstall:      true,
		bootstrapTenantID: uuid.Nil, // explicit zero
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/install-state", nil)
	srv.handleInstallState(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "tenant_id") {
		t.Fatalf("body contains tenant_id on uuid.Nil; want omitted: %q", rec.Body.String())
	}
}

// Slice 210 P0-A3 — post-first-install responses MUST NOT change.
// Critically, the bootstrap-tenant lookup must NOT be called when
// first_install=false (avoids the wasted DB query and preserves the
// "this endpoint is a 1-row read" perf characteristic).
func TestHandleInstallState_PostFirstInstall_OmitsTenantID_NoLookup(t *testing.T) {
	fake := &fakePlatformStatus{firstInstall: false}
	srv := New(Config{})
	srv.AttachPlatformStatus(fake)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/install-state", nil)
	srv.handleInstallState(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "tenant_id") {
		t.Fatalf("body contains tenant_id on post-first-install; want omitted: %q", rec.Body.String())
	}
	if fake.bootstrapTenantCalls != 0 {
		t.Fatalf("BootstrapTenantID called %d times on post-first-install; want 0 (P0-A3)", fake.bootstrapTenantCalls)
	}
}

func TestHandleMarkFirstSignin_HappyPath(t *testing.T) {
	srv := New(Config{})
	srv.AttachPlatformStatus(&fakePlatformStatus{markDid: true})

	rec := httptest.NewRecorder()
	req := adminMarkFirstSigninRequest()
	srv.handleMarkFirstSignin(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	var body markFirstSigninResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Marked {
		t.Fatalf("marked = false; want true")
	}
}

func TestHandleMarkFirstSignin_Idempotent(t *testing.T) {
	fake := &fakePlatformStatus{markDid: false}
	srv := New(Config{})
	srv.AttachPlatformStatus(fake)

	rec := httptest.NewRecorder()
	req := adminMarkFirstSigninRequest()
	srv.handleMarkFirstSignin(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	var body markFirstSigninResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Marked {
		t.Fatalf("marked = true; want false on idempotent re-call")
	}
}

func TestHandleMarkFirstSignin_WriteNotConfigured_503(t *testing.T) {
	srv := New(Config{})
	srv.AttachPlatformStatus(&fakePlatformStatus{markErr: platform.ErrWriteNotConfigured})

	rec := httptest.NewRecorder()
	req := adminMarkFirstSigninRequest()
	srv.handleMarkFirstSignin(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503 when write pool is not configured", rec.Code)
	}
}

func TestHandleMarkFirstSignin_NotConfigured_503(t *testing.T) {
	srv := New(Config{})
	rec := httptest.NewRecorder()
	req := adminMarkFirstSigninRequest()
	srv.handleMarkFirstSignin(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503 when PlatformStatus is nil", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "not configured") {
		t.Fatalf("body = %q; want 'not configured'", rec.Body.String())
	}
}

// adminMarkFirstSigninRequest builds a mark-first-signin request carrying
// an admin credential, which the handler requires (#1906).
func adminMarkFirstSigninRequest() *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/v1/install/mark-first-signin", nil)
	return req.WithContext(authctx.WithCredential(req.Context(), credstore.Credential{ID: "admin", IsAdmin: true}))
}
