//go:build integration

// Slice 135 — integration tests for the audit-log data-export endpoint.
//
// AC coverage:
//
//	AC-7  → TestSlice135_ExportEndpointReusesUnifiedAggregator
//	AC-8  → TestSlice135_RowCapEnforced413
//	AC-9  → TestSlice135_MetaAuditOnEveryOutcome
//	AC-11 → TestSlice135_CrossTenantIsolationAllThreeFormats
//	AC-12 → TestSlice135_AuditPeriodFreezingClampsWindow
//
// Requires DATABASE_URL_APP — runs under the same TestMain bootstrap as
// the slice-124 unified tests in `unified_integration_test.go`.

package adminauditlog_test

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
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/adminauditlog"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
)

// mustParseCSV parses a body the export endpoint emitted into the
// list of records (rows are []string of cell values). LazyQuotes is
// on so cells the encoder quoted to preserve CR/tab unquote cleanly.
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

// newExportRouter wires the slice-135 export endpoint under the same
// auth + tenancy middleware stack as slice 124. isAdmin maps to the
// credential's IsAdmin flag.
func newExportRouter(t *testing.T, tenantID uuid.UUID, isAdmin bool) http.Handler {
	t.Helper()
	h := adminauditlog.New(appPool)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID:       "key_test_export",
				TenantID: tenantID.String(),
				IsAdmin:  isAdmin,
				UserID:   "user-export-test",
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Get("/v1/admin/audit-log/export", h.ExportUnified)
	return r
}

// AC-7: the export endpoint reuses the slice-124 aggregator. Seed one
// row per kind for the tenant; export CSV; assert all nine kinds
// appear and exactly one row per kind is present. This is the
// "encoder wraps the same SELECT" contract.
func TestSlice135_ExportEndpointReusesUnifiedAggregator(t *testing.T) {
	tenant := uuid.New()
	cleanupUnifiedTables(t, tenant)
	for _, kc := range allNineKinds {
		seedUnifiedRow(t, tenant, kc.table)
	}

	from := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	url := fmt.Sprintf("/v1/admin/audit-log/export?format=csv&from=%s&to=%s", from, to)

	r := newExportRouter(t, tenant, true)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}

	// Content-Type + Content-Disposition contract.
	if got := rec.Header().Get("Content-Type"); got != "text/csv; charset=utf-8" {
		t.Errorf("Content-Type = %q; want text/csv; charset=utf-8", got)
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, `attachment; filename="audit-log_`) {
		t.Errorf("Content-Disposition = %q; want prefix attachment; filename=\"audit-log_", cd)
	}
	if !strings.HasSuffix(cd, `.csv"`) {
		t.Errorf("Content-Disposition = %q; want suffix .csv\"", cd)
	}

	body := rec.Body.String()
	// Header row + 9 data rows = 10 lines (last line ends with \n
	// so split-on-\n yields 11 elements with the final empty).
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) < 10 {
		t.Fatalf("expected >= 10 CSV lines (header + 9 rows); got %d in body %q",
			len(lines), body)
	}
	header := lines[0]
	for _, col := range []string{"occurred_at", "actor_id", "actor_name", "tenant_id", "kind", "target_type", "target_id", "action", "row_id", "payload_json"} {
		if !strings.Contains(header, col) {
			t.Errorf("CSV header missing column %q; got %q", col, header)
		}
	}

	// Count kinds across data rows. RLS keeps this tenant-scoped; one
	// row per kind seeded -> one row per kind in the export.
	// Parse the CSV body via stdlib so we look at the actual
	// `kind` column (index 4) rather than substring-matching against
	// the whole line — `evidence` (kind) collides with
	// `evidence_record` (target_type) on a naive grep.
	parsedRows := mustParseCSV(t, body)
	if len(parsedRows) < 10 {
		t.Fatalf("expected >= 10 parsed CSV rows; got %d", len(parsedRows))
	}
	kindCount := map[string]int{}
	for _, row := range parsedRows[1:] {
		if len(row) < 5 {
			continue
		}
		kindCount[row[4]]++
	}
	for _, kc := range allNineKinds {
		if kindCount[kc.kind] != 1 {
			t.Errorf("kind %q count = %d; want 1", kc.kind, kindCount[kc.kind])
		}
	}
}

// AC-8: the row cap returns 413 with an actionable body. The default
// cap is 100,000 (slice 135 D3) — seeding that many rows is expensive,
// so we override the threshold by checking the body shape against a
// smaller seed that we KNOW exceeds the cap. To make the test fast,
// we seed (defaultExportRowCap+10) rows in me_audit_log (the only one
// of the nine tables whose bulk-seed loop is cheap).
//
// Choosing me_audit_log: its INSERT has no FK references; bulk seed
// via generate_series is microseconds per row even at 100k.
func TestSlice135_RowCapEnforced413(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 100k-row seed in short mode")
	}
	tenant := uuid.New()
	cleanupUnifiedTables(t, tenant)

	// Bulk seed 100_001 rows so we cross the default cap (100_000)
	// by exactly one. The aggregator's "ask for cap+1 rows to detect
	// more-available" trip is what surfaces the 413.
	ctx := context.Background()
	tx, err := appPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin bulk: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenant.String()); err != nil {
		t.Fatalf("bulk set_config: %v", err)
	}
	const seedCount = 100_001
	if _, err := tx.Exec(ctx,
		`INSERT INTO me_audit_log (tenant_id, user_id, action, occurred_at)
		 SELECT $1, gen_random_uuid(), 'profile.update',
		        now() - (i || ' microseconds')::interval
		 FROM generate_series(1, $2) AS i`,
		tenant, seedCount,
	); err != nil {
		t.Fatalf("bulk insert: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("bulk commit: %v", err)
	}

	from := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	url := fmt.Sprintf("/v1/admin/audit-log/export?format=csv&from=%s&to=%s", from, to)

	r := newExportRouter(t, tenant, true)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d; want %d (413)", rec.Code, http.StatusRequestEntityTooLarge)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "row cap") {
		t.Errorf("413 body should mention the row cap; got %q", body)
	}
	if !strings.Contains(body, "narrow") {
		t.Errorf("413 body should suggest narrowing the filter; got %q", body)
	}

	// AC-9 hook: the 413 outcome still writes a meta-audit row.
	// Use the same pool to count rows with action='audit_log_export'
	// under this tenant; one new row should be present.
	if got := countExportMetaAuditRows(t, tenant); got != 1 {
		t.Errorf("meta-audit row count after 413 = %d; want 1", got)
	}
}

// AC-9: every outcome path writes a meta-audit row. Covers:
//
//   - success (200) → 1 row
//   - bad-request (400) → 1 row
//   - forbidden (403) → 1 row
//   - 413 covered separately in TestSlice135_RowCapEnforced413
//
// Each subtest uses a fresh tenant so the row count starts at zero
// and the test asserts the exact +1 delta.
func TestSlice135_MetaAuditOnEveryOutcome(t *testing.T) {
	from := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)

	t.Run("success_200", func(t *testing.T) {
		tenant := uuid.New()
		cleanupUnifiedTables(t, tenant)
		seedUnifiedRow(t, tenant, "decision_audit_log")

		url := fmt.Sprintf("/v1/admin/audit-log/export?format=json&from=%s&to=%s", from, to)
		r := newExportRouter(t, tenant, true)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
		}
		if got := countExportMetaAuditRows(t, tenant); got != 1 {
			t.Errorf("meta-audit after success: %d rows; want 1", got)
		}
	})

	t.Run("bad_request_400_missing_from", func(t *testing.T) {
		tenant := uuid.New()
		cleanupUnifiedTables(t, tenant)

		// Missing `from` → 400 in parseUnifiedParams.
		url := fmt.Sprintf("/v1/admin/audit-log/export?format=csv&to=%s", to)
		r := newExportRouter(t, tenant, true)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d; want 400", rec.Code)
		}
		if got := countExportMetaAuditRows(t, tenant); got != 1 {
			t.Errorf("meta-audit after 400: %d rows; want 1", got)
		}
	})

	t.Run("forbidden_403_no_eligible_role", func(t *testing.T) {
		tenant := uuid.New()
		cleanupUnifiedTables(t, tenant)

		url := fmt.Sprintf("/v1/admin/audit-log/export?format=csv&from=%s&to=%s", from, to)
		// isAdmin=false + no user_roles seeded → 403.
		r := newExportRouter(t, tenant, false)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d; want 403", rec.Code)
		}
		if got := countExportMetaAuditRows(t, tenant); got != 1 {
			t.Errorf("meta-audit after 403: %d rows; want 1", got)
		}
	})

	t.Run("bad_format_400", func(t *testing.T) {
		tenant := uuid.New()
		cleanupUnifiedTables(t, tenant)

		// PDF is explicitly out of scope (slice 135 P0-A11).
		url := fmt.Sprintf("/v1/admin/audit-log/export?format=pdf&from=%s&to=%s", from, to)
		r := newExportRouter(t, tenant, true)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d; want 400 (unsupported format)", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "csv|json|xlsx") {
			t.Errorf("body should mention valid formats; got %q", rec.Body.String())
		}
		if got := countExportMetaAuditRows(t, tenant); got != 1 {
			t.Errorf("meta-audit after bad_format: %d rows; want 1", got)
		}
	})
}

// AC-11: cross-tenant isolation across all three formats. Seeds nine
// rows in Tenant B; exports from Tenant A; asserts the body carries
// zero Tenant B identifiers.
func TestSlice135_CrossTenantIsolationAllThreeFormats(t *testing.T) {
	tenantA := uuid.New()
	tenantB := uuid.New()
	cleanupUnifiedTables(t, tenantA)
	cleanupUnifiedTables(t, tenantB)
	for _, kc := range allNineKinds {
		seedUnifiedRow(t, tenantA, kc.table)
		seedUnifiedRow(t, tenantB, kc.table)
	}

	from := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)

	cases := []string{"csv", "json", "xlsx"}
	for _, format := range cases {
		format := format
		t.Run(format, func(t *testing.T) {
			url := fmt.Sprintf("/v1/admin/audit-log/export?format=%s&from=%s&to=%s",
				format, from, to)
			r := newExportRouter(t, tenantA, true)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
			}

			// Extract the searchable text. CSV / JSON are
			// already text; XLSX is a zip whose sheet1.xml
			// carries cell text we can grep.
			searchable := extractSearchableText(t, format, rec.Body.Bytes())

			// Tenant B's UUID MUST NOT appear anywhere in the
			// body. Tenant A's UUID MUST appear at least once
			// (positive control — proves the export actually
			// ran). RLS on every base table is the load-
			// bearing contract.
			if strings.Contains(searchable, tenantB.String()) {
				t.Errorf("CROSS-TENANT LEAK in format=%s: body contains tenant B UUID %s",
					format, tenantB)
			}
			if !strings.Contains(searchable, tenantA.String()) {
				t.Errorf("format=%s body does not contain tenant A UUID %s — "+
					"sanity check failed (RLS may be over-eager or seed missing)",
					format, tenantA)
			}
		})
	}
}

// AC-12: audit-period freezing clamps the effective `to` boundary.
//
// Test design:
//
//  1. Freeze an audit_period whose [period_start, period_end] covers
//     "yesterday → today" and whose frozen_at = NOW - 30 minutes.
//  2. Seed two me_audit_log rows: one occurred_at = NOW - 1 hour
//     (BEFORE the frozen_at horizon — should appear in the export),
//     and one occurred_at = NOW (AFTER the frozen_at horizon —
//     should be excluded).
//  3. Export with from = NOW - 2 hours, to = NOW + 1 hour. The
//     request window overlaps the frozen period; the export MUST
//     clamp to = frozen_at and return ONLY the pre-frozen row.
func TestSlice135_AuditPeriodFreezingClampsWindow(t *testing.T) {
	tenant := uuid.New()
	cleanupUnifiedTables(t, tenant)

	now := time.Now().UTC()
	frozenAt := now.Add(-30 * time.Minute)

	// Build a frozen audit_period covering today.
	ctx := context.Background()
	tx, err := appPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin freeze setup: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenant.String()); err != nil {
		t.Fatalf("freeze set_config: %v", err)
	}

	// Need a framework_version_id for the period. Insert a
	// minimal framework + version under this tenant.
	fwID := uuid.New()
	fvID := uuid.New()
	if _, err := tx.Exec(ctx,
		`INSERT INTO frameworks (id, tenant_id, slug, name, issuer)
		 VALUES ($1, $2, $3, $4, $5)`,
		fwID, tenant, "test-fw-"+fwID.String()[:8], "Test Framework", "test-issuer"); err != nil {
		t.Fatalf("insert framework: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO framework_versions (id, tenant_id, framework_id, version)
		 VALUES ($1, $2, $3, $4)`,
		fvID, tenant, fwID, "v1"); err != nil {
		t.Fatalf("insert framework_version: %v", err)
	}

	periodID := uuid.New()
	periodStart := now.Add(-24 * time.Hour).Format("2006-01-02")
	periodEnd := now.Format("2006-01-02")
	if _, err := tx.Exec(ctx,
		`INSERT INTO audit_periods
		 (id, tenant_id, name, framework_version_id, period_start, period_end,
		  status, frozen_at, frozen_hash, frozen_by, created_by)
		 VALUES ($1, $2, $3, $4, $5::date, $6::date,
		         'frozen', $7, $8::bytea, 'test-frozen-by', 'test-created-by')`,
		periodID, tenant, "test-period", fvID,
		periodStart, periodEnd,
		frozenAt, []byte("test-hash"),
	); err != nil {
		t.Fatalf("insert frozen audit_period: %v", err)
	}

	// Pre-frozen row — MUST appear in the export.
	preID := uuid.New()
	if _, err := tx.Exec(ctx,
		`INSERT INTO me_audit_log (tenant_id, user_id, action, occurred_at)
		 VALUES ($1, $2, 'profile.update', $3)`,
		tenant, preID, frozenAt.Add(-15*time.Minute),
	); err != nil {
		t.Fatalf("insert pre-frozen row: %v", err)
	}

	// Post-frozen row — MUST NOT appear in the export.
	postID := uuid.New()
	if _, err := tx.Exec(ctx,
		`INSERT INTO me_audit_log (tenant_id, user_id, action, occurred_at)
		 VALUES ($1, $2, 'profile.update', $3)`,
		tenant, postID, now,
	); err != nil {
		t.Fatalf("insert post-frozen row: %v", err)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit freeze setup: %v", err)
	}
	t.Cleanup(func() {
		// Audit_periods row needs cleanup so the next test isn't
		// polluted; same for framework + framework_version.
		ctx := context.Background()
		_, _ = appPool.Exec(ctx, `DELETE FROM audit_periods WHERE id = $1`, periodID)
		_, _ = appPool.Exec(ctx, `DELETE FROM framework_versions WHERE id = $1`, fvID)
		_, _ = appPool.Exec(ctx, `DELETE FROM frameworks WHERE id = $1`, fwID)
	})

	// Request window: 2 hours before now to 1 hour after now.
	// Overlaps the frozen period; the export MUST clamp `to` to
	// frozen_at and exclude the post-frozen row.
	from := now.Add(-2 * time.Hour).Format(time.RFC3339)
	to := now.Add(1 * time.Hour).Format(time.RFC3339)
	url := fmt.Sprintf("/v1/admin/audit-log/export?format=json&from=%s&to=%s", from, to)

	r := newExportRouter(t, tenant, true)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, preID.String()) {
		t.Errorf("export MUST include pre-frozen row %s; body=%q", preID, body)
	}
	if strings.Contains(body, postID.String()) {
		t.Errorf("export MUST EXCLUDE post-frozen row %s (constitutional invariant #10); body=%q",
			postID, body)
	}
}

// ===== Helpers =====

// countExportMetaAuditRows counts me_audit_log rows with the slice 135
// action under the given tenant's GUC. Used by AC-9 outcome tests.
func countExportMetaAuditRows(t *testing.T, tenant uuid.UUID) int {
	t.Helper()
	ctx := context.Background()
	tx, err := appPool.Begin(ctx)
	if err != nil {
		t.Fatalf("count begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenant.String()); err != nil {
		t.Fatalf("count set_config: %v", err)
	}
	var n int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM me_audit_log WHERE tenant_id = $1 AND action = 'audit_log_export'`,
		tenant,
	).Scan(&n); err != nil {
		t.Fatalf("count query: %v", err)
	}
	return n
}

// extractSearchableText returns a substring-greppable rendering of the
// export body. CSV / JSON are already text; XLSX's sheet1.xml carries
// the cell text we can grep for UUID matches.
func extractSearchableText(t *testing.T, format string, body []byte) string {
	t.Helper()
	switch format {
	case "csv", "json":
		return string(body)
	case "xlsx":
		zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			t.Fatalf("xlsx zip open: %v", err)
		}
		for _, f := range zr.File {
			if f.Name != "xl/worksheets/sheet1.xml" {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("xlsx sheet open: %v", err)
			}
			defer rc.Close()
			b, err := io.ReadAll(rc)
			if err != nil {
				t.Fatalf("xlsx sheet read: %v", err)
			}
			return string(b)
		}
		t.Fatalf("xlsx body missing sheet1.xml")
		return ""
	default:
		t.Fatalf("unknown format %q", format)
		return ""
	}
}

// Plain unit-style smoke for JSON parsing of the export body — proves
// the wire format is array-of-objects with the expected keys.
func TestSlice135_JSONFormatIsArrayOfObjects(t *testing.T) {
	tenant := uuid.New()
	cleanupUnifiedTables(t, tenant)
	seedUnifiedRow(t, tenant, "decision_audit_log")

	from := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	url := fmt.Sprintf("/v1/admin/audit-log/export?format=json&from=%s&to=%s", from, to)

	r := newExportRouter(t, tenant, true)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q; want application/json", got)
	}

	var parsed []map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("body should be array-of-objects: %v; body=%q", err, rec.Body.String())
	}
	if len(parsed) < 1 {
		t.Fatalf("len(parsed) = %d; want >= 1", len(parsed))
	}
	got := parsed[0]
	for _, k := range []string{"occurred_at", "actor_id", "tenant_id", "kind", "row_id"} {
		if _, ok := got[k]; !ok {
			t.Errorf("first row missing key %q; got keys=%v", k, mapKeys(got))
		}
	}
}

func mapKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
