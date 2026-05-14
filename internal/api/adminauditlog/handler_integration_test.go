//go:build integration

// Integration tests for the slice 062 admin audit-log HTTP API. Requires
// Postgres reachable via DATABASE_URL_APP.
package adminauditlog_test

import (
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

	"github.com/mgoodric/security-atlas/internal/api/adminauditlog"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
)

var appPool *pgxpool.Pool

func TestMain(m *testing.M) {
	url := os.Getenv("DATABASE_URL_APP")
	if url == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL_APP not set; skipping adminauditlog integration tests")
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

func newRouter(t *testing.T, tenantID uuid.UUID, isAdmin bool) http.Handler {
	t.Helper()
	h := adminauditlog.New(appPool)

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID:       "key_test_auditlog",
				TenantID: tenantID.String(),
				IsAdmin:  isAdmin,
				UserID:   "user-auditlog-test",
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Get("/v1/admin/audit-log", h.List)
	return r
}

// seedAuditRow inserts into one of the source audit-log tables under the
// tenant's GUC. Returns the row's ts so the test can verify ordering.
func seedAuditRow(t *testing.T, tenantID uuid.UUID, table string) time.Time {
	t.Helper()
	ctx := context.Background()
	tx, err := appPool.Begin(ctx)
	if err != nil {
		t.Fatalf("seed begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenantID.String()); err != nil {
		t.Fatalf("seed set_config: %v", err)
	}
	var ts time.Time
	var qry string
	switch table {
	case "decision_audit_log":
		qry = `INSERT INTO decision_audit_log
			   (decision_id, tenant_id, user_id, action, resource_type, resource_id, result)
			   VALUES (gen_random_uuid(), $1, 'seeder', 'list', 'evidence', 'r-1', 'allow')
			   RETURNING occurred_at`
	case "evidence_audit_log":
		qry = `INSERT INTO evidence_audit_log
			   (id, tenant_id, credential_id, decision)
			   VALUES (gen_random_uuid(), $1, 'key_seed', 'accepted')
			   RETURNING received_at`
	case "exception_audit_log":
		qry = `INSERT INTO exception_audit_log
			   (id, tenant_id, exception_id, action, actor, to_state)
			   VALUES (gen_random_uuid(), $1, gen_random_uuid(), 'requested', 'seeder', 'requested')
			   RETURNING occurred_at`
	case "feature_flag_audit_log":
		qry = `INSERT INTO feature_flag_audit_log
			   (id, tenant_id, flag_key, from_enabled, to_enabled, actor)
			   VALUES (gen_random_uuid(), $1, 'risk.enabled', true, false, 'seeder')
			   RETURNING occurred_at`
	case "sample_audit_log":
		qry = `INSERT INTO sample_audit_log
			   (id, tenant_id, action, actor)
			   VALUES (gen_random_uuid(), $1, 'sample_drawn', 'seeder')
			   RETURNING occurred_at`
	case "audit_period_audit_log":
		qry = `INSERT INTO audit_period_audit_log
			   (id, tenant_id, audit_period_id, action, actor)
			   VALUES (gen_random_uuid(), $1, gen_random_uuid(), 'period_created', 'seeder')
			   RETURNING occurred_at`
	default:
		t.Fatalf("seedAuditRow: unsupported table %q", table)
	}
	if err := tx.QueryRow(ctx, qry, tenantID).Scan(&ts); err != nil {
		t.Fatalf("seed %s: %v", table, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("seed commit: %v", err)
	}
	return ts
}

func cleanupAuditTables(t *testing.T, tenantID uuid.UUID) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, tbl := range []string{
			"decision_audit_log", "evidence_audit_log",
			"exception_audit_log", "feature_flag_audit_log",
			"sample_audit_log", "audit_period_audit_log",
			"artifact_access_log",
		} {
			_, _ = appPool.Exec(ctx,
				fmt.Sprintf("DELETE FROM %s WHERE tenant_id = $1", tbl), tenantID)
		}
	})
}

// AC-6: GET /v1/admin/audit-log returns rows from across the source tables.
func TestAuditLogReturnsRowsFromMultipleSources(t *testing.T) {
	tenant := uuid.New()
	cleanupAuditTables(t, tenant)

	tables := []string{
		"decision_audit_log",
		"evidence_audit_log",
		"exception_audit_log",
		"feature_flag_audit_log",
		"sample_audit_log",
		"audit_period_audit_log",
	}
	for _, tbl := range tables {
		seedAuditRow(t, tenant, tbl)
	}

	r := newRouter(t, tenant, true)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/admin/audit-log?limit=50", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var resp adminauditlog.ListResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	seen := make(map[string]bool)
	for _, row := range resp.Rows {
		seen[row.SourceTable] = true
	}
	for _, tbl := range tables {
		if !seen[tbl] {
			t.Errorf("source_table %q absent from audit-log response", tbl)
		}
	}
	if len(resp.Rows) < len(tables) {
		t.Errorf("rows = %d; want >= %d", len(resp.Rows), len(tables))
	}
}

// AC-6: rows are ordered ts DESC.
func TestAuditLogOrderedByTSDesc(t *testing.T) {
	tenant := uuid.New()
	cleanupAuditTables(t, tenant)

	// Seed two rows; the second one happens later.
	t1 := seedAuditRow(t, tenant, "decision_audit_log")
	time.Sleep(20 * time.Millisecond)
	t2 := seedAuditRow(t, tenant, "feature_flag_audit_log")
	if !t2.After(t1) {
		t.Skipf("clock didn't advance between inserts: %v vs %v", t1, t2)
	}

	r := newRouter(t, tenant, true)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/admin/audit-log?limit=10", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp adminauditlog.ListResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Rows) < 2 {
		t.Fatalf("rows = %d; want >= 2", len(resp.Rows))
	}
	for i := 1; i < len(resp.Rows); i++ {
		if resp.Rows[i].TS.After(resp.Rows[i-1].TS) {
			t.Errorf("ts order violated at %d: %v > %v", i, resp.Rows[i].TS, resp.Rows[i-1].TS)
		}
	}
}

// AC-6 / P0: tenant isolation — tenant A admin sees tenant A rows only.
func TestAuditLogTenantIsolation(t *testing.T) {
	tenantA := uuid.New()
	tenantB := uuid.New()
	cleanupAuditTables(t, tenantA)
	cleanupAuditTables(t, tenantB)

	seedAuditRow(t, tenantA, "feature_flag_audit_log")
	seedAuditRow(t, tenantB, "feature_flag_audit_log")

	rA := newRouter(t, tenantA, true)
	rec := httptest.NewRecorder()
	rA.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/admin/audit-log?limit=50", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var resp adminauditlog.ListResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	// We can't easily distinguish tenant A's row from tenant B's just by
	// source_table, but the COUNT should equal exactly 1 (tenant A's
	// seeded row). Tenant B's row must be filtered by RLS.
	count := 0
	for _, row := range resp.Rows {
		if row.SourceTable == "feature_flag_audit_log" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("tenant A saw %d feature_flag_audit_log rows; want 1 (RLS bypass detected)", count)
	}
}

// P0: filter by event_type.
func TestAuditLogFilterByEventType(t *testing.T) {
	tenant := uuid.New()
	cleanupAuditTables(t, tenant)
	seedAuditRow(t, tenant, "feature_flag_audit_log")
	seedAuditRow(t, tenant, "decision_audit_log")

	r := newRouter(t, tenant, true)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/admin/audit-log?event_type=feature_flag.flip", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp adminauditlog.ListResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	for _, row := range resp.Rows {
		if row.EventType != "feature_flag.flip" {
			t.Errorf("row leaked event_type %q under event_type=feature_flag.flip filter", row.EventType)
		}
	}
}

// Non-admin gets 403.
func TestAuditLogRejectsNonAdmin(t *testing.T) {
	tenant := uuid.New()
	r := newRouter(t, tenant, false)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/admin/audit-log", nil))
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d; want 403", rec.Code)
	}
}
