//go:build integration

// Slice 269 — integration tests for the dashboard snapshot export.
//
// AC coverage:
//
//	AC-1 / AC-2 / AC-3 / AC-4 / AC-5 → TestSlice269_HappyPathPerFormat
//	AC-6                              → TestSlice269_RoleGate403
//	AC-7                              → TestSlice269_MetaAuditRowWrittenOnEveryOutcome
//	AC-8 (migration)                  → exercised by every test (the
//	                                    `dashboard_export` insert would
//	                                    fail the CHECK without the
//	                                    migration applied)
//	AC-9                              → TestSlice269_CrossTenantIsolation
//	AC-10                             → TestSlice269_StreamingMemoryUnder200MBFor50KRows
//
// Requires DATABASE_URL (admin) + DATABASE_URL_APP (atlas_app) —
// runs under the same harness as the slice 175 history-export
// integration tests.

package dashboardexport_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/dashboard"
	"github.com/mgoodric/security-atlas/internal/api/dashboardexport"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/drift"
	"github.com/mgoodric/security-atlas/internal/freshness"
	"github.com/mgoodric/security-atlas/internal/risk"
)

// ----- router wiring -----

// newExportRouter wires the slice 269 endpoint under the same auth +
// tenancy middleware stack the production server uses. Mirrors the
// slice 175 newControlsHistoryExportRouter exactly: an outer
// middleware injects the credential onto the context; tenancymw
// lifts the tenant id from the credential onto the context;
// dashboardexport.Handler runs the export.
func newExportRouter(t *testing.T, tenantID uuid.UUID, isAdmin, isApprover bool) http.Handler {
	t.Helper()
	app := dbtest.NewAppPool(t)
	dashStore := dashboard.NewStore(app)
	riskStore := risk.NewStore(app)
	freshStore := freshness.NewStore(app)
	driftStore := drift.NewStore(app)

	src := dashboardexport.NewLivePanelSource(dashStore, riskStore, freshStore, driftStore)
	h := dashboardexport.NewHandler(app, src)

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID:         "test-dashboard-export-id",
				TenantID:   tenantID.String(),
				UserID:     "user-dashboard-export-test",
				IsAdmin:    isAdmin,
				IsApprover: isApprover,
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Get("/v1/dashboard/export", h.ExportDashboard)
	return r
}

// ----- tenant scaffolding -----

// freshExportTenant returns a new tenant id and registers a cleanup
// that deletes every row this slice's tests can create under it.
// Includes the `dashboard_export` meta-audit rows the handler writes.
func freshExportTenant(t *testing.T) uuid.UUID {
	t.Helper()
	tenant := uuid.New()
	// Carve-out (742 drain batch 11): this helper returns uuid.UUID (not the
	// string dbtest.SeedTenant yields) and the cleanup scopes one row to
	// action = 'dashboard_export' on me_audit_log — neither shape SeedTenant
	// can express — so it stays inline; only its pool is re-routed onto the
	// shared dbtest.NewMigratePool. The pool is acquired here (not inside the
	// closure) so dbtest registers its own t.Cleanup before teardown runs.
	admin := dbtest.NewMigratePool(t)
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM me_audit_log WHERE tenant_id = $1 AND action = 'dashboard_export'`,
			`DELETE FROM evidence_audit_log WHERE tenant_id = $1`,
			`DELETE FROM evidence_freshness WHERE tenant_id = $1`,
			`DELETE FROM exceptions WHERE tenant_id = $1`,
			`DELETE FROM vendors WHERE tenant_id = $1`,
			`DELETE FROM audit_periods WHERE tenant_id = $1`,
			`DELETE FROM control_evaluations WHERE tenant_id = $1`,
			`DELETE FROM risks WHERE tenant_id = $1`,
			`DELETE FROM controls WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

// seedRisk inserts one mitigate-treatment risk so the risks panel
// has a row. Returns its id + title so callers can assert presence.
func seedRisk(t *testing.T, tenant uuid.UUID, titleHint string) (uuid.UUID, string) {
	t.Helper()
	admin := dbtest.NewMigratePool(t)
	ctx := context.Background()
	id := uuid.New()
	title := titleHint + "-" + uuid.NewString()[:8]
	if _, err := admin.Exec(ctx, `
		INSERT INTO risks (
			id, tenant_id, title, description, category, methodology,
			inherent_score, treatment, treatment_owner, residual_score
		)
		VALUES (
			$1, $2, $3, 'slice 269 seeded risk',
			'operational', 'nist_800_30',
			'{"likelihood":3,"impact":4,"value":12}'::jsonb,
			'mitigate', 'platform-eng',
			'{"likelihood":2,"impact":3,"value":6}'::jsonb
		)
	`, id, tenant, title); err != nil {
		t.Fatalf("seed risk: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM risks WHERE id = $1`, id)
	})
	return id, title
}

// countDashboardExportMetaAuditRows counts me_audit_log rows with
// the slice 269 action under the given tenant's GUC.
func countDashboardExportMetaAuditRows(t *testing.T, tenant uuid.UUID) int {
	t.Helper()
	app := dbtest.NewAppPool(t)
	ctx := context.Background()
	tx, err := app.Begin(ctx)
	if err != nil {
		t.Fatalf("count begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		"SELECT set_config('app.current_tenant', $1, true)",
		tenant.String()); err != nil {
		t.Fatalf("set_config: %v", err)
	}
	var n int
	if err := tx.QueryRow(ctx,
		"SELECT count(*) FROM me_audit_log WHERE tenant_id = $1 AND action = 'dashboard_export'",
		tenant).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// ===== AC-1 / AC-2 / AC-3 / AC-4 / AC-5: happy path per format =====

func TestSlice269_HappyPathPerFormat(t *testing.T) {
	tenant := freshExportTenant(t)
	_, riskTitle := seedRisk(t, tenant, "tenant-A-export")

	cases := []struct {
		format       string
		contentType  string
		bodyContains []string
	}{
		{
			format:      "json",
			contentType: "application/json",
			// JSON body carries the snapshot envelope + the seeded
			// risk title verbatim.
			bodyContains: []string{
				`"snapshot_at"`,
				`"panels"`,
				`"framework_posture"`,
				`"risks"`,
				`"freshness"`,
				`"drift"`,
				`"upcoming"`,
				`"activity"`,
				riskTitle,
			},
		},
		{
			format:      "csv",
			contentType: "application/zip",
		},
		{
			format:      "xlsx",
			contentType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.format, func(t *testing.T) {
			r := newExportRouter(t, tenant, true, false)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet,
				"/v1/dashboard/export?format="+tc.format, nil)
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Content-Type"); got != tc.contentType {
				t.Errorf("Content-Type = %q; want %q", got, tc.contentType)
			}
			body := rec.Body.Bytes()
			if len(body) == 0 {
				t.Fatalf("body empty")
			}
			// JSON: assert substring presence.
			for _, want := range tc.bodyContains {
				if !strings.Contains(string(body), want) {
					t.Errorf("body missing %q; full:\n%s", want, string(body))
				}
			}
			// CSV: re-open as zip, assert at least one panel file.
			if tc.format == "csv" {
				zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
				if err != nil {
					t.Fatalf("zip.NewReader: %v", err)
				}
				if !zipHasMember(zr, "risks.csv") {
					t.Errorf("zip missing risks.csv")
				}
				risksBody := readZipMember(t, zr, "risks.csv")
				if !strings.Contains(risksBody, riskTitle) {
					t.Errorf("risks.csv missing seeded title %q; got:\n%s",
						riskTitle, risksBody)
				}
			}
			// XLSX: re-open as zip, assert envelope + 6 sheets.
			if tc.format == "xlsx" {
				zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
				if err != nil {
					t.Fatalf("zip.NewReader: %v", err)
				}
				for _, want := range []string{
					"[Content_Types].xml",
					"_rels/.rels",
					"xl/workbook.xml",
					"xl/_rels/workbook.xml.rels",
				} {
					if !zipHasMember(zr, want) {
						t.Errorf("xlsx missing envelope member %q", want)
					}
				}
				for i := 1; i <= 6; i++ {
					name := fmt.Sprintf("xl/worksheets/sheet%d.xml", i)
					if !zipHasMember(zr, name) {
						t.Errorf("xlsx missing %q", name)
					}
				}
			}
		})
	}
}

// ===== AC-6: role gate =====

// A role lacking both IsAdmin and IsApprover is denied 403.
func TestSlice269_RoleGate403(t *testing.T) {
	tenant := freshExportTenant(t)
	r := newExportRouter(t, tenant, false, false)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/v1/dashboard/export?format=json", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d; want 403; body=%s", rec.Code, rec.Body.String())
	}
}

// IsApprover alone (no IsAdmin) is admitted.
func TestSlice269_RoleGate_ApproverAdmitted(t *testing.T) {
	tenant := freshExportTenant(t)
	r := newExportRouter(t, tenant, false, true)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/v1/dashboard/export?format=json", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", rec.Code, rec.Body.String())
	}
}

// ===== AC-7: meta-audit row per terminal outcome =====

func TestSlice269_MetaAuditRowWrittenOnEveryOutcome(t *testing.T) {
	t.Run("success_200", func(t *testing.T) {
		tenant := freshExportTenant(t)
		r := newExportRouter(t, tenant, true, false)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
			"/v1/dashboard/export?format=json", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
		}
		if got := countDashboardExportMetaAuditRows(t, tenant); got != 1 {
			t.Errorf("meta-audit after success: %d; want 1", got)
		}
	})

	t.Run("bad_format_400", func(t *testing.T) {
		tenant := freshExportTenant(t)
		r := newExportRouter(t, tenant, true, false)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
			"/v1/dashboard/export?format=pdf", nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d; want 400", rec.Code)
		}
		if got := countDashboardExportMetaAuditRows(t, tenant); got != 1 {
			t.Errorf("meta-audit after bad_format: %d; want 1", got)
		}
	})

	t.Run("forbidden_403", func(t *testing.T) {
		tenant := freshExportTenant(t)
		r := newExportRouter(t, tenant, false, false)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
			"/v1/dashboard/export?format=csv", nil))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d; want 403", rec.Code)
		}
		if got := countDashboardExportMetaAuditRows(t, tenant); got != 1 {
			t.Errorf("meta-audit after 403: %d; want 1", got)
		}
	})
}

// ===== AC-9: cross-tenant isolation =====

// Tenant A's export MUST NOT include tenant B's data in any format.
// This is the merge-blocking evidence for the constitutional
// invariant #6 (RLS / tenancy) — slice 269's composition of six
// RLS-gated reads must preserve the boundary at every panel.
func TestSlice269_CrossTenantIsolation(t *testing.T) {
	tenantA := freshExportTenant(t)
	tenantB := freshExportTenant(t)

	// Seed a unique title in tenant B that MUST NOT appear in
	// tenant A's export.
	bTitleBase := "TENANT_B_DASHBOARD_LEAK_PROBE_" + uuid.NewString()[:8]
	_, _ = seedRisk(t, tenantA, "tenant-A-iso")
	_, bTitle := seedRisk(t, tenantB, bTitleBase)

	cases := []string{"json", "csv", "xlsx"}
	for _, format := range cases {
		format := format
		t.Run(format, func(t *testing.T) {
			r := newExportRouter(t, tenantA, true, false)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet,
				"/v1/dashboard/export?format="+format, nil)
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
			}
			searchable := extractSearchableText(t, format, rec.Body.Bytes())
			// CROSS-TENANT INVARIANT: tenant B's seeded probe MUST
			// NOT appear in tenant A's export.
			if strings.Contains(searchable, bTitle) {
				t.Errorf("CROSS-TENANT LEAK in format=%s: tenant B token %q present in tenant A export",
					format, bTitle)
			}
		})
	}
}

// ===== AC-10: streaming memory budget =====

// A 50K-row export through any of the three encoders MUST keep live
// heap delta under 200 MB. This test does NOT hit Postgres — at v1
// dashboard volumes the live snapshot never approaches 50K rows.
// The streaming-memory invariant is about the encoder pipeline (the
// CSV zip + XLSX multi-sheet streaming paths), so we drive the
// encoders directly with a synthetic 50K-row snapshot.
func TestSlice269_StreamingMemoryUnder200MBFor50KRows(t *testing.T) {
	snap := syntheticLargeSnapshot(50_000)

	cases := []struct {
		name   string
		encode func(io.Writer, dashboardexport.Snapshot) error
	}{
		{"json", dashboardexport.EncodeJSONForTesting},
		{"csv", dashboardexport.EncodeCSVZipForTesting},
		{"xlsx", dashboardexport.EncodeXLSXForTesting},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			runtime.GC()
			var before runtime.MemStats
			runtime.ReadMemStats(&before)

			if err := tc.encode(discardWriter{}, snap); err != nil {
				t.Fatalf("encode %s: %v", tc.name, err)
			}

			runtime.GC()
			var after runtime.MemStats
			runtime.ReadMemStats(&after)

			const budget = 200 * 1024 * 1024
			liveDelta := int64(after.HeapAlloc) - int64(before.HeapAlloc)
			if liveDelta > budget {
				t.Errorf("HeapAlloc grew by %d bytes (%.1f MB); want <= %d (200 MB)",
					liveDelta, float64(liveDelta)/1024/1024, budget)
			}
		})
	}
}

// syntheticLargeSnapshot constructs an in-memory snapshot whose
// total row count across the panels is `total`. Distributes the row
// budget across the six panels so each encoder pipeline runs the
// full multi-panel path.
func syntheticLargeSnapshot(total int) dashboardexport.Snapshot {
	per := total / 6
	out := dashboardexport.Snapshot{
		SnapshotAt: time.Now().UTC(),
	}
	for i := 0; i < per; i++ {
		out.Panels.FrameworkPosture = append(out.Panels.FrameworkPosture,
			dashboardexport.FrameworkPosturePanelRow{
				FrameworkID:        uuid.NewString(),
				FrameworkVersion:   "v" + fmt.Sprintf("%d", i%5+1),
				CoveragePct:        0.5 + float64(i%100)/200,
				FreshnessComposite: 0.8,
				TrendDelta90d:      0.02,
			})
		out.Panels.Risks = append(out.Panels.Risks,
			dashboardexport.RiskPanelRow{
				ID:            uuid.NewString(),
				Title:         fmt.Sprintf("synthetic risk %d", i),
				Treatment:     "mitigate",
				Category:      "operational",
				Methodology:   "nist_800_30",
				ResidualScore: `{"value":6}`,
				CreatedAt:     "2026-05-01T00:00:00Z",
			})
		out.Panels.Drift.FlippedOut = append(out.Panels.Drift.FlippedOut,
			dashboardexport.DriftRow{
				ControlID:     uuid.NewString(),
				LastPassing:   "2026-05-10",
				CurrentResult: "fail",
			})
		out.Panels.Upcoming = append(out.Panels.Upcoming,
			dashboardexport.UpcomingPanelRow{
				DueDate:      "2026-06-01T00:00:00Z",
				Category:     "exception",
				Title:        fmt.Sprintf("upcoming %d", i),
				ResourceType: "exception",
				ResourceID:   uuid.NewString(),
			})
		out.Panels.Activity = append(out.Panels.Activity,
			dashboardexport.ActivityPanelRow{
				TS:           "2026-05-23T10:00:00Z",
				EventType:    "evidence.ingest",
				Actor:        "cred-" + uuid.NewString()[:8],
				ResourceType: "evidence",
				ResourceID:   uuid.NewString(),
				Summary:      `{"decision":"accepted"}`,
			})
	}
	// Freshness has bucketed shape — a handful of buckets is the
	// realistic dashboard rendering.
	out.Panels.Freshness = dashboardexport.FreshnessPanel{
		Bucket: "class",
		Buckets: []dashboardexport.FreshnessClassBucket{
			{FreshnessClass: "daily", Total: per / 4, Fresh: per / 5, Stale: per / 20},
			{FreshnessClass: "weekly", Total: per / 4, Fresh: per / 5, Stale: per / 20},
			{FreshnessClass: "monthly", Total: per / 4, Fresh: per / 5, Stale: per / 20},
			{FreshnessClass: "quarterly", Total: per / 4, Fresh: per / 5, Stale: per / 20},
		},
		Total:      per,
		TotalStale: per / 5,
	}
	out.Panels.Drift.Since = "2026-05-17"
	out.Panels.Drift.Through = "2026-05-24"
	out.Panels.Drift.Delta = -per / 10
	out.Panels.Drift.FlippedOutCount = len(out.Panels.Drift.FlippedOut)
	return out
}

// ===== helpers =====

// extractSearchableText returns a string view of the export body
// suitable for substring assertions across formats. JSON is
// returned verbatim; CSV / XLSX are unzipped and member bodies
// concatenated.
func extractSearchableText(t *testing.T, format string, body []byte) string {
	t.Helper()
	switch format {
	case "json":
		return string(body)
	case "csv":
		zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			t.Fatalf("zip.NewReader (csv): %v", err)
		}
		return concatZipMembers(t, zr)
	case "xlsx":
		zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			t.Fatalf("zip.NewReader (xlsx): %v", err)
		}
		return concatZipMembers(t, zr)
	default:
		t.Fatalf("unknown format %q", format)
		return ""
	}
}

func concatZipMembers(t *testing.T, zr *zip.Reader) string {
	t.Helper()
	var b strings.Builder
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		b.WriteString(f.Name)
		b.WriteString("\n")
		b.Write(data)
		b.WriteString("\n")
	}
	return b.String()
}

func zipHasMember(zr *zip.Reader, name string) bool {
	for _, f := range zr.File {
		if f.Name == name {
			return true
		}
	}
	return false
}

func readZipMember(t *testing.T, zr *zip.Reader, name string) string {
	t.Helper()
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		defer rc.Close()
		data, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(data)
	}
	t.Fatalf("zip missing %s", name)
	return ""
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// ===== silence unused-import warnings on integration-build path =====

var (
	_ = json.Marshal
	_ = chi.NewRouter
)
