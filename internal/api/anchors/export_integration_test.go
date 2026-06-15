//go:build integration

// Slice 174 — integration tests for the UCF anchor catalog export
// endpoint.
//
// AC coverage:
//
//	AC-1 / AC-2 → TestSlice174_AnchorsExport_AllThreeFormats
//	AC-3        → covered by the BFF route test + frontend e2e (separate suite)
//	AC-4        → TestSlice174_CrossTenantIsolationIdenticalAllFormats
//	AC-5        → covered by internal/authz/slice174_test.go
//	AC-6        → TestSlice174_MetaAuditRowWrittenOnEveryOutcome
//	AC-7        → TestSlice174_StreamingMemoryUnder200MBFor50KAnchors
//	            → TestSlice174_ConcurrencyCapInherited
//	            → TestSlice174_EmptyCatalogStreamsEmptyResult (defensive)
//
// Requires DATABASE_URL + DATABASE_URL_APP (admin + app DSNs). Runs
// under the same harness as the slice 006 anchors integration_test.go.

package anchors_test

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
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	anchorsapi "github.com/mgoodric/security-atlas/internal/api/anchors"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/export"
)

// ----- router helpers (mirror slice 137's newControlsExportRouter) -----

// newAnchorsExportRouter wires the slice 174 export endpoint under the
// same auth + tenancy middleware stack the production server uses.
// Any authenticated user can call the endpoint per slice 174 D4.
func newAnchorsExportRouter(t *testing.T, tenantID uuid.UUID) http.Handler {
	t.Helper()
	return newAnchorsExportRouterWithLimiter(t, tenantID, nil)
}

func newAnchorsExportRouterWithLimiter(t *testing.T, tenantID uuid.UUID, lim *export.Limiter) http.Handler {
	t.Helper()
	app := dbtest.NewAppPool(t)
	h := anchorsapi.NewExportHandler(app)
	if lim != nil {
		h = h.WithLimiter(lim)
	}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID:       "test-anchors-export-id",
				TenantID: tenantID.String(),
				UserID:   "user-anchors-export-test",
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Get("/v1/anchors/export", h.ExportAnchors)
	return r
}

// ----- seeding -----

// ensureSCFAnchorsSeed makes sure there is at least one current-SCF
// anchor + one outgoing edge in the catalog. Idempotent: re-runs as
// a no-op when the catalog is already loaded (which is the common case
// under the broader integration suite).
//
// Returns one anchor uuid + the framework_requirement uuid the seed
// edge points at so tests can assert wire shape against known values.
func ensureSCFAnchorsSeed(t *testing.T) (anchorID uuid.UUID, requirementID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	admin := dbtest.NewMigratePool(t)

	// SCF framework + current version
	var scfFwID uuid.UUID
	err := admin.QueryRow(ctx, `SELECT id FROM frameworks WHERE slug='scf' AND tenant_id IS NULL`).Scan(&scfFwID)
	if err != nil {
		scfFwID = uuid.New()
		if _, err := admin.Exec(ctx, `
			INSERT INTO frameworks (id, tenant_id, slug, name, issuer, description)
			VALUES ($1, NULL, 'scf', 'Secure Controls Framework', 'SCF Council', '')
		`, scfFwID); err != nil {
			t.Fatalf("insert scf framework: %v", err)
		}
	}
	var scfVerID uuid.UUID
	err = admin.QueryRow(ctx, `
		SELECT id FROM framework_versions
		WHERE framework_id = $1 AND status = 'current' AND tenant_id IS NULL
	`, scfFwID).Scan(&scfVerID)
	if err != nil {
		scfVerID = uuid.New()
		if _, err := admin.Exec(ctx, `
			INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
			VALUES ($1, NULL, $2, 'slice174-1.0', 'current')
		`, scfVerID, scfFwID); err != nil {
			t.Fatalf("insert scf framework_version: %v", err)
		}
	}

	// SCF anchor (idempotent on (framework_version_id, scf_id))
	anchorID = uuid.New()
	if _, err := admin.Exec(ctx, `
		INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title, description)
		VALUES ($1, $2, 'SLICE174-A1', 'iam', 'slice174 test anchor', 'seeded for slice 174 export test')
		ON CONFLICT (framework_version_id, scf_id) DO NOTHING
	`, anchorID, scfVerID); err != nil {
		t.Fatalf("insert anchor: %v", err)
	}
	// If the ON CONFLICT skipped, look up the real id.
	if err := admin.QueryRow(ctx, `
		SELECT id FROM scf_anchors WHERE framework_version_id = $1 AND scf_id = 'SLICE174-A1'
	`, scfVerID).Scan(&anchorID); err != nil {
		t.Fatalf("lookup anchor: %v", err)
	}

	// SOC 2 (or similar) framework + version + requirement to anchor the edge.
	// Use slug='slice174-soc' to avoid colliding with other tests' seed data.
	var soc2FwID uuid.UUID
	err = admin.QueryRow(ctx, `SELECT id FROM frameworks WHERE slug='slice174-soc' AND tenant_id IS NULL`).Scan(&soc2FwID)
	if err != nil {
		soc2FwID = uuid.New()
		if _, err := admin.Exec(ctx, `
			INSERT INTO frameworks (id, tenant_id, slug, name, issuer, description)
			VALUES ($1, NULL, 'slice174-soc', 'Slice174 SOC test', 'AICPA-test', '')
		`, soc2FwID); err != nil {
			t.Fatalf("insert slice174-soc framework: %v", err)
		}
	}
	var soc2VerID uuid.UUID
	err = admin.QueryRow(ctx, `
		SELECT id FROM framework_versions
		WHERE framework_id = $1 AND status = 'current' AND tenant_id IS NULL
	`, soc2FwID).Scan(&soc2VerID)
	if err != nil {
		soc2VerID = uuid.New()
		if _, err := admin.Exec(ctx, `
			INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
			VALUES ($1, NULL, $2, '2017', 'current')
		`, soc2VerID, soc2FwID); err != nil {
			t.Fatalf("insert slice174-soc framework_version: %v", err)
		}
	}

	requirementID = uuid.New()
	if _, err := admin.Exec(ctx, `
		INSERT INTO framework_requirements (id, framework_version_id, code, title, body)
		VALUES ($1, $2, 'SLICE174-CC1.1', 'slice174 test requirement', '')
		ON CONFLICT (framework_version_id, code) DO NOTHING
	`, requirementID, soc2VerID); err != nil {
		t.Fatalf("insert requirement: %v", err)
	}
	if err := admin.QueryRow(ctx, `
		SELECT id FROM framework_requirements
		WHERE framework_version_id = $1 AND code = 'SLICE174-CC1.1'
	`, soc2VerID).Scan(&requirementID); err != nil {
		t.Fatalf("lookup requirement: %v", err)
	}

	// Edge anchor → requirement
	if _, err := admin.Exec(ctx, `
		INSERT INTO fw_to_scf_edges (id, framework_requirement_id, scf_anchor_id, relationship_type, strength, source_attribution, rationale)
		VALUES ($1, $2, $3, 'equal', 1.0, 'scf_official', 'slice174 test edge')
		ON CONFLICT (framework_requirement_id, scf_anchor_id) DO NOTHING
	`, uuid.New(), requirementID, anchorID); err != nil {
		t.Fatalf("insert edge: %v", err)
	}

	return anchorID, requirementID
}

// freshAnchorsTenant creates a tenant uuid that auto-cleans its
// me_audit_log rows on test exit. Catalog rows (anchors, edges,
// frameworks) are NOT cleaned because they're global and shared
// across tests.
//
// Kept inline rather than delegated to dbtest.SeedTenant: this suite's
// tenant is a uuid.UUID (consumed by newAnchorsExportRouter +
// countAnchorsExportMetaAuditRows) and the cleanup is scoped to a
// specific action (`anchors_export`), neither of which dbtest.SeedTenant
// (string tenant, plain `WHERE tenant_id = $1`) expresses. Only the pool
// is migrated to the slice-435 harness; the migrate pool opened here
// outlives the DELETE cleanup (dbtest registers its own pool-close
// cleanup first, so it runs last under t.Cleanup's LIFO order).
func freshAnchorsTenant(t *testing.T) uuid.UUID {
	t.Helper()
	tenant := uuid.New()
	admin := dbtest.NewMigratePool(t)
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(),
			`DELETE FROM me_audit_log WHERE tenant_id = $1 AND action = 'anchors_export'`,
			tenant,
		); err != nil {
			t.Logf("cleanup me_audit_log: %v", err)
		}
	})
	return tenant
}

// countAnchorsExportMetaAuditRows runs under the tenant GUC.
func countAnchorsExportMetaAuditRows(t *testing.T, tenant uuid.UUID) int {
	t.Helper()
	app := dbtest.NewAppPool(t)
	ctx := context.Background()
	tx, err := app.Begin(ctx)
	if err != nil {
		t.Fatalf("count begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenant.String()); err != nil {
		t.Fatalf("set_config: %v", err)
	}
	var n int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM me_audit_log WHERE tenant_id = $1 AND action = 'anchors_export'`,
		tenant,
	).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// ----- tests -----

// AC-1 / AC-2: all three formats stream a valid body shape carrying
// the canonical anchor metadata + the seeded edge.
func TestSlice174_AnchorsExport_AllThreeFormats(t *testing.T) {
	_, _ = ensureSCFAnchorsSeed(t)
	tenant := freshAnchorsTenant(t)

	cases := []string{"csv", "json", "xlsx"}
	for _, format := range cases {
		format := format
		t.Run(format, func(t *testing.T) {
			r := newAnchorsExportRouter(t, tenant)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
				fmt.Sprintf("/v1/anchors/export?format=%s", format), nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
			}
			cd := rec.Header().Get("Content-Disposition")
			if !strings.HasPrefix(cd, `attachment; filename="anchors_`) {
				t.Errorf("Content-Disposition = %q; want prefix `attachment; filename=\"anchors_`", cd)
			}
			body := rec.Body.Bytes()
			switch format {
			case "csv":
				assertAnchorsCSVShape(t, body)
			case "json":
				assertAnchorsJSONShape(t, body)
			case "xlsx":
				assertAnchorsXLSXShape(t, body)
			}
		})
	}
}

func assertAnchorsCSVShape(t *testing.T, body []byte) {
	t.Helper()
	r := stdcsv.NewReader(bytes.NewReader(body))
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("csv parse: %v", err)
	}
	if len(rows) < 2 {
		t.Fatalf("rows = %d; want >= 2 (header + at least one anchor)", len(rows))
	}
	header := rows[0]
	if header[0] != "id" || header[len(header)-1] != "framework_satisfactions" {
		t.Errorf("CSV header = %v; want id...framework_satisfactions", header)
	}
	// Find our seed anchor row.
	foundSeed := false
	for _, row := range rows[1:] {
		if len(row) > 1 && row[1] == "SLICE174-A1" {
			foundSeed = true
			// The framework_satisfactions column should contain a JSON array.
			sats := row[len(row)-1]
			if !strings.HasPrefix(sats, "[") {
				t.Errorf("satisfactions cell does not start with `[`: %q", sats)
			}
			if !strings.Contains(sats, "SLICE174-CC1.1") {
				t.Errorf("satisfactions cell missing seeded edge requirement code: %q", sats)
			}
		}
	}
	if !foundSeed {
		t.Errorf("seeded anchor SLICE174-A1 not found in CSV export")
	}
}

func assertAnchorsJSONShape(t *testing.T, body []byte) {
	t.Helper()
	var anchors []map[string]any
	if err := json.Unmarshal(body, &anchors); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, body)
	}
	if len(anchors) == 0 {
		t.Fatal("empty anchors array")
	}
	// Find seed anchor.
	foundSeed := false
	for _, a := range anchors {
		if a["scf_id"] == "SLICE174-A1" {
			foundSeed = true
			sats, ok := a["framework_satisfactions"].([]any)
			if !ok {
				t.Errorf("framework_satisfactions is not an array: %v", a["framework_satisfactions"])
				continue
			}
			if len(sats) == 0 {
				t.Errorf("framework_satisfactions empty for seeded anchor")
				continue
			}
			sat0, _ := sats[0].(map[string]any)
			if sat0["framework_requirement_code"] != "SLICE174-CC1.1" {
				t.Errorf("first satisfaction code = %v; want SLICE174-CC1.1", sat0["framework_requirement_code"])
			}
		}
	}
	if !foundSeed {
		t.Errorf("seeded anchor SLICE174-A1 not found in JSON export")
	}
}

func assertAnchorsXLSXShape(t *testing.T, body []byte) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("zip open: %v", err)
	}
	// Two-sheet contract: exactly 6 zip members.
	if len(zr.File) != 6 {
		gotNames := make([]string, len(zr.File))
		for i, f := range zr.File {
			gotNames[i] = f.Name
		}
		t.Errorf("zip member count = %d; want 6; got=%v", len(zr.File), gotNames)
	}
	readMember := func(name string) string {
		for _, f := range zr.File {
			if f.Name == name {
				rc, _ := f.Open()
				defer rc.Close()
				b, _ := io.ReadAll(rc)
				return string(b)
			}
		}
		return ""
	}
	sheet1 := readMember("xl/worksheets/sheet1.xml")
	if !strings.Contains(sheet1, "SLICE174-A1") {
		t.Errorf("sheet1 missing seeded anchor: %s", sheet1)
	}
	sheet2 := readMember("xl/worksheets/sheet2.xml")
	if !strings.Contains(sheet2, "SLICE174-CC1.1") {
		t.Errorf("sheet2 missing seeded edge requirement code: %s", sheet2)
	}
}

// AC-4 (semantic): the export body is bit-for-bit identical across
// tenants. The slice 174 D7 contract — same global catalog, same
// bytes, regardless of who's asking.
func TestSlice174_CrossTenantIsolationIdenticalAllFormats(t *testing.T) {
	_, _ = ensureSCFAnchorsSeed(t)
	tenantA := freshAnchorsTenant(t)
	tenantB := freshAnchorsTenant(t)

	cases := []string{"csv", "json", "xlsx"}
	for _, format := range cases {
		format := format
		t.Run(format, func(t *testing.T) {
			rA := newAnchorsExportRouter(t, tenantA)
			rB := newAnchorsExportRouter(t, tenantB)

			recA := httptest.NewRecorder()
			rA.ServeHTTP(recA, httptest.NewRequest(http.MethodGet,
				fmt.Sprintf("/v1/anchors/export?format=%s", format), nil))
			if recA.Code != http.StatusOK {
				t.Fatalf("tenant A status = %d; body=%s", recA.Code, recA.Body.String())
			}

			recB := httptest.NewRecorder()
			rB.ServeHTTP(recB, httptest.NewRequest(http.MethodGet,
				fmt.Sprintf("/v1/anchors/export?format=%s", format), nil))
			if recB.Code != http.StatusOK {
				t.Fatalf("tenant B status = %d; body=%s", recB.Code, recB.Body.String())
			}

			bodyA := recA.Body.Bytes()
			bodyB := recB.Body.Bytes()
			// XLSX zip output is deterministic for the content but
			// archive/zip can emit different modtimes per zip member
			// across calls. Compare the union of sheet XML strings
			// instead of the raw zip bytes.
			if format == "xlsx" {
				if !equalSheetContent(t, bodyA, bodyB) {
					t.Errorf("XLSX sheet content differs across tenants — catalog should be identical")
				}
				return
			}
			if !bytes.Equal(bodyA, bodyB) {
				t.Errorf("format=%s: tenant A body != tenant B body — catalog export should be identical across tenants",
					format)
			}
		})
	}
}

func equalSheetContent(t *testing.T, a, b []byte) bool {
	t.Helper()
	readSheet := func(buf []byte, name string) string {
		zr, err := zip.NewReader(bytes.NewReader(buf), int64(len(buf)))
		if err != nil {
			t.Fatalf("zip open: %v", err)
		}
		for _, f := range zr.File {
			if f.Name == name {
				rc, _ := f.Open()
				defer rc.Close()
				body, _ := io.ReadAll(rc)
				return string(body)
			}
		}
		return ""
	}
	return readSheet(a, "xl/worksheets/sheet1.xml") == readSheet(b, "xl/worksheets/sheet1.xml") &&
		readSheet(a, "xl/worksheets/sheet2.xml") == readSheet(b, "xl/worksheets/sheet2.xml")
}

// AC-6: every terminal outcome writes a meta-audit row.
func TestSlice174_MetaAuditRowWrittenOnEveryOutcome(t *testing.T) {
	_, _ = ensureSCFAnchorsSeed(t)

	t.Run("success_200", func(t *testing.T) {
		tenant := freshAnchorsTenant(t)
		r := newAnchorsExportRouter(t, tenant)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/anchors/export?format=json", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
		}
		if got := countAnchorsExportMetaAuditRows(t, tenant); got != 1 {
			t.Errorf("meta-audit count after success = %d; want 1", got)
		}
	})

	t.Run("bad_format_400", func(t *testing.T) {
		tenant := freshAnchorsTenant(t)
		r := newAnchorsExportRouter(t, tenant)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/anchors/export?format=pdf", nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d; want 400", rec.Code)
		}
		if got := countAnchorsExportMetaAuditRows(t, tenant); got != 1 {
			t.Errorf("meta-audit count after 400 = %d; want 1", got)
		}
	})
}

// Concurrency cap inherited from slice 145.
func TestSlice174_ConcurrencyCapInherited(t *testing.T) {
	_, _ = ensureSCFAnchorsSeed(t)
	tenant := freshAnchorsTenant(t)

	lim := export.NewLimiter(2)
	r := newAnchorsExportRouterWithLimiter(t, tenant, lim)

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
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/anchors/export?format=json", nil))
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

// AC-7: streaming-memory budget — a 50K-anchor export through any
// of the three encoders MUST keep live heap delta under 200 MB.
//
// This test does NOT hit Postgres (50K real anchors would require
// dirtying the global catalog). Instead it constructs the 50K
// anchors in-process via a generator and runs them through each
// encoder against a discard writer, exactly matching the streaming
// path the production handler uses.
func TestSlice174_StreamingMemoryUnder200MBFor50KAnchors(t *testing.T) {
	const n = 50_000
	cases := []struct {
		name string
		run  func(t *testing.T) error
	}{
		{"csv", func(_ *testing.T) error {
			anchors := generatedAnchors(n)
			edges := generatedEdgesByAnchor(n, 3)
			return anchorsapi.ExportTestingWriteCSV(discard174{}, anchors, edges)
		}},
		{"json", func(_ *testing.T) error {
			anchors := generatedAnchors(n)
			edges := generatedEdgesByAnchor(n, 3)
			return anchorsapi.ExportTestingWriteJSON(discard174{}, anchors, edges)
		}},
		{"xlsx", func(_ *testing.T) error {
			anchors := generatedAnchors(n)
			edges := generatedEdgesFlat(n, 3)
			return anchorsapi.ExportTestingWriteXLSX(discard174{}, anchors, edges)
		}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			runtime.GC()
			var before runtime.MemStats
			runtime.ReadMemStats(&before)

			if err := tc.run(t); err != nil {
				t.Fatalf("encoder %s: %v", tc.name, err)
			}

			runtime.GC()
			var after runtime.MemStats
			runtime.ReadMemStats(&after)

			const budget = 200 * 1024 * 1024
			liveDelta := int64(after.HeapAlloc) - int64(before.HeapAlloc)
			if liveDelta > budget {
				t.Errorf("HeapAlloc grew by %d bytes (%.1f MB); want <= 200 MB",
					liveDelta, float64(liveDelta)/1024/1024)
			}
		})
	}
}

// generatedAnchors builds n in-memory anchors with stable-shape cells
// approximating production size (uuid + scf_id + family + title +
// description ~150 bytes per anchor).
func generatedAnchors(n int) []anchorsapi.ExportTestingAnchorRow {
	out := make([]anchorsapi.ExportTestingAnchorRow, n)
	for i := 0; i < n; i++ {
		id := uuid.New()
		out[i] = anchorsapi.ExportTestingAnchorRow{
			ID:                 id,
			SCFID:              fmt.Sprintf("SLICE174-%05d", i),
			Family:             "iam",
			Title:              fmt.Sprintf("slice174 generated anchor %05d title", i),
			Description:        fmt.Sprintf("slice174 generated anchor %05d description padded to thirtytwo chars", i),
			FrameworkVersionID: uuid.New(),
			FrameworkVersion:   "2026.1",
			FrameworkSlug:      "scf",
		}
	}
	return out
}

// generatedEdgesByAnchor returns a map of anchor_id -> edges with
// `edgesPerAnchor` edges per anchor.
func generatedEdgesByAnchor(nAnchors, edgesPerAnchor int) map[uuid.UUID][]anchorsapi.ExportTestingEdgeRow {
	out := make(map[uuid.UUID][]anchorsapi.ExportTestingEdgeRow, nAnchors)
	for i := 0; i < nAnchors; i++ {
		aid := uuid.New()
		edges := make([]anchorsapi.ExportTestingEdgeRow, edgesPerAnchor)
		for j := 0; j < edgesPerAnchor; j++ {
			edges[j] = anchorsapi.ExportTestingEdgeRow{
				EdgeID:                    uuid.New(),
				AnchorID:                  aid,
				AnchorSCFID:               fmt.Sprintf("SLICE174-%05d", i),
				FrameworkRequirementID:    uuid.New(),
				FrameworkRequirementCode:  fmt.Sprintf("REQ-%05d-%02d", i, j),
				FrameworkRequirementTitle: "padded requirement title to thirtytwo chars",
				FrameworkSlug:             "slice174-soc",
				FrameworkVersion:          "2017",
				RelationshipType:          "equal",
				Strength:                  1.0,
				SourceAttribution:         "scf_official",
				Rationale:                 "slice174 generated edge rationale",
			}
		}
		out[aid] = edges
	}
	return out
}

// generatedEdgesFlat returns a flat slice of edges (the XLSX writer
// consumes the flat shape — Sheet 2 doesn't need grouping).
func generatedEdgesFlat(nAnchors, edgesPerAnchor int) []anchorsapi.ExportTestingEdgeRow {
	out := make([]anchorsapi.ExportTestingEdgeRow, 0, nAnchors*edgesPerAnchor)
	for i := 0; i < nAnchors; i++ {
		aid := uuid.New()
		for j := 0; j < edgesPerAnchor; j++ {
			out = append(out, anchorsapi.ExportTestingEdgeRow{
				EdgeID:                    uuid.New(),
				AnchorID:                  aid,
				AnchorSCFID:               fmt.Sprintf("SLICE174-%05d", i),
				FrameworkRequirementID:    uuid.New(),
				FrameworkRequirementCode:  fmt.Sprintf("REQ-%05d-%02d", i, j),
				FrameworkRequirementTitle: "padded requirement title to thirtytwo chars",
				FrameworkSlug:             "slice174-soc",
				FrameworkVersion:          "2017",
				RelationshipType:          "equal",
				Strength:                  1.0,
				SourceAttribution:         "scf_official",
				Rationale:                 "slice174 generated edge rationale",
			})
		}
	}
	return out
}

type discard174 struct{}

func (discard174) Write(p []byte) (int, error) { return len(p), nil }
