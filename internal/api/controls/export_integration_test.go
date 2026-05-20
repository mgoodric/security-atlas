//go:build integration

// Slice 137 — integration tests for the controls UCF graph data-export
// endpoint.
//
// AC coverage:
//
//	AC-1 / AC-3 → TestSlice137_ControlsExportReturnsCanonicalColumns
//	AC-4        → TestSlice137_CrossTenantIsolationAllThreeFormats
//	AC-6        → TestSlice137_MetaAuditRowWrittenOnEveryOutcome
//	AC-8        → TestSlice137_StreamingMemoryUnder200MBFor500KRows
//	            → TestSlice137_RoleGateForbidden
//	            → TestSlice137_ConcurrencyCapInherited
//	            → TestSlice137_EmptyTenantStreamsEmptyResult
//
// Requires DATABASE_URL_APP — runs under the same harness as the
// slice 011 attest_integration_test.go.

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
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	controlsapi "github.com/mgoodric/security-atlas/internal/api/controls"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/export"
)

// ----- router helpers (mirror slice 136's newRiskExportRouter) -----

// newControlsExportRouter wires the slice 137 export endpoint under
// the same auth + tenancy middleware stack the production server uses.
// isAdmin maps to credential.IsAdmin; ownerRoles maps to the
// program-read gate. Tests that want a forbidden caller pass an empty
// ownerRoles slice and isAdmin=false.
func newControlsExportRouter(t *testing.T, tenantID uuid.UUID, isAdmin bool, ownerRoles []string) http.Handler {
	t.Helper()
	return newControlsExportRouterWithLimiter(t, tenantID, isAdmin, ownerRoles, nil)
}

func newControlsExportRouterWithLimiter(t *testing.T, tenantID uuid.UUID, isAdmin bool, ownerRoles []string, lim *export.Limiter) http.Handler {
	t.Helper()
	app := openPool(t, appDSN(t))
	h := controlsapi.NewExportHandler(app)
	if lim != nil {
		h = h.WithLimiter(lim)
	}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID:         "test-controls-export-id",
				TenantID:   tenantID.String(),
				IsAdmin:    isAdmin,
				UserID:     "user-controls-export-test",
				OwnerRoles: ownerRoles,
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Get("/v1/controls/export", h.ExportControls)
	return r
}

// ----- seeding -----

// seedControlForExport inserts ONE active control for the given
// tenant. The tenant must already exist; SCF anchor must already
// exist. Returns the control id + the SCF anchor id used (so tests
// can assert the topology columns round-trip).
//
// The function reuses the slice 006 SCF anchor catalog (a single
// anchor per SCF code is shared across tests).
func seedControlForExport(t *testing.T, tenant uuid.UUID, bundleID, title string) (controlID uuid.UUID, anchorID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	admin := openPool(t, adminDSN(t))

	// Re-use or insert the test SCF anchor.
	anchorID = ensureTestSCFAnchor(t)

	controlID = uuid.New()
	if _, err := admin.Exec(ctx, `
		INSERT INTO controls (
			id, tenant_id, bundle_id, version, scf_id, scf_anchor_id,
			title, description, control_family, implementation_type,
			owner_role, lifecycle_state, applicability_expr,
			freshness_class, bundle_manifest_yaml, bundle_manifest_hash,
			bundle_uploaded_at, bundle_uploaded_by
		)
		VALUES (
			$1, $2, $3, 1, 'IAC-06', $4,
			$5, 'test control for slice 137 export', 'identity-access-management', 'automated',
			'platform-eng', 'active', 'BU=eng AND env=prod',
			'fresh', '', 'sha256:slice137',
			now(), 'test-author'
		)
	`, controlID, tenant, bundleID, anchorID, title); err != nil {
		t.Fatalf("seed control for export: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = admin.Exec(ctx, `DELETE FROM controls WHERE id = $1`, controlID)
	})
	return controlID, anchorID
}

// ensureTestSCFAnchor inserts a single test SCF anchor (IAC-06) if
// one doesn't already exist. Returns its uuid. Reused across all
// slice 137 tests so the FK pressure is minimal.
//
// The slice 011 attest_integration_test.go's `seedSCFAnchor` helper
// inserts an anchor and returns its CODE (not its uuid); slice 137
// needs the uuid for the controls.scf_anchor_id FK target, so this
// wrapper does the SCF anchor uuid lookup itself.
func ensureTestSCFAnchor(t *testing.T) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	admin := openPool(t, adminDSN(t))

	// Try to read an existing anchor first — most tests share the
	// catalog, so don't re-INSERT every time.
	var existing uuid.UUID
	err := admin.QueryRow(ctx, `
		SELECT a.id FROM scf_anchors a
		JOIN framework_versions fv ON fv.id = a.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = 'scf' AND f.tenant_id IS NULL AND a.scf_id = 'IAC-06'
		LIMIT 1
	`).Scan(&existing)
	if err == nil {
		return existing
	}

	// Else lean on the package-local seedSCFAnchor helper (already in
	// attest_integration_test.go) to plant the anchor.
	_ = seedSCFAnchor(t, admin, "IAC-06", "identity-access-management")
	// Look it up again.
	if err := admin.QueryRow(ctx, `
		SELECT a.id FROM scf_anchors a
		JOIN framework_versions fv ON fv.id = a.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = 'scf' AND f.tenant_id IS NULL AND a.scf_id = 'IAC-06'
		LIMIT 1
	`).Scan(&existing); err != nil {
		t.Fatalf("re-lookup IAC-06 anchor: %v", err)
	}
	return existing
}

// freshExportTenant creates a tenant that auto-cleans on test exit.
// Distinct from the slice-009 freshTenant helper so the slice 137
// tests own their cleanup (which must include me_audit_log rows for
// the slice 137 action).
func freshExportTenant(t *testing.T) uuid.UUID {
	t.Helper()
	tenant := uuid.New()
	t.Cleanup(func() {
		ctx := context.Background()
		admin := openPool(t, adminDSN(t))
		for _, stmt := range []string{
			`DELETE FROM me_audit_log WHERE tenant_id = $1 AND action = 'controls_export'`,
			`DELETE FROM evidence_records WHERE tenant_id = $1`,
			`DELETE FROM controls WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

// countControlsExportMetaAuditRows counts me_audit_log rows with the
// slice 137 action under the given tenant's GUC.
func countControlsExportMetaAuditRows(t *testing.T, tenant uuid.UUID) int {
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
		`SELECT count(*) FROM me_audit_log WHERE tenant_id = $1 AND action = 'controls_export'`,
		tenant,
	).Scan(&n); err != nil {
		t.Fatalf("count query: %v", err)
	}
	return n
}

// mustParseControlsCSV parses an export body into rows.
func mustParseControlsCSV(t *testing.T, body string) [][]string {
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

// extractControlsSearchableText returns greppable text for any format.
func extractControlsSearchableText(t *testing.T, format string, body []byte) string {
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

// ----- Tests -----

// AC-1 / AC-3: the export endpoint returns CSV with the canonical
// column set. Seed one control; export CSV; verify the header row +
// data row + the column ordering.
func TestSlice137_ControlsExportReturnsCanonicalColumns(t *testing.T) {
	tenant := freshExportTenant(t)
	id, anchorID := seedControlForExport(t, tenant, "bundle-slice137-canonical", "slice137 canonical control")

	r := newControlsExportRouter(t, tenant, true, []string{"control-owner"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/controls/export?format=csv", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/csv; charset=utf-8" {
		t.Errorf("Content-Type = %q; want text/csv; charset=utf-8", got)
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, `attachment; filename="controls_`) {
		t.Errorf("Content-Disposition = %q; want prefix attachment; filename=\"controls_", cd)
	}
	rows := mustParseControlsCSV(t, rec.Body.String())
	if len(rows) < 2 {
		t.Fatalf("expected at least header + 1 data row; got %d rows", len(rows))
	}
	header := rows[0]
	expectedCols := []string{
		"id", "bundle_id", "version", "title", "control_family",
		"scf_id", "scf_anchor_id",
		"implementation_type", "owner_role", "lifecycle_state",
		"applicability_expr",
		"freshness_class", "bundle_manifest_hash",
		"created_at", "updated_at",
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
	// Data row sanity — find the row for our seeded control.
	foundID := false
	for _, row := range rows[1:] {
		if len(row) > 0 && row[0] == id.String() {
			foundID = true
			// bundle_id @ 1
			if row[1] != "bundle-slice137-canonical" {
				t.Errorf("data row bundle_id = %q; want %q", row[1], "bundle-slice137-canonical")
			}
			// title @ 3
			if row[3] != "slice137 canonical control" {
				t.Errorf("data row title = %q; want %q", row[3], "slice137 canonical control")
			}
			// scf_anchor_id @ 6 — UCF graph join key. The slice 137 D1
			// flat projection's load-bearing column.
			if row[6] != anchorID.String() {
				t.Errorf("data row scf_anchor_id = %q; want %q (graph join key broken)",
					row[6], anchorID.String())
			}
			// applicability_expr @ 10 — the tenant-private DSL.
			if row[10] != "BU=eng AND env=prod" {
				t.Errorf("data row applicability_expr = %q; want %q",
					row[10], "BU=eng AND env=prod")
			}
			break
		}
	}
	if !foundID {
		t.Errorf("seeded control id %s not present in export", id)
	}
}

// AC-4: cross-tenant isolation across all three formats. Seeds one
// control in Tenant B with a distinctive title; exports from Tenant A;
// the body must NOT contain Tenant B's control id or title.
func TestSlice137_CrossTenantIsolationAllThreeFormats(t *testing.T) {
	tenantA := freshExportTenant(t)
	tenantB := freshExportTenant(t)

	bTitle := "TENANT_B_LEAK_PROBE_" + uuid.New().String()
	idA, _ := seedControlForExport(t, tenantA, "bundle-A-"+uuid.NewString()[:8], "tenant-A-control")
	idB, _ := seedControlForExport(t, tenantB, "bundle-B-"+uuid.NewString()[:8], bTitle)

	cases := []string{"csv", "json", "xlsx"}
	for _, format := range cases {
		format := format
		t.Run(format, func(t *testing.T) {
			r := newControlsExportRouter(t, tenantA, true, []string{"control-owner"})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/v1/controls/export?format=%s", format), nil)
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
			}
			searchable := extractControlsSearchableText(t, format, rec.Body.Bytes())
			// CROSS-TENANT INVARIANT: tenant B's id + title MUST NOT appear.
			if strings.Contains(searchable, idB.String()) {
				t.Errorf("CROSS-TENANT LEAK in format=%s: tenant B control id %s present in tenant A export", format, idB)
			}
			if strings.Contains(searchable, bTitle) {
				t.Errorf("CROSS-TENANT LEAK in format=%s: tenant B title %q present in tenant A export", format, bTitle)
			}
			// Tenant A's id MUST appear (positive control).
			if !strings.Contains(searchable, idA.String()) {
				t.Errorf("format=%s: tenant A id %s not found in tenant A export — RLS may be over-eager or seed missing",
					format, idA)
			}
		})
	}
}

// AC-6: every terminal outcome writes a `controls_export` meta-audit
// row. Covers success (200) + bad format (400) + forbidden (403) paths.
func TestSlice137_MetaAuditRowWrittenOnEveryOutcome(t *testing.T) {
	t.Run("success_200", func(t *testing.T) {
		tenant := freshExportTenant(t)
		_, _ = seedControlForExport(t, tenant, "bundle-meta-"+uuid.NewString()[:8], "meta-success")

		r := newControlsExportRouter(t, tenant, true, []string{"control-owner"})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/controls/export?format=json", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
		}
		if got := countControlsExportMetaAuditRows(t, tenant); got != 1 {
			t.Errorf("meta-audit after success: %d; want 1", got)
		}
	})

	t.Run("bad_format_400", func(t *testing.T) {
		tenant := freshExportTenant(t)
		r := newControlsExportRouter(t, tenant, true, []string{"control-owner"})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/controls/export?format=pdf", nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d; want 400", rec.Code)
		}
		if got := countControlsExportMetaAuditRows(t, tenant); got != 1 {
			t.Errorf("meta-audit after bad_format: %d; want 1", got)
		}
	})

	t.Run("forbidden_403", func(t *testing.T) {
		tenant := freshExportTenant(t)
		// no admin, no approver, no owner roles → forbidden
		r := newControlsExportRouter(t, tenant, false, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/controls/export?format=csv", nil))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d; want 403", rec.Code)
		}
		if got := countControlsExportMetaAuditRows(t, tenant); got != 1 {
			t.Errorf("meta-audit after 403: %d; want 1", got)
		}
	})
}

// Role-gate: a credential with no admin / approver / owner-roles flag
// is forbidden (status 403). Mirrors slice 067 AC-6.
func TestSlice137_RoleGateForbidden(t *testing.T) {
	tenant := freshExportTenant(t)
	r := newControlsExportRouter(t, tenant, false, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/controls/export?format=csv", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d; want 403; body=%s", rec.Code, rec.Body.String())
	}
}

// Concurrency cap inherited from slice 145: with a deterministic cap=2
// limiter, five concurrent exports should yield at least 1 429 (the
// cap fired). Same shape as slice 136's test.
func TestSlice137_ConcurrencyCapInherited(t *testing.T) {
	tenant := freshExportTenant(t)
	_, _ = seedControlForExport(t, tenant, "bundle-conc-"+uuid.NewString()[:8], "concurrency-probe")

	lim := export.NewLimiter(2)
	r := newControlsExportRouterWithLimiter(t, tenant, true, []string{"control-owner"}, lim)

	const N = 5
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		statuses []int
	)

	start := make(chan struct{})
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/controls/export?format=json", nil))
			mu.Lock()
			statuses = append(statuses, rec.Code)
			mu.Unlock()
		}()
	}
	close(start)
	wg.Wait()

	ok, denied := 0, 0
	for _, s := range statuses {
		switch s {
		case http.StatusOK:
			ok++
		case http.StatusTooManyRequests:
			denied++
		default:
			t.Errorf("unexpected status %d", s)
		}
	}
	if denied == 0 {
		t.Errorf("expected at least 1 denial (429) under cap=2 with %d concurrent workers; got 0; statuses=%v", N, statuses)
	}
	if ok+denied != N {
		t.Errorf("status accounting: ok=%d denied=%d sum=%d; want %d", ok, denied, ok+denied, N)
	}
}

// Empty tenant streams an empty result body with a `row_count: 0`
// meta-audit row. Exercises the encoder + meta-audit pipeline
// end-to-end without any seeded data.
func TestSlice137_EmptyTenantStreamsEmptyResult(t *testing.T) {
	tenant := freshExportTenant(t)
	r := newControlsExportRouter(t, tenant, true, []string{"control-owner"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/controls/export?format=csv", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if got := countControlsExportMetaAuditRows(t, tenant); got != 1 {
		t.Errorf("meta-audit after empty success: %d; want 1", got)
	}
}

// AC-8 / P0-A-UCF-3 / P0-A6: streaming-memory budget — a 500K-row
// export through any of the three encoders MUST keep live heap delta
// under 200 MB. This test does NOT hit Postgres (500K real rows would
// take minutes and balloon the test DB). Instead it constructs the
// 500K rows in-process via a generator and runs them through the
// encoder via a synthetic iter.Seq, exactly matching the production
// streaming path (the encoder doesn't care where rows come from).
//
// Mirrors the slice 135 `TestStreamingMemoryUnder50MBFor100KRows`
// test, scaled to 500K rows × 200 MB per slice 137 D3.
//
// Why this lives in the integration suite, not the unit suite: it
// runs for ~10s and allocates O(GB) of total bytes through the
// encoder writer pipeline; keeping it out of the unit suite avoids
// slowing `go test ./...` for routine PRs.
func TestSlice137_StreamingMemoryUnder200MBFor500KRows(t *testing.T) {
	const rows = 500_000
	// The canonical 15-column header (locked by the unit-suite
	// `TestSlice137_ControlsExportHeader_StableOrder`). Re-stated
	// here as string literals because the unit-suite `controls`
	// package symbols are unexported; this test lives in
	// `controls_test`.
	header := []string{
		"id", "bundle_id", "version", "title", "control_family",
		"scf_id", "scf_anchor_id",
		"implementation_type", "owner_role", "lifecycle_state",
		"applicability_expr",
		"freshness_class", "bundle_manifest_hash",
		"created_at", "updated_at",
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
				slice137GeneratedRowIter(rows, cols)); err != nil {
				t.Fatalf("WriteRows: %v", err)
			}

			runtime.GC()
			var after runtime.MemStats
			runtime.ReadMemStats(&after)

			// HeapAlloc snapshot delta. 200 MB cap per slice 137
			// P0-A-UCF-3 / D3 (4x slice 135's 50 MB unit-suite cap;
			// the row count is 5x and the column count is +1 over the
			// audit-log shape, so the budget grows roughly linearly).
			//
			// We compare HeapAlloc directly (live heap at sample
			// time) rather than TotalAlloc (cumulative). Streaming
			// encoders should not retain rows; live heap should be
			// roughly flat across the call.
			const budget = 200 * 1024 * 1024
			liveDelta := int64(after.HeapAlloc) - int64(before.HeapAlloc)
			if liveDelta > budget {
				t.Errorf("HeapAlloc grew by %d bytes (%.1f MB); want <= %d (200 MB)",
					liveDelta, float64(liveDelta)/1024/1024, budget)
			}
		})
	}
}

// slice137GeneratedRowIter yields `n` synthetic rows of `cols`
// columns each. Reuses a single underlying string slice across yields
// so the iterator itself does NOT retain rows (the encoder's
// allocation, not the generator's, is what we're measuring).
func slice137GeneratedRowIter(n, cols int) iter.Seq[[]string] {
	return func(yield func([]string) bool) {
		buf := make([]string, cols)
		for i := 0; i < cols; i++ {
			// Each cell is ~32 bytes — close enough to the average
			// control row's cell size (uuid + bundle_id + title ~32
			// chars each).
			buf[i] = fmt.Sprintf("col%02d-cell-padded-to-thirtytwo", i)
		}
		for i := 0; i < n; i++ {
			if !yield(buf) {
				return
			}
		}
	}
}

// discardWriter is the slice 135 export library's test-only sink —
// re-declared locally so the slice 137 integration test doesn't have
// to take a hard dependency on a test-only export from the library.
// `io.Discard` would also work; this version is explicit about its
// purpose at the call site.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
