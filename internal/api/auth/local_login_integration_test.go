//go:build integration

// Handler-level integration tests for #1727: POST /auth/local/login without
// tenant_id, against real Postgres with the BYPASSRLS implicit-tenant
// resolver wired as cmd/atlas wires it.
//
// Run via: go test -tags=integration ./internal/api/auth/...
// (needs DATABASE_URL = BYPASSRLS migrate role, DATABASE_URL_APP = atlas_app).
package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	authapi "github.com/mgoodric/security-atlas/internal/api/auth"
	"github.com/mgoodric/security-atlas/internal/auth/oidc"
	"github.com/mgoodric/security-atlas/internal/auth/sessions"
	"github.com/mgoodric/security-atlas/internal/auth/users"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const (
	loginEmail    = "solo@example.com"
	loginPassword = "correct horse battery staple"
)

func (h harness) localLoginHandler() *authapi.Handler {
	handler := authapi.New(
		oidc.New(stubResolver{}),
		users.NewStoreWithAuthPool(h.app, h.admin),
		sessions.NewStore(h.app, time.Hour),
		false,
		nil,
	)
	handler.AttachImplicitTenantResolver(authapi.PoolImplicitTenantResolver{Q: h.admin})
	return handler
}

func (h harness) createLocalUser(t *testing.T, tenantID uuid.UUID) {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenantID.String())
	if err != nil {
		t.Fatalf("tenant ctx: %v", err)
	}
	if _, err := users.NewStore(h.app).CreateLocal(ctx, users.CreateLocalInput{
		TenantID: tenantID, Email: loginEmail, DisplayName: "Solo", Password: loginPassword,
	}); err != nil {
		t.Fatalf("CreateLocal: %v", err)
	}
}

func postLogin(handler *authapi.Handler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/auth/local/login", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.LocalLogin(rec, req)
	return rec
}

// One real tenant plus the all-zeros row: a login without tenant_id lands
// in the real tenant, never the all-zeros one.
func TestLocalLogin_SingleTenant_DefaultsTenantID(t *testing.T) {
	h := newHarness(t)
	h.insertTenant(t, uuid.Nil, "nil", "nil-tenant", false)
	only := uuid.New()
	h.insertTenant(t, only, "Solo Org", "solo-org", true)
	h.createLocalUser(t, only)

	rec := postLogin(h.localLoginHandler(), `{"email":"`+loginEmail+`","password":"`+loginPassword+`"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), only.String()) {
		t.Errorf("response does not carry the tenant %s: %s", only, rec.Body.String())
	}
	if n := h.count(t, `SELECT COUNT(*) FROM sessions WHERE tenant_id = $1`, only); n != 1 {
		t.Errorf("sessions in the only tenant = %d, want 1", n)
	}

	// Wrong password and unknown email are the same 401.
	for _, body := range []string{
		`{"email":"` + loginEmail + `","password":"wrong"}`,
		`{"email":"nobody@example.com","password":"wrong"}`,
	} {
		rec := postLogin(h.localLoginHandler(), body)
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "invalid credentials") {
			t.Errorf("%s: got %d %s, want 401 invalid credentials", body, rec.Code, rec.Body.String())
		}
	}
}

// Two real tenants: no tenant is implied, and the 400 names neither.
func TestLocalLogin_MultiTenant_RequiresTenantID(t *testing.T) {
	h := newHarness(t)
	a, b := uuid.New(), uuid.New()
	h.insertTenant(t, a, "Alpha Org", "alpha-org", true)
	h.insertTenant(t, b, "Beta Org", "beta-org", false)
	h.createLocalUser(t, a)

	rec := postLogin(h.localLoginHandler(), `{"email":"`+loginEmail+`","password":"`+loginPassword+`"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, authapi.ErrMsgTenantIDRequired) {
		t.Errorf("body %q lacks %q", body, authapi.ErrMsgTenantIDRequired)
	}
	for _, leak := range []string{a.String(), b.String(), "alpha-org", "beta-org", "Alpha Org", "Beta Org"} {
		if strings.Contains(body, leak) {
			t.Errorf("400 body discloses %q: %s", leak, body)
		}
	}
	if n := h.count(t, `SELECT COUNT(*) FROM sessions`); n != 0 {
		t.Errorf("sessions = %d, want 0", n)
	}

	// The explicit tenant still works.
	rec = postLogin(h.localLoginHandler(), `{"tenant_id":"`+a.String()+`","email":"`+loginEmail+`","password":"`+loginPassword+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("explicit tenant: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
}

// Only the all-zeros row: it is not a tenant, so nothing is implied, and
// naming it explicitly is refused.
func TestLocalLogin_AllZerosTenantRefused(t *testing.T) {
	h := newHarness(t)
	h.insertTenant(t, uuid.Nil, "nil", "nil-tenant", false)

	rec := postLogin(h.localLoginHandler(), `{"email":"`+loginEmail+`","password":"x"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), authapi.ErrMsgTenantIDRequired) {
		t.Errorf("implicit: got %d %s, want 400 %q", rec.Code, rec.Body.String(), authapi.ErrMsgTenantIDRequired)
	}
	rec = postLogin(h.localLoginHandler(), `{"tenant_id":"`+uuid.Nil.String()+`","email":"`+loginEmail+`","password":"x"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("explicit all-zeros: got %d %s, want 400", rec.Code, rec.Body.String())
	}
}

// #1912: a deactivated user with the right password gets the same 401 as a
// wrong password. Admin revoke-membership sets status='disabled'; SCIM
// deprovision sets active=false (and status in lockstep). Either flag alone
// refuses the login, and no session is minted.
func TestLocalLogin_DeactivatedUserRefused(t *testing.T) {
	for name, deactivate := range map[string]string{
		"status disabled": `UPDATE users SET status = 'disabled' WHERE tenant_id = $1 AND email = $2`,
		"scim inactive":   `UPDATE users SET active = false WHERE tenant_id = $1 AND email = $2`,
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			only := uuid.New()
			h.insertTenant(t, only, "Solo Org", "solo-org", true)
			h.createLocalUser(t, only)
			if _, err := h.admin.Exec(context.Background(), deactivate, only, loginEmail); err != nil {
				t.Fatalf("deactivate: %v", err)
			}

			rec := postLogin(h.localLoginHandler(), `{"email":"`+loginEmail+`","password":"`+loginPassword+`"}`)

			if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "invalid credentials") {
				t.Errorf("got %d %s, want 401 invalid credentials", rec.Code, rec.Body.String())
			}
			if n := h.count(t, `SELECT COUNT(*) FROM sessions`); n != 0 {
				t.Errorf("sessions = %d, want 0", n)
			}
		})
	}
}
