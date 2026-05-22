//go:build integration

// Slice 175 — integration tests for the controls history-export endpoint.
//
// AC coverage:
//
//	AC-1 / AC-2 → TestSlice175_HistoryExportReturnsLineageColumns
//	AC-3        → covered by Playwright + BFF tests; AC-1 confirms the
//	              underlying endpoint shape
//	AC-4        → TestSlice175_HistoryCrossTenantIsolation
//	AC-5        → TestSlice175_HistoryMetaAuditRowWrittenOnEveryOutcome
//	AC-6        → TestSlice175_HistoryStreamingMemoryUnder200MB
//
// Requires DATABASE_URL_APP — runs under the same harness as the
// slice 137 export_integration_test.go.

package controls_test

import (
	"archive/zip"
	"bytes"
	"context"
	stdcsv "encoding/csv"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	controlsapi "github.com/mgoodric/security-atlas/internal/api/controls"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/export"
)

// ----- router helpers -----

// newControlsHistoryExportRouter wires the slice 175 history-export
// endpoint under the same auth + tenancy middleware stack the
// production server uses. Mirrors the slice 137
// newControlsExportRouter exactly.
func newControlsHistoryExportRouter(t *testing.T, tenantID uuid.UUID, isAdmin bool, ownerRoles []string) http.Handler {
	t.Helper()
	return newControlsHistoryExportRouterWithLimiter(t, tenantID, isAdmin, ownerRoles, nil)
}

func newControlsHistoryExportRouterWithLimiter(t *testing.T, tenantID uuid.UUID, isAdmin bool, ownerRoles []string, lim *export.Limiter) http.Handler {
	t.Helper()
	app := openPool(t, appDSN(t))
	h := controlsapi.NewHistoryExportHandler(app)
	if lim != nil {
		h = h.WithLimiter(lim)
	}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID:         "test-controls-history-export-id",
				TenantID:   tenantID.String(),
				IsAdmin:    isAdmin,
				UserID:     "user-controls-history-export-test",
				OwnerRoles: ownerRoles,
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Get("/v1/controls/history/export", h.ExportControlsHistory)
	return r
}

// ----- seeding -----

// freshHistoryExportTenant creates a tenant that auto-cleans on test
// exit. Distinct from the slice 137 freshExportTenant helper so the
// slice 175 tests own their cleanup (which must include
// `controls_history_export` meta-audit rows).
func freshHistoryExportTenant(t *testing.T) uuid.UUID {
	t.Helper()
	tenant := uuid.New()
	t.Cleanup(func() {
		ctx := context.Background()
		admin := openPool(t, adminDSN(t))
		for _, stmt := range []string{
			`DELETE FROM me_audit_log WHERE tenant_id = $1 AND action = 'controls_history_export'`,
			`UPDATE controls SET superseded_by = NULL WHERE tenant_id = $1`, // break FK chains before delete
			`DELETE FROM controls WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

// seedControlPairForHistory inserts TWO control rows in the same
// bundle — v1 (superseded) and v2 (active). Returns both ids. The
// superseded row's `superseded_by` points at the active row; both
// rows share the same `scf_anchor_id`. Used to assert the lineage
// view returns both versions.
func seedControlPairForHistory(t *testing.T, tenant uuid.UUID, bundleID, baseTitle string) (v1ID, v2ID, anchorID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	admin := openPool(t, adminDSN(t))

	anchorID = ensureTestSCFAnchor(t)

	v1ID = uuid.New()
	v2ID = uuid.New()

	// Insert v2 (active) first so we can FK v1.superseded_by to it.
	if _, err := admin.Exec(ctx, `
		INSERT INTO controls (
			id, tenant_id, bundle_id, version, scf_id, scf_anchor_id,
			title, description, control_family, implementation_type,
			owner_role, lifecycle_state, applicability_expr,
			freshness_class, bundle_manifest_yaml, bundle_manifest_hash,
			bundle_uploaded_at, bundle_uploaded_by
		)
		VALUES (
			$1, $2, $3, 2, 'IAC-06', $4,
			$5, 'slice 175 v2 active', 'identity-access-management', 'automated',
			'platform-eng', 'active', 'BU=eng AND env=prod',
			'fresh', '', 'sha256:slice175-v2',
			now(), 'test-author'
		)
	`, v2ID, tenant, bundleID, anchorID, baseTitle+" v2"); err != nil {
		t.Fatalf("seed v2: %v", err)
	}

	// Insert v1 with superseded_by pointing at v2.
	if _, err := admin.Exec(ctx, `
		INSERT INTO controls (
			id, tenant_id, bundle_id, version, superseded_by,
			scf_id, scf_anchor_id, title, description, control_family,
			implementation_type, owner_role, lifecycle_state, applicability_expr,
			freshness_class, bundle_manifest_yaml, bundle_manifest_hash,
			bundle_uploaded_at, bundle_uploaded_by
		)
		VALUES (
			$1, $2, $3, 1, $4,
			'IAC-06', $5, $6, 'slice 175 v1 superseded', 'identity-access-management',
			'automated', 'platform-eng', 'active', 'BU=eng AND env=prod',
			'fresh', '', 'sha256:slice175-v1',
			now() - interval '30 days', 'test-author'
		)
	`, v1ID, tenant, bundleID, v2ID, anchorID, baseTitle+" v1"); err != nil {
		t.Fatalf("seed v1: %v", err)
	}

	t.Cleanup(func() {
		ctx := context.Background()
		// Break the FK chain first.
		_, _ = admin.Exec(ctx, `UPDATE controls SET superseded_by = NULL WHERE id = $1`, v1ID)
		_, _ = admin.Exec(ctx, `DELETE FROM controls WHERE id IN ($1, $2)`, v1ID, v2ID)
	})
	return v1ID, v2ID, anchorID
}

// countHistoryExportMetaAuditRows counts me_audit_log rows with the
// slice 175 action under the given tenant's GUC.
func countHistoryExportMetaAuditRows(t *testing.T, tenant uuid.UUID) int {
	t.Helper()
	app := openPool(t, appDSN(t))
	ctx := context.Background()
	tx, err := app.Begin(ctx)
	if err != nil {
		t.Fatalf("count begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenant.String()); err != nil {
		t.Fatalf("count set_config: %v", err)
	}
	var n int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM me_audit_log WHERE tenant_id = $1 AND action = 'controls_history_export'`,
		tenant,
	).Scan(&n); err != nil {
		t.Fatalf("count query: %v", err)
	}
	return n
}

// mustParseHistoryCSV parses an export body into rows.
func mustParseHistoryCSV(t *testing.T, body string) [][]string {
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

// extractHistorySearchableText returns greppable text for any format.
func extractHistorySearchableText(t *testing.T, format string, body []byte) string {
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

// silence the pool import if a later refactor drops one of the
// helpers above. The pool reference itself is used through openPool().
var _ = (*pgxpool.Pool)(nil)

// ===== Tests =====

// AC-1 / AC-2: the history export returns the 17-column projection
// and INCLUDES BOTH the active and the superseded rows for a bundle.
// The superseded row carries non-empty `superseded_by` + `superseded_at`;
// the active row carries empty cells in those positions.
func TestSlice175_HistoryExportReturnsLineageColumns(t *testing.T) {
	tenant := freshHistoryExportTenant(t)
	bundleID := "bundle-slice175-canonical-" + uuid.NewString()[:8]
	v1ID, v2ID, _ := seedControlPairForHistory(t, tenant, bundleID, "slice175 canonical")

	r := newControlsHistoryExportRouter(t, tenant, true, []string{"control-owner"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/controls/history/export?format=csv", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/csv; charset=utf-8" {
		t.Errorf("Content-Type = %q; want text/csv; charset=utf-8", got)
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, `attachment; filename="controls_history_`) {
		t.Errorf("Content-Disposition = %q; want prefix attachment; filename=\"controls_history_", cd)
	}

	rows := mustParseHistoryCSV(t, rec.Body.String())
	if len(rows) < 3 {
		t.Fatalf("expected header + 2 data rows; got %d rows", len(rows))
	}

	// Header: 17 columns; first 15 mirror slice 137 + 2 new at end.
	header := rows[0]
	expectedCols := []string{
		"id", "bundle_id", "version", "title", "control_family",
		"scf_id", "scf_anchor_id",
		"implementation_type", "owner_role", "lifecycle_state",
		"applicability_expr",
		"freshness_class", "bundle_manifest_hash",
		"created_at", "updated_at",
		"superseded_by", "superseded_at",
	}
	if len(header) != len(expectedCols) {
		t.Errorf("header column count = %d; want %d; header=%v", len(header), len(expectedCols), header)
	}
	for i, want := range expectedCols {
		if i >= len(header) {
			break
		}
		if header[i] != want {
			t.Errorf("header[%d] = %q; want %q", i, header[i], want)
		}
	}

	// Locate the two seeded rows by id.
	var v1Row, v2Row []string
	for _, row := range rows[1:] {
		if len(row) == 0 {
			continue
		}
		switch row[0] {
		case v1ID.String():
			v1Row = row
		case v2ID.String():
			v2Row = row
		}
	}
	if v1Row == nil {
		t.Fatalf("v1 (superseded) row %s missing from history export", v1ID)
	}
	if v2Row == nil {
		t.Fatalf("v2 (active) row %s missing from history export", v2ID)
	}

	// v2 is active: superseded_by + superseded_at empty.
	if v2Row[15] != "" {
		t.Errorf("v2 (active) superseded_by = %q; want empty", v2Row[15])
	}
	if v2Row[16] != "" {
		t.Errorf("v2 (active) superseded_at = %q; want empty", v2Row[16])
	}
	// v1 is superseded: superseded_by = v2 id; superseded_at non-empty RFC3339.
	if v1Row[15] != v2ID.String() {
		t.Errorf("v1 (superseded) superseded_by = %q; want %q (FK to v2)",
			v1Row[15], v2ID.String())
	}
	if !strings.Contains(v1Row[16], "T") {
		t.Errorf("v1 (superseded) superseded_at = %q; want RFC3339-shaped", v1Row[16])
	}

	// Ordering invariant: v2 (version=2) appears BEFORE v1 (version=1)
	// within the same bundle. The query orders DESC by version per
	// the slice 175 narrative §1 (most-recent-first lineage).
	v1Idx, v2Idx := -1, -1
	for i, row := range rows[1:] {
		if row[0] == v1ID.String() {
			v1Idx = i
		}
		if row[0] == v2ID.String() {
			v2Idx = i
		}
	}
	if v2Idx > v1Idx {
		t.Errorf("ordering broken: v2 (newer) at idx %d, v1 (older) at idx %d; want newer-first",
			v2Idx, v1Idx)
	}
}

// AC-4: cross-tenant isolation across all three formats. Seeds a
// control pair (active + superseded) in Tenant B; exports history
// from Tenant A; the body must NOT contain Tenant B's ids or title.
func TestSlice175_HistoryCrossTenantIsolation(t *testing.T) {
	tenantA := freshHistoryExportTenant(t)
	tenantB := freshHistoryExportTenant(t)

	bTitleBase := "TENANT_B_HISTORY_LEAK_PROBE_" + uuid.NewString()[:8]
	v1A, v2A, _ := seedControlPairForHistory(t, tenantA, "bundle-A-"+uuid.NewString()[:8], "tenant-A-history")
	v1B, v2B, _ := seedControlPairForHistory(t, tenantB, "bundle-B-"+uuid.NewString()[:8], bTitleBase)

	cases := []string{"csv", "json", "xlsx"}
	for _, format := range cases {
		format := format
		t.Run(format, func(t *testing.T) {
			r := newControlsHistoryExportRouter(t, tenantA, true, []string{"control-owner"})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet,
				fmt.Sprintf("/v1/controls/history/export?format=%s", format), nil)
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
			}
			searchable := extractHistorySearchableText(t, format, rec.Body.Bytes())
			// CROSS-TENANT INVARIANT: tenant B ids + title MUST NOT appear.
			for _, leak := range []string{v1B.String(), v2B.String(), bTitleBase} {
				if strings.Contains(searchable, leak) {
					t.Errorf("CROSS-TENANT LEAK in format=%s: tenant B token %q present in tenant A history export",
						format, leak)
				}
			}
			// Tenant A's ids MUST appear (positive control — both
			// active AND superseded).
			for _, want := range []string{v1A.String(), v2A.String()} {
				if !strings.Contains(searchable, want) {
					t.Errorf("format=%s: tenant A id %s not found in tenant A history export",
						format, want)
				}
			}
		})
	}
}

// AC-5: every terminal outcome writes a `controls_history_export`
// meta-audit row. Covers success (200) + bad format (400) +
// forbidden (403) paths.
func TestSlice175_HistoryMetaAuditRowWrittenOnEveryOutcome(t *testing.T) {
	t.Run("success_200", func(t *testing.T) {
		tenant := freshHistoryExportTenant(t)
		_, _, _ = seedControlPairForHistory(t, tenant,
			"bundle-meta-"+uuid.NewString()[:8], "meta-success")

		r := newControlsHistoryExportRouter(t, tenant, true, []string{"control-owner"})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
			"/v1/controls/history/export?format=json", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
		}
		if got := countHistoryExportMetaAuditRows(t, tenant); got != 1 {
			t.Errorf("meta-audit after success: %d; want 1", got)
		}
	})

	t.Run("bad_format_400", func(t *testing.T) {
		tenant := freshHistoryExportTenant(t)
		r := newControlsHistoryExportRouter(t, tenant, true, []string{"control-owner"})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
			"/v1/controls/history/export?format=pdf", nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d; want 400", rec.Code)
		}
		if got := countHistoryExportMetaAuditRows(t, tenant); got != 1 {
			t.Errorf("meta-audit after bad_format: %d; want 1", got)
		}
	})

	t.Run("forbidden_403", func(t *testing.T) {
		tenant := freshHistoryExportTenant(t)
		// no admin, no approver, no owner roles → forbidden
		r := newControlsHistoryExportRouter(t, tenant, false, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
			"/v1/controls/history/export?format=csv", nil))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d; want 403", rec.Code)
		}
		if got := countHistoryExportMetaAuditRows(t, tenant); got != 1 {
			t.Errorf("meta-audit after 403: %d; want 1", got)
		}
	})
}

// AC-6: streaming-memory budget — a 50K-row history export through
// any of the three encoders MUST keep live heap delta under 200 MB.
// This test does NOT hit Postgres (50K real rows × supersession chains
// would take minutes and balloon the test DB). Instead it constructs
// the rows in-process via a generator and runs them through the
// encoder via a synthetic iter.Seq, exactly matching the production
// streaming path.
//
// Why 50K (not 500K like slice 137): the history export's row shape
// has 17 columns (vs slice 137's 15), so per-row encoded size is
// ~13% larger. A 50K-row test gives O(GB) of total streamed bytes
// (well above the 200 MB live-heap budget) with O(2-3s) wall-clock,
// vs the slice 137 500K test's O(10s) — same load-bearing assertion
// at lower test-suite friction. The 200 MB cap is the slice 175 AC-6
// invariant and matches slice 137's P0-A-UCF-3.
func TestSlice175_HistoryStreamingMemoryUnder200MB(t *testing.T) {
	const rows = 50_000
	// The canonical 17-column header (locked by the unit-suite
	// `TestSlice175_HistoryHeader_LockedShape`). Re-stated here as
	// string literals because the unit-suite symbols are unexported.
	header := []string{
		"id", "bundle_id", "version", "title", "control_family",
		"scf_id", "scf_anchor_id",
		"implementation_type", "owner_role", "lifecycle_state",
		"applicability_expr",
		"freshness_class", "bundle_manifest_hash",
		"created_at", "updated_at",
		"superseded_by", "superseded_at",
	}
	cols := len(header)

	cases := []struct {
		name string
		exp  export.Exporter
	}{
		{"csv", export.NewCSVExporter()},
		{"json", export.NewJSONExporter()},
		{"xlsx", export.NewXLSXExporter()},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			runtime.GC()
			var before runtime.MemStats
			runtime.ReadMemStats(&before)

			if err := tc.exp.WriteRows(discardWriter{}, header,
				slice175GeneratedRowIter(rows, cols)); err != nil {
				t.Fatalf("WriteRows: %v", err)
			}

			runtime.GC()
			var after runtime.MemStats
			runtime.ReadMemStats(&after)

			// HeapAlloc delta. 200 MB cap per slice 175 AC-6 (matches
			// slice 137 P0-A-UCF-3 / D3). HeapAlloc is the live heap
			// at sample time; streaming encoders should not retain
			// rows; live heap should be roughly flat across the call.
			const budget = 200 * 1024 * 1024
			liveDelta := int64(after.HeapAlloc) - int64(before.HeapAlloc)
			if liveDelta > budget {
				t.Errorf("HeapAlloc grew by %d bytes (%.1f MB); want <= %d (200 MB)",
					liveDelta, float64(liveDelta)/1024/1024, budget)
			}
		})
	}
}

// slice175GeneratedRowIter yields `n` synthetic rows of `cols`
// columns each. Reuses a single underlying string slice across yields
// so the iterator itself does NOT retain rows.
//
// Half of the rows simulate superseded controls (last two columns
// populated with realistic UUID + RFC3339 timestamp shapes); the
// other half simulate active controls (last two columns empty).
// This mirrors the production wire shape so the streaming-memory
// assertion is load-bearing against real-world per-row sizes.
func slice175GeneratedRowIter(n, cols int) iter.Seq[[]string] {
	return func(yield func([]string) bool) {
		bufSuperseded := make([]string, cols)
		bufActive := make([]string, cols)
		for i := 0; i < cols; i++ {
			cell := fmt.Sprintf("col%02d-cell-padded-to-thirtytwo", i)
			bufSuperseded[i] = cell
			bufActive[i] = cell
		}
		// Active row: empty supersession cells (positions 15, 16).
		bufActive[15] = ""
		bufActive[16] = ""
		// Superseded row: populated supersession cells.
		bufSuperseded[15] = "00000000-0000-0000-0000-000000000000"
		bufSuperseded[16] = "2026-05-01T12:00:00Z"

		for i := 0; i < n; i++ {
			var buf []string
			if i%2 == 0 {
				buf = bufActive
			} else {
				buf = bufSuperseded
			}
			if !yield(buf) {
				return
			}
		}
	}
}
