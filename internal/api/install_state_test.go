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
}

func (f *fakePlatformStatus) IsFirstInstall(_ context.Context) (bool, error) {
	return f.firstInstall, f.firstInstallErr
}

func (f *fakePlatformStatus) MarkFirstSignin(_ context.Context, _ time.Time) (bool, error) {
	f.markCalls++
	return f.markDid, f.markErr
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

func TestHandleMarkFirstSignin_HappyPath(t *testing.T) {
	srv := New(Config{})
	srv.AttachPlatformStatus(&fakePlatformStatus{markDid: true})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/install/mark-first-signin", nil)
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
	req := httptest.NewRequest(http.MethodPost, "/v1/install/mark-first-signin", nil)
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
	req := httptest.NewRequest(http.MethodPost, "/v1/install/mark-first-signin", nil)
	srv.handleMarkFirstSignin(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503 when write pool is not configured", rec.Code)
	}
}

func TestHandleMarkFirstSignin_NotConfigured_503(t *testing.T) {
	srv := New(Config{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/install/mark-first-signin", nil)
	srv.handleMarkFirstSignin(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503 when PlatformStatus is nil", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "not configured") {
		t.Fatalf("body = %q; want 'not configured'", rec.Body.String())
	}
}
