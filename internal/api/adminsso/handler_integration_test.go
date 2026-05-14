//go:build integration

// Integration tests for the slice 062 admin SSO HTTP API. Requires
// Postgres reachable via DATABASE_URL_APP.
package adminsso_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/adminsso"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
)

var appPool *pgxpool.Pool

func TestMain(m *testing.M) {
	url := os.Getenv("DATABASE_URL_APP")
	if url == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL_APP not set; skipping adminsso integration tests")
		os.Exit(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, err := pgxpool.New(ctx, url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pgxpool.New: %v\n", err)
		os.Exit(1)
	}
	appPool = p
	code := m.Run()
	p.Close()
	os.Exit(code)
}

// newRouter wires the handler with an admin-or-not credential mux that
// mimics the production middleware stack: bearer auth -> tenancymw -> handler.
func newRouter(t *testing.T, tenantID uuid.UUID, isAdmin bool, opts *adminsso.PreflightOptions) http.Handler {
	t.Helper()
	h := adminsso.New(appPool)
	if opts != nil {
		h = h.WithPreflightOptions(*opts)
	}

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID:       "key_test_sso",
				TenantID: tenantID.String(),
				IsAdmin:  isAdmin,
				UserID:   "user-sso-test",
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Get("/v1/admin/sso", h.Get)
	r.Patch("/v1/admin/sso", h.Patch)
	r.Post("/v1/admin/sso/preflight", h.Preflight)
	return r
}

func cleanupSSO(t *testing.T, tenantID uuid.UUID) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = appPool.Exec(context.Background(),
			"DELETE FROM oidc_idp_configs WHERE tenant_id = $1", tenantID)
	})
}

// AC-1 (1/2): PATCH /v1/admin/sso creates a config; GET returns it
// WITHOUT client_secret.
func TestPatchThenGetSSOOmitsClientSecret(t *testing.T) {
	tenant := uuid.New()
	cleanupSSO(t, tenant)
	r := newRouter(t, tenant, true, nil)

	body, _ := json.Marshal(map[string]any{
		"issuer_url":            "https://accounts.example.com",
		"client_id":             "client-abc",
		"client_secret":         "shh-this-is-secret",
		"redirect_url":          "https://atlas.example.com/auth/oidc/callback",
		"allowed_email_domains": []string{"example.com"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/v1/admin/sso", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d; body = %s", rec.Code, rec.Body.String())
	}

	// GET — verify no client_secret field appears in JSON.
	getRec := httptest.NewRecorder()
	r.ServeHTTP(getRec, httptest.NewRequest(http.MethodGet, "/v1/admin/sso", nil))
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d; body = %s", getRec.Code, getRec.Body.String())
	}
	respBody := getRec.Body.String()
	if strings.Contains(respBody, "client_secret") {
		t.Errorf("GET response leaked client_secret field: %s", respBody)
	}
	if strings.Contains(respBody, "shh-this-is-secret") {
		t.Errorf("GET response leaked secret value verbatim: %s", respBody)
	}

	// Verify shape.
	var resp adminsso.GetResponse
	if err := json.NewDecoder(strings.NewReader(respBody)).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.IssuerURL != "https://accounts.example.com" {
		t.Errorf("issuer_url = %q; want https://accounts.example.com", resp.IssuerURL)
	}
	if resp.ClientID != "client-abc" {
		t.Errorf("client_id = %q; want client-abc", resp.ClientID)
	}
	if len(resp.AllowedEmailDomains) != 1 || resp.AllowedEmailDomains[0] != "example.com" {
		t.Errorf("allowed_email_domains = %v; want [example.com]", resp.AllowedEmailDomains)
	}
}

// AC-1 (2/2): PATCH a second time with empty client_secret leaves the
// existing secret intact. (Verified indirectly: GET still 200, the secret
// in DB still has length > 0.)
func TestPatchEmptySecretPreservesExisting(t *testing.T) {
	tenant := uuid.New()
	cleanupSSO(t, tenant)
	r := newRouter(t, tenant, true, nil)

	// Initial save with secret.
	body1, _ := json.Marshal(map[string]any{
		"issuer_url":    "https://idp.example.com",
		"client_id":     "client-x",
		"client_secret": "first-secret-value",
		"redirect_url":  "https://atlas.example.com/cb",
	})
	rec1 := httptest.NewRecorder()
	r.ServeHTTP(rec1, httptest.NewRequest(http.MethodPatch, "/v1/admin/sso", bytes.NewReader(body1)))
	if rec1.Code != http.StatusOK {
		t.Fatalf("first PATCH status = %d; body = %s", rec1.Code, rec1.Body.String())
	}

	// Re-PATCH with empty secret should succeed (leave-existing).
	body2, _ := json.Marshal(map[string]any{
		"issuer_url":    "https://idp.example.com",
		"client_id":     "client-x",
		"client_secret": "",
		"redirect_url":  "https://atlas.example.com/cb",
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/v1/admin/sso", bytes.NewReader(body2)))
	if rec.Code != http.StatusOK {
		t.Fatalf("second PATCH status = %d; body = %s", rec.Code, rec.Body.String())
	}

	// Verify DB-stored secret bytes are unchanged. set_config must run in
	// the same transaction as the SELECT so the GUC is in scope; we use
	// pool.Begin to get a single connection.
	ctx := context.Background()
	tx, err := appPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenant.String()); err != nil {
		t.Fatalf("set_config: %v", err)
	}
	var nbytes int
	if err := tx.QueryRow(ctx,
		`SELECT octet_length(client_secret_enc) FROM oidc_idp_configs WHERE tenant_id = $1`,
		tenant).Scan(&nbytes); err != nil {
		t.Fatalf("scan secret len: %v", err)
	}
	if nbytes != len("first-secret-value") {
		t.Errorf("client_secret_enc length = %d; want %d (secret should be unchanged after empty PATCH)",
			nbytes, len("first-secret-value"))
	}
}

// Anti-criterion P0: non-admin gets 403.
func TestGetSSORejectsNonAdmin(t *testing.T) {
	tenant := uuid.New()
	r := newRouter(t, tenant, false, nil)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/admin/sso", nil))
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d; want 403", rec.Code)
	}
}

func TestPatchSSORejectsNonAdmin(t *testing.T) {
	tenant := uuid.New()
	r := newRouter(t, tenant, false, nil)

	body, _ := json.Marshal(map[string]any{"issuer_url": "https://x", "client_id": "y"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/v1/admin/sso", bytes.NewReader(body)))
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d; want 403", rec.Code)
	}
}

// GET returns 404 when no SSO config exists.
func TestGetSSOReturns404WhenAbsent(t *testing.T) {
	tenant := uuid.New()
	cleanupSSO(t, tenant)
	r := newRouter(t, tenant, true, nil)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/admin/sso", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404", rec.Code)
	}
}

// AC-2: POST /v1/admin/sso/preflight fetches discovery doc and returns
// parsed endpoints. Drives a local httptest server as the fake IdP; the
// handler is configured with AllowPrivateIPs so 127.0.0.1 isn't blocked
// by the SSRF guard.
func TestPreflightReturnsParsedEndpoints(t *testing.T) {
	// Spin up a fake IdP exposing /.well-known/openid-configuration.
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"issuer": "https://idp.example.com",
			"authorization_endpoint": "https://idp.example.com/authorize",
			"token_endpoint": "https://idp.example.com/token",
			"jwks_uri": "https://idp.example.com/jwks.json"
		}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	tenant := uuid.New()
	r := newRouter(t, tenant, true, &adminsso.PreflightOptions{
		AllowPrivateIPs: true,
		Timeout:         3 * time.Second,
		MaxBodyBytes:    64 * 1024,
	})

	body, _ := json.Marshal(map[string]any{"issuer_url": srv.URL})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/admin/sso/preflight", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var resp adminsso.PreflightResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.AuthorizationEndpoint != "https://idp.example.com/authorize" {
		t.Errorf("authorization_endpoint = %q", resp.AuthorizationEndpoint)
	}
	if resp.TokenEndpoint != "https://idp.example.com/token" {
		t.Errorf("token_endpoint = %q", resp.TokenEndpoint)
	}
	if resp.JWKsURI != "https://idp.example.com/jwks.json" {
		t.Errorf("jwks_uri = %q", resp.JWKsURI)
	}
}

// Preflight rejects an issuer_url that's http (not https) in production
// (no AllowPrivateIPs).
func TestPreflightRejectsHTTP(t *testing.T) {
	tenant := uuid.New()
	r := newRouter(t, tenant, true, nil) // default opts: AllowPrivateIPs=false

	body, _ := json.Marshal(map[string]any{"issuer_url": "http://example.com"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/admin/sso/preflight", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "https") {
		t.Errorf("error message should mention https: %s", rec.Body.String())
	}
}

// SSRF guard: a hostname that resolves to a private IP is rejected.
func TestPreflightRejectsPrivateIP(t *testing.T) {
	tenant := uuid.New()
	r := newRouter(t, tenant, true, &adminsso.PreflightOptions{
		AllowPrivateIPs: false,
		Timeout:         3 * time.Second,
		MaxBodyBytes:    64 * 1024,
		LookupHost: func(_ context.Context, _ string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("10.0.0.5")}, nil
		},
	})

	body, _ := json.Marshal(map[string]any{"issuer_url": "https://internal.example.com"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/admin/sso/preflight", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400 (SSRF rejection); body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "non-routable") {
		t.Errorf("expected non-routable error; got: %s", rec.Body.String())
	}
}
