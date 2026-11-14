//go:build integration

// Handler-level integration tests for security-atlas#1759: an OIDC first
// login through GET /auth/oidc/callback, driven end to end against an
// in-process fake IdP (discovery + JWKS + token endpoint, same shape as
// internal/auth/oidc/oidc_nonce_integration_test.go) and real Postgres.
//
//   - AC1: one seeded bootstrap tenant, no users → redirect + session cookie,
//     the user and the session land in that tenant, still exactly one tenant.
//   - AC3: two tenants, no users → 409, nothing written, and the body names
//     no tenant (UUID, slug or name).
//
// Run via: go test -tags=integration ./internal/api/auth/...
// (needs DATABASE_URL = BYPASSRLS migrate role, DATABASE_URL_APP = atlas_app).
package auth_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	authapi "github.com/mgoodric/security-atlas/internal/api/auth"
	"github.com/mgoodric/security-atlas/internal/auth/oidc"
	"github.com/mgoodric/security-atlas/internal/auth/sessions"
	"github.com/mgoodric/security-atlas/internal/auth/users"
)

const fakeClientID = "atlas-test-client"

// fakeIdP is a minimal OIDC provider: it signs ID tokens with a local RSA
// key and mints one, carrying the nonce under test, at /token.
type fakeIdP struct {
	srv    *httptest.Server
	signer jose.Signer
	pub    *rsa.PublicKey
	nonce  string
	// emailVerified, when non-nil, is sent as the email_verified claim.
	emailVerified any
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{
		Algorithm: jose.RS256,
		Key:       jose.JSONWebKey{Key: priv, KeyID: "kid-1", Algorithm: string(jose.RS256), Use: "sig"},
	}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "kid-1"))
	if err != nil {
		t.Fatalf("jose.NewSigner: %v", err)
	}
	f := &fakeIdP{signer: signer, pub: &priv.PublicKey}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		iss := f.srv.URL
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                iss,
			"authorization_endpoint":                iss + "/authorize",
			"token_endpoint":                        iss + "/token",
			"jwks_uri":                              iss + "/jwks",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
			{Key: f.pub, KeyID: "kid-1", Algorithm: string(jose.RS256), Use: "sig"},
		}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		claims := map[string]any{
			"iss":   f.srv.URL,
			"sub":   "oidc-founder",
			"aud":   fakeClientID,
			"exp":   time.Now().Add(5 * time.Minute).Unix(),
			"iat":   time.Now().Unix(),
			"email": "founder@example.com",
			"name":  "Founder",
			"nonce": f.nonce,
		}
		if f.emailVerified != nil {
			claims["email_verified"] = f.emailVerified
		}
		tok, err := jwt.Signed(f.signer).Claims(claims).Serialize()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "stub", "token_type": "Bearer", "id_token": tok, "expires_in": 3600,
		})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

type stubResolver struct{ cfg oidc.IdpConfig }

func (s stubResolver) ResolveIdp(_ context.Context, _ uuid.UUID, _ string) (oidc.IdpConfig, error) {
	return s.cfg, nil
}

type harness struct {
	app, admin *pgxpool.Pool
}

func newHarness(t *testing.T) harness {
	t.Helper()
	appURL, adminURL := os.Getenv("DATABASE_URL_APP"), os.Getenv("DATABASE_URL")
	if appURL == "" || adminURL == "" {
		t.Skip("DATABASE_URL_APP or DATABASE_URL not set; skipping #1759 handler integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	app, err := pgxpool.New(ctx, appURL)
	if err != nil {
		t.Fatalf("app pool: %v", err)
	}
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	h := harness{app: app, admin: admin}
	h.reset(t)
	t.Cleanup(func() {
		h.reset(t)
		app.Close()
		admin.Close()
	})
	return h
}

// reset returns the identity tables to an empty install.
func (h harness) reset(t *testing.T) {
	t.Helper()
	for _, s := range []string{
		`DELETE FROM sessions`,
		`DELETE FROM me_audit_log WHERE action = 'bootstrap_first_install'`,
		`DELETE FROM super_admins`,
		`DELETE FROM user_roles`,
		`DELETE FROM local_credentials`,
		`DELETE FROM users`,
		`DELETE FROM tenants`,
	} {
		if _, err := h.admin.Exec(context.Background(), s); err != nil {
			t.Logf("reset %s: %v (continuing)", s, err)
		}
	}
}

func (h harness) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := h.admin.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

func (h harness) insertTenant(t *testing.T, id uuid.UUID, name, slug string, bootstrap bool) {
	t.Helper()
	if _, err := h.admin.Exec(context.Background(),
		`INSERT INTO tenants (id, name, slug, is_bootstrap_tenant) VALUES ($1, $2, $3, $4)`,
		id, name, slug, bootstrap,
	); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
}

// callback runs BeginLogin + the IdP round trip and returns the recorded
// response of GET /auth/oidc/callback?tenant_id=<urlTenant>.
func (h harness) callback(t *testing.T, urlTenant uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	return h.callbackVia(t, newFakeIdP(t), urlTenant)
}

// callbackVia is callback against a given IdP, so repeat logins keep one
// issuer and land on the same principal.
func (h harness) callbackVia(t *testing.T, idp *fakeIdP, urlTenant uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	authn := oidc.New(stubResolver{cfg: oidc.IdpConfig{
		ID:           uuid.New(),
		TenantID:     urlTenant,
		Name:         "primary",
		IssuerURL:    idp.srv.URL,
		ClientID:     fakeClientID,
		ClientSecret: "test-secret",
		RedirectURL:  "http://localhost:9999/auth/oidc/callback",
	}})
	login, err := authn.BeginLogin(context.Background(), oidc.LoginInput{TenantID: urlTenant, IdpName: "primary"}, false)
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	var state string
	for _, c := range login.Cookies {
		switch c.Name {
		case oidc.StateCookie:
			state = c.Value
		case oidc.NonceCookie:
			idp.nonce = c.Value
		}
	}

	handler := authapi.New(
		authn,
		users.NewStoreWithAuthPool(h.app, h.admin),
		sessions.NewStore(h.app, time.Hour),
		false,
		nil,
	)
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf(
		"/auth/oidc/callback?tenant_id=%s&code=test-code&state=%s",
		urlTenant, url.QueryEscape(state)), nil)
	for _, c := range login.Cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	handler.OIDCCallback(rec, req)
	return rec
}

func cookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// AC1: a fresh install whose bootstrap tenant was seeded and whose first
// sign-in is OIDC ends with exactly one tenant, the bootstrap tenant, and
// the user (with a session) in it.
func TestOIDCCallback_FirstLogin_AttachesToSeededBootstrapTenant(t *testing.T) {
	h := newHarness(t)
	seeded := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	h.insertTenant(t, seeded, "Seeded Org", "seeded-org", true)

	rec := h.callback(t, seeded)

	if rec.Code != http.StatusFound {
		body, _ := io.ReadAll(rec.Body)
		t.Fatalf("status = %d, want 302; body %s", rec.Code, body)
	}
	sess := cookieNamed(rec, sessions.CookieName)
	if sess == nil || sess.Value == "" {
		t.Fatal("no session cookie set")
	}
	if n := h.count(t, `SELECT COUNT(*) FROM tenants`); n != 1 {
		t.Errorf("tenants = %d, want exactly 1", n)
	}
	if n := h.count(t, `SELECT COUNT(*) FROM users WHERE tenant_id = $1 AND idp_subject = 'oidc-founder'`, seeded); n != 1 {
		t.Errorf("OIDC user not in the bootstrap tenant")
	}
	if n := h.count(t, `SELECT COUNT(*) FROM users`); n != 1 {
		t.Errorf("users = %d, want 1", n)
	}
	if n := h.count(t, `SELECT COUNT(*) FROM sessions WHERE tenant_id = $1`, seeded); n != 1 {
		t.Errorf("session not in the bootstrap tenant")
	}
	if n := h.count(t,
		`SELECT COUNT(*) FROM super_admins s JOIN users u ON u.id = s.user_id WHERE u.tenant_id = $1`, seeded); n != 1 {
		t.Errorf("bootstrap admin is not super-admin")
	}
}

// AC3: with two tenants and no user the first OIDC login is refused with a
// generic 409 that names no tenant, writes nothing, and clears the flow
// cookies.
func TestOIDCCallback_FirstLogin_TwoTenants_Generic409(t *testing.T) {
	h := newHarness(t)
	a, b := uuid.New(), uuid.New()
	h.insertTenant(t, a, "Alpha Org", "alpha-org", true)
	h.insertTenant(t, b, "Beta Org", "beta-org", false)

	rec := h.callback(t, a)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, authapi.ErrMsgBootstrapAmbiguousTenant) {
		t.Errorf("body %q lacks the generic message", body)
	}
	for _, leak := range []string{a.String(), b.String(), "alpha-org", "beta-org", "Alpha Org", "Beta Org"} {
		if strings.Contains(body, leak) {
			t.Errorf("409 body discloses tenant identifier %q: %s", leak, body)
		}
	}
	if c := cookieNamed(rec, oidc.StateCookie); c == nil || c.MaxAge >= 0 {
		t.Errorf("OIDC flow cookies not cleared on the 409 path")
	}
	if cookieNamed(rec, sessions.CookieName) != nil {
		t.Error("session cookie set on a refused login")
	}
	for _, tbl := range []string{"users", "sessions", "super_admins", "user_roles"} {
		if n := h.count(t, `SELECT COUNT(*) FROM `+tbl); n != 0 {
			t.Errorf("%s = %d, want 0 (nothing written)", tbl, n)
		}
	}
	if n := h.count(t, `SELECT COUNT(*) FROM tenants`); n != 2 {
		t.Errorf("tenants = %d, want 2", n)
	}
}

// #1912: a deactivated user who still passes the IdP gets no session.
// Admin revoke-membership sets status='disabled' and leaves the IdP account
// alone, so the IdP still authenticates them; SCIM sets active=false.
func TestOIDCCallback_DeactivatedUserRefused(t *testing.T) {
	for name, deactivate := range map[string]string{
		"status disabled": `UPDATE users SET status = 'disabled' WHERE tenant_id = $1`,
		"scim inactive":   `UPDATE users SET active = false WHERE tenant_id = $1`,
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			tenant := uuid.MustParse("00000000-0000-4000-8000-000000000001")
			h.insertTenant(t, tenant, "Seeded Org", "seeded-org", true)
			idp := newFakeIdP(t)
			if rec := h.callbackVia(t, idp, tenant); rec.Code != http.StatusFound {
				t.Fatalf("first login: status = %d, want 302", rec.Code)
			}
			// A returning active user signs in again (#1912 also fixes the
			// UpsertOIDC conflict target this second login used to 500 on).
			if rec := h.callbackVia(t, idp, tenant); rec.Code != http.StatusFound {
				t.Fatalf("returning login: status = %d, want 302; body %s", rec.Code, rec.Body.String())
			}
			if _, err := h.admin.Exec(context.Background(), deactivate, tenant); err != nil {
				t.Fatalf("deactivate: %v", err)
			}
			if _, err := h.admin.Exec(context.Background(), `DELETE FROM sessions`); err != nil {
				t.Fatalf("clear sessions: %v", err)
			}

			rec := h.callbackVia(t, idp, tenant)

			if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "invalid credentials") {
				t.Errorf("got %d %s, want 401 invalid credentials", rec.Code, rec.Body.String())
			}
			if c := cookieNamed(rec, sessions.CookieName); c != nil && c.Value != "" {
				t.Errorf("session cookie set for a deactivated user")
			}
			if n := h.count(t, `SELECT COUNT(*) FROM sessions`); n != 0 {
				t.Errorf("sessions = %d, want 0", n)
			}
		})
	}
}
