//go:build integration

// Integration tests for the slice 378 adminauthzbundle handler.
// Requires Postgres reachable via DATABASE_URL_APP + DATABASE_URL. The
// harness opens an atlas_app-backed pool and seeds tenants + users
// via the BYPASSRLS admin pool.
//
// Run with:
//
//	go test -tags=integration -race ./internal/api/adminauthzbundle/...
//
// Coverage maps to slice 378 acceptance criteria:
//
//	AC-4   POST /v1/admin/authz-bundle/reload mounted + super_admin-gated
//	AC-5   audit-log row written (super_admin_audit_log + me_audit_log)
//	AC-7   non-super_admin → 403; matrix-failure → 422; happy path → 200
//	AC-8   no test regressions (existing matrix integration still passes)

package adminauthzbundle_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/open-policy-agent/opa/v1/rego"

	"github.com/mgoodric/security-atlas/internal/api/adminauthzbundle"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
	"github.com/mgoodric/security-atlas/internal/authz"
)

var (
	appPool   *pgxpool.Pool
	adminPool *pgxpool.Pool
)

func TestMain(m *testing.M) {
	appURL := os.Getenv("DATABASE_URL_APP")
	adminURL := os.Getenv("DATABASE_URL")
	if appURL == "" || adminURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL_APP or DATABASE_URL not set; skipping adminauthzbundle integration tests")
		os.Exit(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, err := pgxpool.New(ctx, appURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pgxpool.New app: %v\n", err)
		os.Exit(1)
	}
	appPool = p
	a, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pgxpool.New admin: %v\n", err)
		os.Exit(1)
	}
	adminPool = a
	code := m.Run()
	p.Close()
	a.Close()
	os.Exit(code)
}

// --- harness ---

func seedTenant(t *testing.T, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := adminPool.Exec(context.Background(),
		`INSERT INTO tenants (id, name) VALUES ($1, $2)`, id, name); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminPool.Exec(context.Background(),
			`DELETE FROM me_audit_log WHERE tenant_id = $1`, id)
		_, _ = adminPool.Exec(context.Background(),
			`DELETE FROM super_admin_audit_log WHERE actor_tenant_id = $1`, id)
		_, _ = adminPool.Exec(context.Background(),
			`DELETE FROM tenants WHERE id = $1`, id)
	})
	return id
}

// cleanReloadAuditRows removes any prior 'authz_bundle_reload' rows
// for the given actor so each test starts from a clean count.
func cleanReloadAuditRows(t *testing.T, actorID uuid.UUID) {
	t.Helper()
	if _, err := adminPool.Exec(context.Background(),
		`DELETE FROM super_admin_audit_log WHERE actor_user_id = $1 AND action = 'authz_bundle_reload'`,
		actorID,
	); err != nil {
		t.Fatalf("clean super_admin_audit_log: %v", err)
	}
	if _, err := adminPool.Exec(context.Background(),
		`DELETE FROM me_audit_log WHERE user_id = $1 AND action = 'authz_bundle_reload'`,
		actorID,
	); err != nil {
		t.Fatalf("clean me_audit_log: %v", err)
	}
}

func newRouter(t *testing.T, tenantID, userID uuid.UUID, superAdmin bool, h *adminauthzbundle.Handler) http.Handler {
	t.Helper()
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			claims := &jwt.AtlasClaims{
				RegisteredClaims: jwt.RegisteredClaims{Subject: userID.String()},
				CurrentTenantID:  tenantID,
				AvailableTenants: []uuid.UUID{tenantID},
				SuperAdmin:       superAdmin,
			}
			ctx := jwtmw.WithClaimsForTest(req.Context(), claims)
			cred := credstore.Credential{
				ID:       "jwt:test",
				TenantID: tenantID.String(),
				UserID:   userID.String(),
				IsAdmin:  superAdmin,
			}
			ctx = authctx.WithCredential(ctx, cred)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Post("/v1/admin/authz-bundle/reload", h.Reload)
	return r
}

func doRequest(t *testing.T, h http.Handler, method, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		buf, _ := json.Marshal(body)
		reader = bytes.NewReader(buf)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	res := rr.Result()
	out := map[string]any{}
	if res.ContentLength != 0 && res.Header.Get("Content-Type") == "application/json" {
		_ = json.NewDecoder(res.Body).Decode(&out)
	}
	return res, out
}

// failingMatrixValidator always returns an error. Used by
// TestReload_MatrixFailure_Returns422 to drive the reload-rejection
// path without modifying any rego source.
func failingMatrixValidator(ctx context.Context, _ *rego.PreparedEvalQuery) error {
	return fmt.Errorf("synthetic matrix failure for test")
}

// --- tests ---

// AC-7 (happy path): super_admin reload returns 200 + both audit-log
// rows are written.
func TestReload_HappyPath_WritesBothAuditRows(t *testing.T) {
	tenantID := seedTenant(t, "Tenant for authz-bundle reload happy")
	actorID := uuid.New()
	cleanReloadAuditRows(t, actorID)

	engine, err := authz.NewEngine(context.Background(), authz.NoopRolesResolver{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	preSHA := engine.BundleSHA256()

	h := adminauthzbundle.New(appPool, engine).WithRateLimitWindow(0)
	router := newRouter(t, tenantID, actorID, true, h)

	res, body := doRequest(t, router, http.MethodPost, "/v1/admin/authz-bundle/reload", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%v", res.StatusCode, body)
	}
	if body["matrix_passed"] != true {
		t.Fatalf("expected matrix_passed=true, got %v", body["matrix_passed"])
	}
	if body["before_bundle_sha256"] != preSHA {
		t.Errorf("before_bundle_sha256 mismatch: got %v want %s", body["before_bundle_sha256"], preSHA)
	}
	if body["after_bundle_sha256"] != preSHA {
		// Same embedded bundle = same SHA. The handler still reports
		// it so the audit log records both values explicitly.
		t.Errorf("after_bundle_sha256 mismatch on identical-bundle reload: got %v want %s", body["after_bundle_sha256"], preSHA)
	}

	// super_admin_audit_log row written.
	var saCount int
	_ = adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM super_admin_audit_log WHERE actor_user_id = $1 AND action = 'authz_bundle_reload'`,
		actorID,
	).Scan(&saCount)
	if saCount != 1 {
		t.Errorf("expected 1 super_admin_audit_log row, got %d", saCount)
	}

	// me_audit_log row written.
	var meCount int
	_ = adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM me_audit_log WHERE tenant_id = $1 AND action = 'authz_bundle_reload'`,
		tenantID,
	).Scan(&meCount)
	if meCount != 1 {
		t.Errorf("expected 1 me_audit_log row, got %d", meCount)
	}

	// payload_json carries both before + after SHAs.
	var payloadJSON []byte
	if err := adminPool.QueryRow(context.Background(),
		`SELECT payload_json FROM super_admin_audit_log WHERE actor_user_id = $1 AND action = 'authz_bundle_reload' LIMIT 1`,
		actorID,
	).Scan(&payloadJSON); err != nil {
		t.Fatalf("read payload_json: %v", err)
	}
	var payload map[string]any
	_ = json.Unmarshal(payloadJSON, &payload)
	if payload["before_bundle_sha256"] != preSHA {
		t.Errorf("payload before_bundle_sha256 mismatch: got %v want %s", payload["before_bundle_sha256"], preSHA)
	}
	if payload["after_bundle_sha256"] == "" || payload["after_bundle_sha256"] == nil {
		t.Errorf("payload after_bundle_sha256 missing: got %v", payload["after_bundle_sha256"])
	}
}

// AC-7 (non-super_admin): caller WITHOUT super_admin gets 403.
func TestReload_NonSuperAdmin_Returns403(t *testing.T) {
	tenantID := seedTenant(t, "Tenant for authz-bundle reload 403")
	actorID := uuid.New()

	engine, err := authz.NewEngine(context.Background(), authz.NoopRolesResolver{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	h := adminauthzbundle.New(appPool, engine)
	router := newRouter(t, tenantID, actorID, false /* not super_admin */, h)

	res, body := doRequest(t, router, http.MethodPost, "/v1/admin/authz-bundle/reload", nil)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%v", res.StatusCode, body)
	}

	// No audit-log row should be written when the gate denies.
	var saCount int
	_ = adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM super_admin_audit_log WHERE actor_user_id = $1`,
		actorID,
	).Scan(&saCount)
	if saCount != 0 {
		t.Errorf("expected 0 super_admin_audit_log rows on 403, got %d", saCount)
	}
}

// AC-3 (matrix-failure rejection): when the matrix validator returns
// an error, the reload endpoint returns 422 and the engine continues
// to serve the prior bundle.
func TestReload_MatrixFailure_Returns422(t *testing.T) {
	tenantID := seedTenant(t, "Tenant for authz-bundle matrix-fail")
	actorID := uuid.New()
	cleanReloadAuditRows(t, actorID)

	engine, err := authz.NewEngine(context.Background(), authz.NoopRolesResolver{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	preSHA := engine.BundleSHA256()

	h := adminauthzbundle.New(appPool, engine).WithRateLimitWindow(0).WithValidator(failingMatrixValidator)
	router := newRouter(t, tenantID, actorID, true, h)

	res, body := doRequest(t, router, http.MethodPost, "/v1/admin/authz-bundle/reload", nil)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 on matrix-failure, got %d body=%v", res.StatusCode, body)
	}

	// Engine continues to serve the prior bundle.
	if engine.BundleSHA256() != preSHA {
		t.Errorf("bundle SHA changed despite matrix-failure: pre=%s post=%s", preSHA, engine.BundleSHA256())
	}

	// No audit-log row should be written on rejection.
	var saCount int
	_ = adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM super_admin_audit_log WHERE actor_user_id = $1 AND action = 'authz_bundle_reload'`,
		actorID,
	).Scan(&saCount)
	if saCount != 0 {
		t.Errorf("expected 0 super_admin_audit_log rows on 422, got %d", saCount)
	}
}

// AC-5 (rate limit): second reload from same actor inside the window
// returns 429.
func TestReload_RateLimit_Returns429(t *testing.T) {
	tenantID := seedTenant(t, "Tenant for authz-bundle rate-limit")
	actorID := uuid.New()
	cleanReloadAuditRows(t, actorID)

	engine, err := authz.NewEngine(context.Background(), authz.NoopRolesResolver{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	// Generous-but-finite window: long enough that the second call
	// inside the test definitely lands inside it.
	h := adminauthzbundle.New(appPool, engine).WithRateLimitWindow(60 * time.Second)
	router := newRouter(t, tenantID, actorID, true, h)

	// First call: 200.
	res1, _ := doRequest(t, router, http.MethodPost, "/v1/admin/authz-bundle/reload", nil)
	if res1.StatusCode != http.StatusOK {
		t.Fatalf("first call expected 200, got %d", res1.StatusCode)
	}
	// Second call: 429.
	res2, body2 := doRequest(t, router, http.MethodPost, "/v1/admin/authz-bundle/reload", nil)
	if res2.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second call expected 429, got %d body=%v", res2.StatusCode, body2)
	}
	if res2.Header.Get("Retry-After") == "" {
		t.Errorf("expected Retry-After header on 429 response")
	}

	// Audit-log: exactly ONE row from the first successful call; the
	// 429'd call must NOT write an audit row.
	var saCount int
	_ = adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM super_admin_audit_log WHERE actor_user_id = $1 AND action = 'authz_bundle_reload'`,
		actorID,
	).Scan(&saCount)
	if saCount != 1 {
		t.Errorf("expected exactly 1 audit row (the 200 call), got %d", saCount)
	}
}
