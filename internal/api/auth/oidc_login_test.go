package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/auth/oidc"
)

type oidcLoginTestResolver struct {
	cfg oidc.IdpConfig
	err error
}

func (r oidcLoginTestResolver) ResolveIdp(_ context.Context, _ uuid.UUID, _ string) (oidc.IdpConfig, error) {
	if r.err != nil {
		return oidc.IdpConfig{}, r.err
	}
	return r.cfg, nil
}

func TestOIDCLoginProviderUnavailableReturnsDesign503(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	idp := httptest.NewServer(mux)
	t.Cleanup(idp.Close)

	tenantID := uuid.New()
	authenticator := oidc.NewWithDiscoveryTimeout(oidcLoginTestResolver{cfg: oidc.IdpConfig{
		ID:           uuid.New(),
		TenantID:     tenantID,
		Name:         "primary",
		IssuerURL:    idp.URL,
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		RedirectURL:  "http://localhost:9999/auth/oidc/callback",
	}}, 25*time.Millisecond)
	handler := New(authenticator, nil, nil, false, nil)

	req := httptest.NewRequest(http.MethodGet, "/auth/oidc/login?tenant_id="+tenantID.String()+"&idp=primary", nil)
	rec := httptest.NewRecorder()

	start := time.Now()
	handler.OIDCLogin(rec, req)
	elapsed := time.Since(start)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("OIDCLogin took %v; discovery outage path is not bounded", elapsed)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not JSON: %v", err)
	}
	if body["error"] != "auth_provider_unavailable" {
		t.Fatalf("error = %v, want auth_provider_unavailable", body["error"])
	}
	if body["retry_after"] != float64(30) {
		t.Fatalf("retry_after = %v, want 30", body["retry_after"])
	}
	if len(body) != 2 {
		t.Fatalf("body keys = %#v, want exactly error + retry_after", body)
	}
	assertNoInternalLeak(t, rec.Body.String())
}

func TestOIDCLoginUnknownIdpStillReturnsExisting4xx(t *testing.T) {
	tenantID := uuid.New()
	handler := New(oidc.New(oidcLoginTestResolver{err: oidc.ErrUnknownIdp}), nil, nil, false, nil)
	req := httptest.NewRequest(http.MethodGet, "/auth/oidc/login?tenant_id="+tenantID.String()+"&idp=missing", nil)
	rec := httptest.NewRecorder()

	handler.OIDCLogin(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not JSON: %v", err)
	}
	if body["error"] != "OIDC begin: oidc: unknown IdP" {
		t.Fatalf("error = %q, want existing unknown-IdP body", body["error"])
	}
	if strings.Contains(rec.Body.String(), "auth_provider_unavailable") {
		t.Fatalf("unknown IdP response was not distinguishable from outage: %s", rec.Body.String())
	}
	assertNoInternalLeak(t, rec.Body.String())
}

func assertNoInternalLeak(t *testing.T, body string) {
	t.Helper()
	for _, forbidden := range []string{"internal/", ".go:", "goroutine ", "\t"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("response body leaked internal detail %q: %s", forbidden, body)
		}
	}
}

// #1761: idp stays required; without an implicit-tenant resolver,
// tenant_id stays required too, and each 400 names its parameter.
func TestOIDCLoginRequiredParams(t *testing.T) {
	handler := New(oidc.New(oidcLoginTestResolver{err: oidc.ErrUnknownIdp}), nil, nil, false, nil)
	for query, want := range map[string]string{
		"tenant_id=" + uuid.NewString():              "idp query parameter is required",
		"idp=primary":                                ErrMsgTenantIDRequired,
		"idp=primary&tenant_id=nope":                 "invalid tenant_id",
		"idp=primary&tenant_id=" + uuid.Nil.String(): "invalid tenant_id",
	} {
		rec := httptest.NewRecorder()
		handler.OIDCLogin(rec, httptest.NewRequest(http.MethodGet, "/auth/oidc/login?"+query, nil))
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: got %d %s, want 400 %q", query, rec.Code, rec.Body.String(), want)
		}
	}
}

// #1761: the callback without tenant_id and without a resolver is a 400
// naming tenant_id, before any flow-cookie or IdP work.
func TestOIDCCallbackRequiresTenantWithoutResolver(t *testing.T) {
	handler := New(oidc.New(oidcLoginTestResolver{err: oidc.ErrUnknownIdp}), nil, nil, false, nil)
	rec := httptest.NewRecorder()
	handler.OIDCCallback(rec, httptest.NewRequest(http.MethodGet, "/auth/oidc/callback?code=c&state=s", nil))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), ErrMsgTenantIDRequired) {
		t.Errorf("got %d %s, want 400 %q", rec.Code, rec.Body.String(), ErrMsgTenantIDRequired)
	}
}
