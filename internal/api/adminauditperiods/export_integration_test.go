//go:build integration

// Slice 139 — integration tests for the audit-periods export endpoint.
//
// AC coverage:
//
//   AC-1   : endpoint exists + returns 200 for csv|json|xlsx
//   AC-3/4 : (BFF + Export button — covered by web/ vitest + Playwright)
//   AC-5/6 : meta-audit row written on every outcome — TestAuditPeriodsExport_MetaAuditFires
//   AC-9   : freeze metadata columns surfaced — TestAuditPeriodsExport_FreezeMetadataIncluded
//   AC-10  : cross-tenant isolation × 3 formats — TestAuditPeriodsExport_CrossTenantIsolationAllThreeFormats
//
// Requires DATABASE_URL_APP + DATABASE_URL.

package adminauditperiods_test

import (
	"archive/zip"
	"bytes"
	"context"
	stdcsv "encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
)

// ===== Harness =====

func appDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL_APP")
	if v == "" {
		t.Skip("DATABASE_URL_APP not set; skipping integration test")
	}
	return v
}

func adminDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL")
	if v == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	return v
}

func openPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	return pool
}

func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM audit_period_audit_log WHERE tenant_id = $1`,
			`DELETE FROM audit_periods WHERE tenant_id = $1`,
			`DELETE FROM framework_versions WHERE tenant_id = $1`,
			`DELETE FROM frameworks WHERE tenant_id = $1`,
			`DELETE FROM me_audit_log WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

func setupHTTPServer(t *testing.T, tenant string) (*httptest.Server, string) {
	t.Helper()
	app := openPool(t, appDSN(t))
	srv := api.New(api.Config{RotationGrace: time.Hour})
	srv.AttachDB(app)
	// Slice 197: JWT bearer via slice 190 path (admin claims).
	bearer := srv.IssueTestJWT(t, testjwt.AdminFor(uuid.MustParse(tenant)))
	h := srv.HTTPHandlerForTests()
	if h == nil {
		t.Fatal("HTTPHandlerForTests nil")
	}
	ts := httptest.NewServer(h)
	t.Cleanup(func() {
		ts.Close()
		app.Close()
	})
	return ts, bearer
}

// seedFrameworkVersion inserts a framework + framework_version row for
// this tenant via the admin pool (BYPASSRLS — direct row insert with
// the tenant_id column set). Returns the framework_version_id used by
// the audit-period seed.
func seedFrameworkVersion(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	fwID := uuid.New()
	fvID := uuid.New()
	tenantUUID, _ := uuid.Parse(tenant)
	if _, err := admin.Exec(ctx,
		`INSERT INTO frameworks (id, tenant_id, slug, name, issuer)
		 VALUES ($1, $2, $3, $4, $5)`,
		fwID, tenantUUID, "test-fw-"+fwID.String()[:8], "Test Framework", "test-issuer",
	); err != nil {
		t.Fatalf("insert framework: %v", err)
	}
	if _, err := admin.Exec(ctx,
		`INSERT INTO framework_versions (id, tenant_id, framework_id, version)
		 VALUES ($1, $2, $3, $4)`,
		fvID, tenantUUID, fwID, "v1"); err != nil {
		t.Fatalf("insert framework_version: %v", err)
	}
	return fvID
}

// seedAuditPeriod inserts one audit_period row directly via the admin
// pool. When frozenAt is non-nil, the period is inserted in
// status='frozen' with the full freeze-metadata triple populated.
func seedAuditPeriod(t *testing.T, admin *pgxpool.Pool, tenant string, fvID uuid.UUID, name string, frozenAt *time.Time) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	tenantUUID, _ := uuid.Parse(tenant)
	periodID := uuid.New()
	now := time.Now().UTC()
	periodStart := now.Add(-90 * 24 * time.Hour).Format("2006-01-02")
	periodEnd := now.Add(-1 * 24 * time.Hour).Format("2006-01-02")

	if frozenAt != nil {
		if _, err := admin.Exec(ctx,
			`INSERT INTO audit_periods
			 (id, tenant_id, name, framework_version_id, period_start, period_end,
			  status, frozen_at, frozen_hash, frozen_by, created_by)
			 VALUES ($1, $2, $3, $4, $5::date, $6::date,
			         'frozen', $7, $8::bytea, 'test-frozen-by', 'test-created-by')`,
			periodID, tenantUUID, name, fvID, periodStart, periodEnd,
			frozenAt.UTC(), []byte("\xde\xad\xbe\xef"),
		); err != nil {
			t.Fatalf("insert frozen audit_period: %v", err)
		}
	} else {
		if _, err := admin.Exec(ctx,
			`INSERT INTO audit_periods
			 (id, tenant_id, name, framework_version_id, period_start, period_end,
			  status, created_by)
			 VALUES ($1, $2, $3, $4, $5::date, $6::date, 'open', 'test-created-by')`,
			periodID, tenantUUID, name, fvID, periodStart, periodEnd,
		); err != nil {
			t.Fatalf("insert open audit_period: %v", err)
		}
	}
	return periodID
}

func doGet(t *testing.T, url, bearer string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}

// extractSearchableText converts the export body to a flat string that
// substring-matches over the cell values, regardless of format. CSV +
// JSON are already text; XLSX is a zip whose sheet1.xml carries cell
// text.
func extractSearchableText(t *testing.T, format string, body []byte) string {
	t.Helper()
	if format != "xlsx" {
		return string(body)
	}
	r, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("xlsx zip open: %v", err)
	}
	for _, f := range r.File {
		if f.Name == "xl/worksheets/sheet1.xml" {
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("xlsx sheet open: %v", err)
			}
			defer func() { _ = rc.Close() }()
			data, err := io.ReadAll(rc)
			if err != nil {
				t.Fatalf("xlsx sheet read: %v", err)
			}
			return string(data)
		}
	}
	t.Fatalf("xlsx body missing sheet1.xml")
	return ""
}

// ===== Tests =====

// AC-1: the audit-periods export endpoint returns 200 + a sane body
// for the CSV happy path. Header row is emitted and contains the
// canonical column set.
func TestAuditPeriodsExport_HappyPathCSV(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	tenant := freshTenant(t, admin)
	ts, bearer := setupHTTPServer(t, tenant)

	fvID := seedFrameworkVersion(t, admin, tenant)
	seedAuditPeriod(t, admin, tenant, fvID, "Q1-2026", nil)

	url := ts.URL + "/v1/admin/audit-periods/export?format=csv"
	resp, body := doGet(t, url, bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; body = %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/csv; charset=utf-8" {
		t.Errorf("Content-Type = %q; want text/csv; charset=utf-8", got)
	}
	cd := resp.Header.Get("Content-Disposition")
	if !strings.HasPrefix(cd, `attachment; filename="audit-periods_`) {
		t.Errorf("Content-Disposition = %q; want prefix attachment; filename=\"audit-periods_", cd)
	}

	rows := mustParseCSV(t, string(body))
	if len(rows) < 2 {
		t.Fatalf("expected >= 2 CSV rows (header + 1 data row); got %d", len(rows))
	}
	wantHeader := []string{
		"id", "name", "framework_version_id", "period_start", "period_end",
		"status", "frozen_at", "frozen_by", "frozen_hash",
		"created_by", "created_at", "updated_at",
	}
	for _, col := range wantHeader {
		if !contains(rows[0], col) {
			t.Errorf("CSV header missing column %q; got %v", col, rows[0])
		}
	}
}

// AC-9: freeze-metadata columns are populated for frozen periods. The
// row for a frozen seed must carry frozen_at, frozen_by, and
// frozen_hash all non-empty.
func TestAuditPeriodsExport_FreezeMetadataIncluded(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	tenant := freshTenant(t, admin)
	ts, bearer := setupHTTPServer(t, tenant)

	fvID := seedFrameworkVersion(t, admin, tenant)
	frozenAt := time.Now().Add(-1 * time.Hour).UTC()
	periodID := seedAuditPeriod(t, admin, tenant, fvID, "Frozen-Q4-2025", &frozenAt)

	url := ts.URL + "/v1/admin/audit-periods/export?format=json"
	resp, body := doGet(t, url, bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; body = %s", resp.StatusCode, body)
	}
	var rows []map[string]string
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("json decode: %v; body = %s", err, body)
	}
	var found map[string]string
	for _, r := range rows {
		if r["id"] == periodID.String() {
			found = r
			break
		}
	}
	if found == nil {
		t.Fatalf("frozen period %s not in export; got %v", periodID, rows)
	}
	if found["status"] != "frozen" {
		t.Errorf("status = %q; want frozen", found["status"])
	}
	if found["frozen_at"] == "" {
		t.Errorf("frozen_at empty; want RFC3339 timestamp")
	}
	if found["frozen_by"] != "test-frozen-by" {
		t.Errorf("frozen_by = %q; want test-frozen-by", found["frozen_by"])
	}
	if found["frozen_hash"] != "deadbeef" {
		t.Errorf("frozen_hash = %q; want deadbeef (hex)", found["frozen_hash"])
	}
}

// AC-5/6: meta-audit row written on success. The me_audit_log row
// MUST carry action='audit_periods_export', the tenant id, and a
// success result.
func TestAuditPeriodsExport_MetaAuditFires(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	tenant := freshTenant(t, admin)
	ts, bearer := setupHTTPServer(t, tenant)

	fvID := seedFrameworkVersion(t, admin, tenant)
	seedAuditPeriod(t, admin, tenant, fvID, "MetaAuditTarget", nil)

	url := ts.URL + "/v1/admin/audit-periods/export?format=csv"
	resp, _ := doGet(t, url, bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	// Allow the deferred meta-audit write a brief window to commit.
	// (The handler writes it in a fresh tx after the body stream
	// completes.)
	var rowCount int
	var afterJSON string
	for tries := 0; tries < 20; tries++ {
		err := admin.QueryRow(context.Background(),
			`SELECT COUNT(*), COALESCE(MAX(after::text), '')
			 FROM me_audit_log
			 WHERE tenant_id = $1 AND action = 'audit_periods_export'`,
			tenant,
		).Scan(&rowCount, &afterJSON)
		if err != nil {
			t.Fatalf("me_audit_log probe: %v", err)
		}
		if rowCount > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if rowCount == 0 {
		t.Fatalf("expected >= 1 me_audit_log row with action=audit_periods_export; got 0")
	}
	// JSONB renders with whitespace around `:` so we match key + value
	// independently rather than the compact literal.
	if !strings.Contains(afterJSON, `"result"`) || !strings.Contains(afterJSON, `"success"`) {
		t.Errorf("after_state missing success result; got %s", afterJSON)
	}
	if !strings.Contains(afterJSON, `"format"`) || !strings.Contains(afterJSON, `"csv"`) {
		t.Errorf("after_state missing format=csv; got %s", afterJSON)
	}
}

// AC-10: cross-tenant isolation across all three formats. Tenant A
// runs the export; the response body MUST NOT contain anything
// uniquely identifying tenant B's seeded period.
func TestAuditPeriodsExport_CrossTenantIsolationAllThreeFormats(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	tsA, bearerA := setupHTTPServer(t, tenantA)
	_, _ = setupHTTPServer(t, tenantB)

	fvA := seedFrameworkVersion(t, admin, tenantA)
	fvB := seedFrameworkVersion(t, admin, tenantB)
	periodA := seedAuditPeriod(t, admin, tenantA, fvA, "tenantA-period", nil)
	periodB := seedAuditPeriod(t, admin, tenantB, fvB, "tenantB-secret-period", nil)

	for _, format := range []string{"csv", "json", "xlsx"} {
		format := format
		t.Run(format, func(t *testing.T) {
			url := tsA.URL + "/v1/admin/audit-periods/export?format=" + format
			resp, body := doGet(t, url, bearerA)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("format=%s status = %d; body = %s", format, resp.StatusCode, body)
			}
			search := extractSearchableText(t, format, body)
			// Tenant B's period name + period UUID must not leak
			// into tenant A's export.
			if strings.Contains(search, "tenantB-secret-period") {
				t.Errorf("CROSS-TENANT LEAK in format=%s: tenant B period name in body", format)
			}
			if strings.Contains(search, periodB.String()) {
				t.Errorf("CROSS-TENANT LEAK in format=%s: tenant B period UUID in body", format)
			}
			// Positive control: tenant A's period MUST appear.
			if !strings.Contains(search, periodA.String()) {
				t.Errorf("format=%s body does not contain tenant A period UUID — RLS may be over-eager", format)
			}
		})
	}
}

// ===== helpers =====

func mustParseCSV(t *testing.T, body string) [][]string {
	t.Helper()
	r := stdcsv.NewReader(strings.NewReader(body))
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("csv parse: %v; body=%q", err, body)
	}
	return rows
}

func contains(row []string, s string) bool {
	for _, c := range row {
		if c == s {
			return true
		}
	}
	return false
}

// unused import guard — keeps fmt available for ad-hoc debug prints.
var _ = fmt.Sprintf
