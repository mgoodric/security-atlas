//go:build integration

// Integration tests for the slice 278 admindemo handler.
//
// Requires Postgres reachable via DATABASE_URL (BYPASSRLS pool) and
// DATABASE_URL_APP (RLS-bound pool). The handler uses the BYPASSRLS
// auth pool for the seeder + audit-log writes; the test harness
// uses the same.
//
// Run with:
//
//	go test -tags=integration -p 1 ./internal/api/admindemo/...
//
// Coverage maps to slice 278 acceptance criteria + ISC criteria:
//
//	AC-4  / ISC-3    env-var unset -> 503 + no seed + no audit row
//	AC-5  / ISC-4    non-admin     -> 403 + no seed + no audit row
//	AC-6  / ISC-7    admin + env   -> 200 + seed runs + audit row written
//	AC-8  / ISC-5    rate limit    -> second call within 60s -> 429
//	AC-9  / ISC-11   status        -> {enabled:true|false}
//
// Test fixtures use only neutral `demo-test-*` slugs (P0-278-2 spirit).

package admindemo_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/admindemo"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/demoseed"
)

var adminPool *pgxpool.Pool

func TestMain(m *testing.M) {
	adminURL := os.Getenv("DATABASE_URL")
	if adminURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL not set; skipping admindemo integration tests")
		os.Exit(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pgxpool.New admin: %v\n", err)
		os.Exit(1)
	}
	adminPool = p
	code := m.Run()
	p.Close()
	os.Exit(code)
}

// --- harness ---

// seedTenant inserts a fresh tenants row + registers cleanup.
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
			`DELETE FROM tenants WHERE id = $1`, id)
	})
	return id
}

// seedActorUser inserts a users row for the actor in their session tenant.
func seedActorUser(t *testing.T, tenantID, userID uuid.UUID) {
	t.Helper()
	if _, err := adminPool.Exec(context.Background(),
		`INSERT INTO users (id, tenant_id, email, display_name, status, idp_issuer, idp_subject)
		 VALUES ($1, $2, $3, $4, 'active', 'https://example.invalid/issuer', $5)`,
		userID, tenantID,
		fmt.Sprintf("test-admin-%s@example.invalid", userID.String()[:8]),
		"Test Admin",
		"test-subject-"+userID.String(),
	); err != nil {
		t.Fatalf("seed actor user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminPool.Exec(context.Background(),
			`DELETE FROM users WHERE id = $1`, userID)
	})
}

// resetDemoAuditRows clears any pre-existing demo-seed / demo-teardown
// rows so per-test counts start at zero.
func resetDemoAuditRows(t *testing.T) {
	t.Helper()
	for _, action := range []string{"demo_seed", "demo_teardown", "demo_seed_apply", "demo_seed_teardown"} {
		_, _ = adminPool.Exec(context.Background(),
			`DELETE FROM super_admin_audit_log WHERE action = $1`, action)
	}
}

// cleanupDemoTenant deletes the demo tenant slug + all anchored rows.
func cleanupDemoTenant(t *testing.T, slug string) {
	t.Helper()
	t.Cleanup(func() {
		// Look up tenant id by slug.
		var id uuid.UUID
		err := adminPool.QueryRow(context.Background(),
			`SELECT id FROM tenants WHERE slug = $1`, slug,
		).Scan(&id)
		if err != nil {
			return // didn't exist
		}
		seeder, ierr := demoseed.NewSeeder(adminPool, demoseed.DefaultScale)
		if ierr != nil {
			return
		}
		_ = seeder.Teardown(context.Background(), slug, uuid.Nil, uuid.Nil)
		_, _ = adminPool.Exec(context.Background(),
			`DELETE FROM tenants WHERE id = $1`, id)
	})
}

// newRouter wires the handler under chi with the standard auth + tenancy
// middleware. `admin` controls whether the credential carries the admin
// role; `enabled` controls the env-var gate.
func newRouter(t *testing.T, tenantID, userID uuid.UUID, isAdmin, enabled bool) http.Handler {
	t.Helper()
	h := admindemo.New(adminPool, func() bool { return enabled })
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			cred := credstore.Credential{
				ID:       "jwt:test",
				TenantID: tenantID.String(),
				UserID:   userID.String(),
				IsAdmin:  isAdmin,
			}
			ctx := authctx.WithCredential(req.Context(), cred)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Get("/v1/admin/demo/status", h.Status)
	r.Post("/v1/admin/demo/seed", h.Seed)
	r.Post("/v1/admin/demo/teardown", h.Teardown)
	return r
}

func doRequest(t *testing.T, h http.Handler, method, path string) (*http.Response, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(nil))
	req.RemoteAddr = "192.0.2.100:12345"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	res := rr.Result()
	out := map[string]any{}
	ct := res.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/json") {
		_ = json.NewDecoder(res.Body).Decode(&out)
	}
	return res, out
}

// countMeAuditRows returns the number of me_audit_log rows with the
// given action for the given tenant.
func countMeAuditRows(t *testing.T, tenantID uuid.UUID, action string) int {
	t.Helper()
	var n int
	if err := adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM me_audit_log WHERE tenant_id = $1 AND action = $2`,
		tenantID, action,
	).Scan(&n); err != nil {
		t.Fatalf("count me_audit_log: %v", err)
	}
	return n
}

// countDemoTenants returns whether the demo tenant slug exists.
func demoTenantExists(t *testing.T, slug string) bool {
	t.Helper()
	var n int
	if err := adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM tenants WHERE slug = $1`, slug,
	).Scan(&n); err != nil {
		t.Fatalf("count tenants: %v", err)
	}
	return n > 0
}

// --- tests ---

// AC-9 / ISC-11: status returns enabled=true when env gate set.
func TestStatus_EnabledTrue(t *testing.T) {
	sessionTenant := seedTenant(t, "slice278 status enabled")
	actor := uuid.New()
	seedActorUser(t, sessionTenant, actor)

	h := newRouter(t, sessionTenant, actor, true, true)
	res, body := doRequest(t, h, http.MethodGet, "/v1/admin/demo/status")
	if res.StatusCode != 200 {
		t.Fatalf("status code = %d; want 200; body=%v", res.StatusCode, body)
	}
	if body["enabled"] != true {
		t.Fatalf("enabled = %v; want true", body["enabled"])
	}
}

// AC-9 / ISC-11: status returns enabled=false when env gate unset.
func TestStatus_EnabledFalse(t *testing.T) {
	sessionTenant := seedTenant(t, "slice278 status disabled")
	actor := uuid.New()
	seedActorUser(t, sessionTenant, actor)

	h := newRouter(t, sessionTenant, actor, true, false)
	res, body := doRequest(t, h, http.MethodGet, "/v1/admin/demo/status")
	if res.StatusCode != 200 {
		t.Fatalf("status code = %d; want 200", res.StatusCode)
	}
	if body["enabled"] != false {
		t.Fatalf("enabled = %v; want false", body["enabled"])
	}
}

// AC-4 / ISC-3: env-var unset -> 503 + no seed + no audit row.
func TestSeed_EnvUnsetGets503AndDoesNotSeed(t *testing.T) {
	resetDemoAuditRows(t)
	sessionTenant := seedTenant(t, "slice278 env-unset")
	actor := uuid.New()
	seedActorUser(t, sessionTenant, actor)
	cleanupDemoTenant(t, "demo")

	h := newRouter(t, sessionTenant, actor, true, false)
	res, body := doRequest(t, h, http.MethodPost, "/v1/admin/demo/seed")
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status code = %d; want 503; body=%v", res.StatusCode, body)
	}

	// No me_audit_log row should exist for the actor's tenant.
	if n := countMeAuditRows(t, sessionTenant, "demo_seed"); n != 0 {
		t.Fatalf("me_audit_log demo_seed count = %d; want 0", n)
	}
	// No demo tenant should exist.
	if demoTenantExists(t, "demo") {
		t.Fatalf("demo tenant exists after 503; should not have been created")
	}
}

// AC-5 / ISC-4: non-admin -> 403 + no seed + no audit row.
func TestSeed_NonAdminGets403(t *testing.T) {
	resetDemoAuditRows(t)
	sessionTenant := seedTenant(t, "slice278 non-admin")
	actor := uuid.New()
	seedActorUser(t, sessionTenant, actor)
	cleanupDemoTenant(t, "demo")

	h := newRouter(t, sessionTenant, actor, false, true)
	res, body := doRequest(t, h, http.MethodPost, "/v1/admin/demo/seed")
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("status code = %d; want 403; body=%v", res.StatusCode, body)
	}
	if n := countMeAuditRows(t, sessionTenant, "demo_seed"); n != 0 {
		t.Fatalf("non-admin produced audit row; count = %d", n)
	}
	if demoTenantExists(t, "demo") {
		t.Fatalf("demo tenant exists after 403")
	}
}

// AC-6 / ISC-7: admin + env set -> 200 + seed runs + audit row written.
//
// This test exercises the real demoseed.Seeder under the BYPASSRLS
// pool. The test is slow (5-10s for the seed) but verifies the
// end-to-end happy path.
func TestSeed_HappyPath(t *testing.T) {
	resetDemoAuditRows(t)
	sessionTenant := seedTenant(t, "slice278 happy")
	actor := uuid.New()
	seedActorUser(t, sessionTenant, actor)
	cleanupDemoTenant(t, "demo")

	h := newRouter(t, sessionTenant, actor, true, true)
	res, body := doRequest(t, h, http.MethodPost, "/v1/admin/demo/seed")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status code = %d; want 200; body=%v", res.StatusCode, body)
	}
	// Summary contains non-zero counts.
	if controls, ok := body["controls"].(float64); !ok || controls < 1 {
		t.Errorf("controls = %v; want > 0", body["controls"])
	}
	if risks, ok := body["risks"].(float64); !ok || risks < 1 {
		t.Errorf("risks = %v; want > 0", body["risks"])
	}
	// me_audit_log row for the HTTP invocation event.
	if n := countMeAuditRows(t, sessionTenant, "demo_seed"); n != 1 {
		t.Errorf("me_audit_log demo_seed count = %d; want 1", n)
	}
	// Demo tenant created.
	if !demoTenantExists(t, "demo") {
		t.Errorf("demo tenant should exist after happy-path seed")
	}
}

// AC-8 / ISC-5: rate limit -> second invocation within 60s -> 429.
func TestSeed_RateLimited(t *testing.T) {
	resetDemoAuditRows(t)
	sessionTenant := seedTenant(t, "slice278 rate-limit")
	actor := uuid.New()
	seedActorUser(t, sessionTenant, actor)

	// Build a handler with a frozen clock so the second call falls
	// inside the rate-limit window. Use enabled=true; the first call
	// will try to seed which will fail because the demo tenant slug
	// "demo" may already exist (cleanup is in a different test). We
	// don't care about the first response — only that the limiter
	// consumed the token.
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	h := admindemo.New(adminPool, func() bool { return true }).WithClock(func() time.Time { return now })
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			cred := credstore.Credential{
				ID:       "jwt:test",
				TenantID: sessionTenant.String(),
				UserID:   actor.String(),
				IsAdmin:  true,
			}
			ctx := authctx.WithCredential(req.Context(), cred)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Post("/v1/admin/demo/seed", h.Seed)

	// First call — consumes the token regardless of downstream outcome.
	req1 := httptest.NewRequest(http.MethodPost, "/v1/admin/demo/seed", nil)
	req1.RemoteAddr = "192.0.2.101:1"
	rr1 := httptest.NewRecorder()
	r.ServeHTTP(rr1, req1)
	if rr1.Code == http.StatusTooManyRequests {
		t.Fatalf("first call was rate-limited; should not be")
	}

	// Second call from same IP within the window — 429.
	req2 := httptest.NewRequest(http.MethodPost, "/v1/admin/demo/seed", nil)
	req2.RemoteAddr = "192.0.2.101:2"
	rr2 := httptest.NewRecorder()
	r.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusTooManyRequests {
		t.Fatalf("second call status = %d; want 429", rr2.Code)
	}
	if rr2.Header().Get("Retry-After") != "60" {
		t.Fatalf("Retry-After = %q; want 60", rr2.Header().Get("Retry-After"))
	}
}
