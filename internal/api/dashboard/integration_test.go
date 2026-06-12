//go:build integration

// Slice 066 — integration tests for the dashboard backend read endpoints.
// Real Postgres + the assembled platform router (or, for the 403 case, a
// minimally-wired router) so the tests exercise the full request path:
// tenancy middleware, RLS, the sqlc query layer.
//
// Run with:
//
//	go test -tags=integration -race ./internal/api/dashboard/...
//
// Requires DATABASE_URL_APP (atlas_app role) and DATABASE_URL (admin role).
//
// Coverage (>=6 tests, AC-7):
//
//	ISC-18  framework posture aggregates correctly across versions
//	ISC-19  activity feed paginates newest-first with a stable cursor
//	ISC-20  risks sort=residual,age orders correctly (in risks_sort_test.go)
//	ISC-21  upcoming rollup merges all four sources date-sorted
//	ISC-22  all four endpoints 403 a role without program-read access
//	ISC-23  all four endpoints are RLS-isolated across tenants
//	(plus)  input validation: bad cursor / limit / category -> 400

package dashboard_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/dashboard"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
)

// ----- harness -----

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
	t.Cleanup(func() { pool.Close() })
	return pool
}

// freshTenant returns a new tenant id and registers a cleanup that deletes
// every row this slice's tests can create under it. Catalog rows (frameworks,
// framework_versions, framework_requirements, scf_anchors, fw_to_scf_edges)
// are seeded per-test with unique ids and cleaned up by seedFramework's own
// cleanup so they do not leak across tests.
func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM control_evaluations WHERE tenant_id = $1`,
			`DELETE FROM evidence_freshness WHERE tenant_id = $1`,
			`DELETE FROM evidence_audit_log WHERE tenant_id = $1`,
			`DELETE FROM exceptions WHERE tenant_id = $1`,
			`DELETE FROM policy_acknowledgments WHERE tenant_id = $1`,
			`DELETE FROM vendors WHERE tenant_id = $1`,
			`DELETE FROM audit_periods WHERE tenant_id = $1`,
			`DELETE FROM risks WHERE tenant_id = $1`,
			`DELETE FROM policies WHERE tenant_id = $1`,
			`DELETE FROM controls WHERE tenant_id = $1`,
			`DELETE FROM users WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

// framework bundles the ids a seeded framework version exposes so the tests
// can assert against a known framework_id + version string.
type framework struct {
	frameworkID        uuid.UUID
	frameworkVersionID uuid.UUID
	version            string
}

// seedFramework inserts a global-catalog framework + one 'current' version +
// `reqCount` requirements + one SCF anchor per requirement + an 'equal' STRM
// edge from each requirement to its anchor. It returns the framework handle
// and the ordered anchor ids (one per requirement) so a test can anchor
// controls onto them. Registers a cleanup that deletes the catalog rows.
func seedFramework(t *testing.T, admin *pgxpool.Pool, slug, version string, reqCount int) (framework, []uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	fw := framework{
		frameworkID:        uuid.New(),
		frameworkVersionID: uuid.New(),
		version:            version,
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO frameworks (id, tenant_id, name, slug, issuer)
		VALUES ($1, NULL, $2, $3, 'test-issuer')
	`, fw.frameworkID, "Framework "+slug, slug+"-"+uuid.NewString()[:8]); err != nil {
		t.Fatalf("seed framework: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
		VALUES ($1, NULL, $2, $3, 'current')
	`, fw.frameworkVersionID, fw.frameworkID, version); err != nil {
		t.Fatalf("seed framework_version: %v", err)
	}
	anchors := make([]uuid.UUID, reqCount)
	for i := 0; i < reqCount; i++ {
		reqID := uuid.New()
		anchorID := uuid.New()
		edgeID := uuid.New()
		code := uuid.NewString()[:8]
		if _, err := admin.Exec(ctx, `
			INSERT INTO framework_requirements (id, framework_version_id, code, title)
			VALUES ($1, $2, $3, $4)
		`, reqID, fw.frameworkVersionID, code, "Requirement "+code); err != nil {
			t.Fatalf("seed framework_requirement: %v", err)
		}
		if _, err := admin.Exec(ctx, `
			INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title)
			VALUES ($1, $2, $3, 'AAA', $4)
		`, anchorID, fw.frameworkVersionID, "SCF-"+code, "Anchor "+code); err != nil {
			t.Fatalf("seed scf_anchor: %v", err)
		}
		if _, err := admin.Exec(ctx, `
			INSERT INTO fw_to_scf_edges (
				id, framework_requirement_id, scf_anchor_id,
				relationship_type, strength, source_attribution
			)
			VALUES ($1, $2, $3, 'equal', 1.0, 'scf_official')
		`, edgeID, reqID, anchorID); err != nil {
			t.Fatalf("seed fw_to_scf_edge: %v", err)
		}
		anchors[i] = anchorID
	}
	t.Cleanup(func() {
		ctx := context.Background()
		// audit_periods has an ON DELETE RESTRICT FK to framework_versions,
		// so any audit period a test seeded against this version must go
		// first. Cleanups run LIFO and seedFramework is called after
		// freshTenant, so this runs before freshTenant's audit_periods
		// delete — clear them here too. fw_to_scf_edges +
		// framework_requirements + scf_anchors cascade from
		// framework_versions; framework_versions cascades from frameworks.
		if _, err := admin.Exec(ctx,
			`DELETE FROM audit_periods WHERE framework_version_id = $1`,
			fw.frameworkVersionID); err != nil {
			t.Logf("cleanup audit_periods for framework: %v", err)
		}
		if _, err := admin.Exec(ctx,
			`DELETE FROM frameworks WHERE id = $1`, fw.frameworkID); err != nil {
			t.Logf("cleanup framework: %v", err)
		}
	})
	return fw, anchors
}

// seedControlOnAnchor inserts one active control for `tenant` anchored on
// `anchorID`. Returns the control id.
func seedControlOnAnchor(t *testing.T, admin *pgxpool.Pool, tenant string, anchorID uuid.UUID) uuid.UUID {
	t.Helper()
	ctrlID := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (
			id, tenant_id, title, control_family, implementation_type,
			bundle_id, evidence_queries, applicability_expr, scf_anchor_id
		)
		VALUES ($1, $2, 'slice 066 test control', 'AAA', 'automated',
		        $3, '[]'::jsonb, 'true', $4)
	`, ctrlID, tenant, "test-bundle-066-"+ctrlID.String(), anchorID); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return ctrlID
}

// seedFreshness inserts an evidence_freshness row for a control.
func seedFreshness(t *testing.T, admin *pgxpool.Pool, tenant string, ctrlID uuid.UUID, isStale bool) {
	t.Helper()
	observed := time.Now().UTC().Add(-24 * time.Hour)
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO evidence_freshness (
			id, tenant_id, control_id, freshness_class,
			latest_observed_at, valid_until, is_stale, evidence_count
		)
		VALUES ($1, $2, $3, 'monthly', $4, $5, $6, 1)
	`, uuid.New(), tenant, ctrlID, observed,
		observed.Add(30*24*time.Hour), isStale); err != nil {
		t.Fatalf("seed evidence_freshness: %v", err)
	}
}

// seedEvaluation inserts one control_evaluations row.
func seedEvaluation(t *testing.T, admin *pgxpool.Pool, tenant string, ctrlID uuid.UUID, result string, evaluatedAt time.Time) {
	t.Helper()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO control_evaluations (
			id, tenant_id, control_id, eval_run_id, evaluated_at,
			result, freshness_status, evidence_count_in_window, trigger
		)
		VALUES ($1, $2, $3, $4, $5, $6, 'fresh', 1, 'manual')
	`, uuid.New(), tenant, ctrlID, uuid.New(), evaluatedAt, result); err != nil {
		t.Fatalf("seed control_evaluation: %v", err)
	}
}

// seedIngestEvent inserts one evidence_audit_log row — the activity feed's
// source. receivedAt drives the newest-first ordering.
func seedIngestEvent(t *testing.T, admin *pgxpool.Pool, tenant, decision string, receivedAt time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO evidence_audit_log (
			id, tenant_id, credential_id, decision, evidence_kind,
			record_id, received_at
		)
		VALUES ($1, $2, 'cred-test-066', $3, 'sast.scan_result', $4, $5)
	`, id, tenant, decision, uuid.New(), receivedAt); err != nil {
		t.Fatalf("seed evidence_audit_log: %v", err)
	}
	return id
}

// seedException inserts one exceptions row. status='active' makes it appear
// in the upcoming rollup; expiresAt is its due_date.
func seedException(t *testing.T, admin *pgxpool.Pool, tenant string, ctrlID uuid.UUID, expiresAt time.Time) {
	t.Helper()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO exceptions (
			id, tenant_id, control_id, scope_cell_predicate,
			justification, requested_by, requested_at, expires_at, status
		)
		VALUES ($1, $2, $3, '{}'::jsonb,
		        'slice 066 test exception', 'tester', now(), $4, 'active')
	`, uuid.New(), tenant, ctrlID, expiresAt); err != nil {
		t.Fatalf("seed exception: %v", err)
	}
}

// seedAuditPeriod inserts one open audit_periods row. periodEnd is its
// due_date.
func seedAuditPeriod(t *testing.T, admin *pgxpool.Pool, tenant string, fvID uuid.UUID, periodEnd time.Time) {
	t.Helper()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO audit_periods (
			id, tenant_id, name, framework_version_id,
			period_start, period_end, status, created_by
		)
		VALUES ($1, $2, 'slice 066 test period', $3,
		        $4, $5, 'open', 'tester')
	`, uuid.New(), tenant, fvID,
		periodEnd.Add(-90*24*time.Hour), periodEnd); err != nil {
		t.Fatalf("seed audit_period: %v", err)
	}
}

// seedVendor inserts one vendors row with a last_review_date so it appears
// in the upcoming rollup (review due = last_review_date + cadence).
func seedVendor(t *testing.T, admin *pgxpool.Pool, tenant, name string, lastReview time.Time, cadence string) {
	t.Helper()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO vendors (
			id, tenant_id, name, criticality, review_cadence, last_review_date
		)
		VALUES ($1, $2, $3, 'medium', $4, $5)
	`, uuid.New(), tenant, name, cadence, lastReview); err != nil {
		t.Fatalf("seed vendor: %v", err)
	}
}

// seedUserAndAck inserts a user + one policy + one policy_acknowledgments
// row. The ack's due_date in the rollup is acknowledged_at + 365 days.
func seedUserAndAck(t *testing.T, admin *pgxpool.Pool, tenant string, ackedAt time.Time) {
	t.Helper()
	userID := uuid.New()
	policyID := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO users (id, tenant_id, email, display_name, status)
		VALUES ($1, $2, $3, 'Slice 066 Tester', 'active')
	`, userID, tenant, "slice066-"+uuid.NewString()[:8]+"@test.example"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO policies (
			id, tenant_id, title, version, body_md, status,
			owner_role, approver_role, created_by, effective_date
		)
		VALUES ($1, $2, 'Slice 066 Policy', '1.0.0', '# policy', 'published',
		        'grc_engineer', 'grc_engineer', 'tester', now())
	`, policyID, tenant); err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO policy_acknowledgments (
			id, tenant_id, policy_id, policy_version_id, user_id,
			acknowledged_at, ack_token
		)
		VALUES ($1, $2, $3, $3, $4, $5, $6)
	`, uuid.New(), tenant, policyID, userID, ackedAt,
		"tok-"+uuid.NewString()[:12]); err != nil {
		t.Fatalf("seed policy_acknowledgment: %v", err)
	}
}

// testEnv bundles the running server with a bearer token bound to the tenant.
type testEnv struct {
	server *httptest.Server
	bearer string
}

// testServer assembles the full platform router with an owner credential —
// owner credentials carry OwnerRoles, so requireProgramRead admits them.
func testServer(t *testing.T, app *pgxpool.Pool, tenant string) testEnv {
	t.Helper()
	srv := api.New(api.Config{})
	srv.AttachDB(app)
	// Slice 197: JWT bearer via slice 190 path (owner roles).
	bearer := srv.IssueTestJWT(t, testjwt.OwnerFor(uuid.MustParse(tenant), []string{"control_owner"}))
	h := srv.HTTPHandlerForTests()
	if h == nil {
		t.Fatal("HTTPHandlerForTests returned nil — DB pool not attached")
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return testEnv{server: ts, bearer: bearer}
}

// get issues an authenticated GET and decodes the JSON body.
func get(t *testing.T, env testEnv, path string) (*http.Response, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, env.server.URL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+env.bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	_ = resp.Body.Close()
	return resp, body
}

// noRoleRouter wires the three dashboard routes behind a credential carrying
// NO program-read signal — no IsAdmin, no IsApprover, no OwnerRoles. That is
// the v1 representation of a viewer-only credential. It exercises the
// handler-level requireProgramRead guard without standing up OPA. The guard
// runs FIRST in every handler — before tenant resolution — so the 403 fires
// regardless of the tenant context.
func noRoleRouter(t *testing.T, app *pgxpool.Pool, tenant string) http.Handler {
	t.Helper()
	h := dashboard.New(dashboard.NewStore(app))
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				TenantID:   tenant,
				UserID:     "viewer-test",
				OwnerRoles: nil,
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Get("/v1/frameworks/posture", h.FrameworkPosture)
	r.Get("/v1/activity", h.Activity)
	r.Get("/v1/upcoming", h.Upcoming)
	return r
}

// ===== ISC-18: framework posture aggregates correctly across versions =====

func TestFrameworkPosture_AggregatesAcrossVersions(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	// Framework A: 4 requirements. Tenant covers 2 of them with active
	// controls -> coverage 50%. One covering control is fresh, one stale ->
	// freshness composite 50%.
	fwA, anchorsA := seedFramework(t, admin, "fwa", "2024", 4)
	ctrlA1 := seedControlOnAnchor(t, admin, tenant, anchorsA[0])
	ctrlA2 := seedControlOnAnchor(t, admin, tenant, anchorsA[1])
	seedFreshness(t, admin, tenant, ctrlA1, false) // fresh
	seedFreshness(t, admin, tenant, ctrlA2, true)  // stale
	// Both covering controls passed >90 days ago -> coverage then was also
	// 50% -> trend delta 0. (We seed a passing eval before the cutoff.)
	old := time.Now().UTC().Add(-120 * 24 * time.Hour)
	seedEvaluation(t, admin, tenant, ctrlA1, "pass", old)
	seedEvaluation(t, admin, tenant, ctrlA2, "pass", old)

	// Framework B: 2 requirements, tenant covers neither -> coverage 0%.
	fwB, _ := seedFramework(t, admin, "fwb", "2025", 2)

	resp, body := get(t, env, "/v1/frameworks/posture")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET posture: status %d, want 200", resp.StatusCode)
	}
	rows, _ := body["frameworks"].([]any)
	// Find each framework's row.
	byFW := map[string]map[string]any{}
	for _, r := range rows {
		m := r.(map[string]any)
		byFW[m["framework_id"].(string)] = m
	}
	rowA, okA := byFW[fwA.frameworkID.String()]
	if !okA {
		t.Fatalf("ISC-18: framework A posture row missing")
	}
	if rowA["framework_version"].(string) != "2024" {
		t.Fatalf("ISC-18: framework A version = %v, want 2024", rowA["framework_version"])
	}
	if cov := rowA["coverage_pct"].(float64); cov != 50.0 {
		t.Fatalf("ISC-18: framework A coverage_pct = %v, want 50.0", cov)
	}
	if fresh := rowA["freshness_composite"].(float64); fresh != 50.0 {
		t.Fatalf("ISC-18: framework A freshness_composite = %v, want 50.0", fresh)
	}
	if trend := rowA["trend_delta_90d"].(float64); trend != 0.0 {
		t.Fatalf("ISC-18: framework A trend_delta_90d = %v, want 0.0", trend)
	}
	rowB, okB := byFW[fwB.frameworkID.String()]
	if !okB {
		t.Fatalf("ISC-18: framework B posture row missing")
	}
	if cov := rowB["coverage_pct"].(float64); cov != 0.0 {
		t.Fatalf("ISC-18: framework B coverage_pct = %v, want 0.0", cov)
	}
}

// ISC-18 (trend arm): a control covered NOW but not passing 90 days ago
// produces a positive trend delta.

func TestFrameworkPosture_TrendReflectsGrowth(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	// Framework with 2 requirements. Tenant covers both NOW (coverage 100%).
	// But 90 days ago only one control was passing (coverage then 50%).
	fw, anchors := seedFramework(t, admin, "fwgrowth", "2024", 2)
	ctrl1 := seedControlOnAnchor(t, admin, tenant, anchors[0])
	ctrl2 := seedControlOnAnchor(t, admin, tenant, anchors[1])
	old := time.Now().UTC().Add(-120 * 24 * time.Hour)
	seedEvaluation(t, admin, tenant, ctrl1, "pass", old) // passing then
	seedEvaluation(t, admin, tenant, ctrl2, "fail", old) // NOT passing then

	_, body := get(t, env, "/v1/frameworks/posture")
	rows, _ := body["frameworks"].([]any)
	for _, r := range rows {
		m := r.(map[string]any)
		if m["framework_id"].(string) != fw.frameworkID.String() {
			continue
		}
		if cov := m["coverage_pct"].(float64); cov != 100.0 {
			t.Fatalf("trend: coverage_pct now = %v, want 100.0", cov)
		}
		// coverage now 100% - coverage then 50% = +50 points.
		if trend := m["trend_delta_90d"].(float64); trend != 50.0 {
			t.Fatalf("trend: trend_delta_90d = %v, want 50.0", trend)
		}
		return
	}
	t.Fatal("trend: framework posture row missing")
}

// ===== ISC-19: activity feed paginates newest-first with a stable cursor =====

func TestActivity_PaginatesNewestFirst(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	// Five ingest events at descending ages.
	for i := 0; i < 5; i++ {
		seedIngestEvent(t, admin, tenant, "accepted",
			time.Now().UTC().Add(-time.Duration(i+1)*time.Hour))
	}

	// Page 1: limit 2 -> 2 rows newest-first + a next_cursor.
	resp, body := get(t, env, "/v1/activity?limit=2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("page1: status %d, want 200", resp.StatusCode)
	}
	rows, _ := body["activity"].([]any)
	if len(rows) != 2 {
		t.Fatalf("ISC-19: page1 expected 2 rows, got %d", len(rows))
	}
	first := rows[0].(map[string]any)
	for _, field := range []string{"ts", "event_type", "actor", "resource_type", "resource_id", "summary"} {
		if _, ok := first[field]; !ok {
			t.Fatalf("ISC-19: activity row missing field %q", field)
		}
	}
	// Newest-first: row 0's ts is after row 1's.
	if rows[0].(map[string]any)["ts"].(string) <= rows[1].(map[string]any)["ts"].(string) {
		t.Fatalf("ISC-19: activity not newest-first")
	}
	// event_type is the view's projected 'evidence.' + decision.
	if et := first["event_type"].(string); et != "evidence.accepted" {
		t.Fatalf("ISC-19: event_type = %q, want evidence.accepted", et)
	}
	cursor, _ := body["next_cursor"].(string)
	if cursor == "" {
		t.Fatalf("ISC-19: page1 expected a non-empty next_cursor")
	}

	// Page 2: cursor -> next 2 rows, no overlap with page 1.
	page1IDs := map[string]bool{}
	for _, r := range rows {
		page1IDs[r.(map[string]any)["resource_id"].(string)] = true
	}
	resp, body = get(t, env, "/v1/activity?limit=2&cursor="+cursor)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("page2: status %d", resp.StatusCode)
	}
	rows, _ = body["activity"].([]any)
	if len(rows) != 2 {
		t.Fatalf("ISC-19: page2 expected 2 rows, got %d", len(rows))
	}
	for _, r := range rows {
		if page1IDs[r.(map[string]any)["resource_id"].(string)] {
			t.Fatalf("ISC-19: page2 row overlaps page1 — keyset pagination drifted")
		}
	}
}

// ===== ISC-21: upcoming rollup merges all four sources date-sorted =====

func TestUpcoming_MergesAllFourSourcesDateSorted(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	now := time.Now().UTC()
	fw, anchors := seedFramework(t, admin, "fwup", "2024", 1)
	ctrl := seedControlOnAnchor(t, admin, tenant, anchors[0])

	// One item per source, with deliberately interleaved due dates so a
	// correct merge interleaves the categories:
	//   exception     due now+10d
	//   policy_ack    due now+20d  (ack'd 345 days ago -> +365 = now+20d)
	//   vendor_review due now+30d  (reviewed today, but annual -> next year;
	//                               use a monthly cadence reviewed ~now-? )
	//   audit_period  due now+40d
	seedException(t, admin, tenant, ctrl, now.Add(10*24*time.Hour))
	seedUserAndAck(t, admin, tenant, now.Add(-345*24*time.Hour)) // due ~now+20d
	// monthly cadence, last reviewed ~now-1d -> due ~now+29d (within window)
	seedVendor(t, admin, tenant, "Vendor M", now.Add(-1*24*time.Hour), "monthly")
	seedAuditPeriod(t, admin, tenant, fw.frameworkVersionID, now.Add(40*24*time.Hour))

	resp, body := get(t, env, "/v1/upcoming")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET upcoming: status %d, want 200", resp.StatusCode)
	}
	rows, _ := body["upcoming"].([]any)
	if len(rows) != 4 {
		t.Fatalf("ISC-21: expected 4 merged rows, got %d", len(rows))
	}
	// Row shape.
	first := rows[0].(map[string]any)
	for _, field := range []string{"due_date", "category", "title", "resource_type", "resource_id"} {
		if _, ok := first[field]; !ok {
			t.Fatalf("ISC-21: upcoming row missing field %q", field)
		}
	}
	// All four categories present.
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.(map[string]any)["category"].(string)] = true
	}
	for _, want := range []string{"exception", "policy_ack", "vendor_review", "audit_period"} {
		if !seen[want] {
			t.Fatalf("ISC-21: category %q missing from merged rollup", want)
		}
	}
	// Date-sorted ascending.
	for i := 1; i < len(rows); i++ {
		prev := rows[i-1].(map[string]any)["due_date"].(string)
		cur := rows[i].(map[string]any)["due_date"].(string)
		if prev > cur {
			t.Fatalf("ISC-21: rollup not date-sorted ascending: %s > %s", prev, cur)
		}
	}
}

// ISC-21 (category filter arm): ?category= narrows to one source.

func TestUpcoming_CategoryFilter(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	now := time.Now().UTC()
	fw, anchors := seedFramework(t, admin, "fwcat", "2024", 1)
	ctrl := seedControlOnAnchor(t, admin, tenant, anchors[0])
	seedException(t, admin, tenant, ctrl, now.Add(10*24*time.Hour))
	seedAuditPeriod(t, admin, tenant, fw.frameworkVersionID, now.Add(40*24*time.Hour))

	resp, body := get(t, env, "/v1/upcoming?category=exception")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET upcoming?category=exception: status %d", resp.StatusCode)
	}
	rows, _ := body["upcoming"].([]any)
	if len(rows) != 1 {
		t.Fatalf("ISC-21 filter: expected 1 exception row, got %d", len(rows))
	}
	if cat := rows[0].(map[string]any)["category"].(string); cat != "exception" {
		t.Fatalf("ISC-21 filter: category = %q, want exception", cat)
	}
}

// TestUpcoming_ExceptionTitleUsesSCFCodeNotUUID is the slice 732 regression
// guard for the dashboard "Upcoming" panel: the expiring-exception row's
// title must resolve the control to its SCF code (here via the control's
// scf_anchor_id -> scf_anchors.scf_id linkage) + the control name, and must
// NOT contain the raw control UUID. Mirrors the calendar exception-label fix
// so the two upcoming-event surfaces read identically (slice 675 vocabulary).
func TestUpcoming_ExceptionTitleUsesSCFCodeNotUUID(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	now := time.Now().UTC()
	_, anchors := seedFramework(t, admin, "fwscf", "2024", 1)
	ctrl := seedControlOnAnchor(t, admin, tenant, anchors[0])
	seedException(t, admin, tenant, ctrl, now.Add(10*24*time.Hour))

	resp, body := get(t, env, "/v1/upcoming?category=exception")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET upcoming?category=exception: status %d", resp.StatusCode)
	}
	rows, _ := body["upcoming"].([]any)
	if len(rows) != 1 {
		t.Fatalf("expected 1 exception row, got %d", len(rows))
	}
	title := rows[0].(map[string]any)["title"].(string)

	if !strings.HasPrefix(title, "Exception on SCF-") {
		t.Errorf("exception title = %q; want it to start with the resolved SCF code", title)
	}
	if !strings.Contains(title, "slice 066 test control") {
		t.Errorf("exception title = %q; want it to contain the control name", title)
	}
	if strings.Contains(title, ctrl.String()) {
		t.Errorf("exception title leaked the raw control UUID: %q", title)
	}
}

// ===== ISC-22: all four endpoints 403 a role without program-read access =====

func TestAllEndpoints_ForbidNonProgramReadRole(t *testing.T) {
	app := openPool(t, appDSN(t))
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)
	r := noRoleRouter(t, app, tenant)

	paths := []string{
		"/v1/frameworks/posture",
		"/v1/activity",
		"/v1/upcoming",
	}
	for _, p := range paths {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("ISC-22: GET %s — status %d, want 403; body %s", p, rec.Code, rec.Body.String())
		}
	}
}

// ===== ISC-23: all four endpoints are RLS-isolated across tenants =====

func TestAllEndpoints_RLSIsolatedAcrossTenants(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))

	// Tenant A owns a covered control, an ingest event, an exception, and an
	// audit period. Tenant B's server must see NONE of it in the
	// tenant-scoped surfaces.
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)

	now := time.Now().UTC()
	fwA, anchorsA := seedFramework(t, admin, "fwrls", "2024", 2)
	ctrlA := seedControlOnAnchor(t, admin, tenantA, anchorsA[0])
	seedFreshness(t, admin, tenantA, ctrlA, false)
	seedIngestEvent(t, admin, tenantA, "accepted", now.Add(-1*time.Hour))
	seedException(t, admin, tenantA, ctrlA, now.Add(10*24*time.Hour))
	seedAuditPeriod(t, admin, tenantA, fwA.frameworkVersionID, now.Add(40*24*time.Hour))

	// Tenant B's bearer — RLS scopes every read to tenant B (which owns
	// nothing).
	envB := testServer(t, app, tenantB)

	// Activity + upcoming are wholly tenant-scoped: tenant B sees zero rows.
	_, actBody := get(t, envB, "/v1/activity")
	if rows, _ := actBody["activity"].([]any); len(rows) != 0 {
		t.Fatalf("ISC-23: /v1/activity leaked %d cross-tenant rows", len(rows))
	}
	_, upBody := get(t, envB, "/v1/upcoming")
	if rows, _ := upBody["upcoming"].([]any); len(rows) != 0 {
		t.Fatalf("ISC-23: /v1/upcoming leaked %d cross-tenant rows", len(rows))
	}
	// Framework posture: the framework + requirements are global catalog
	// (visible to any tenant), but the COVERAGE must be tenant B's — which
	// is zero, because tenant B has no controls anchored on fwA's anchors.
	_, postBody := get(t, envB, "/v1/frameworks/posture")
	rows, _ := postBody["frameworks"].([]any)
	for _, r := range rows {
		m := r.(map[string]any)
		if m["framework_id"].(string) != fwA.frameworkID.String() {
			continue
		}
		if cov := m["coverage_pct"].(float64); cov != 0.0 {
			t.Fatalf("ISC-23: /v1/frameworks/posture leaked tenant A coverage: %v, want 0.0", cov)
		}
		if fresh := m["freshness_composite"].(float64); fresh != 0.0 {
			t.Fatalf("ISC-23: /v1/frameworks/posture leaked tenant A freshness: %v", fresh)
		}
	}
}

// ===== input validation =====

func TestDashboard_RejectsBadInput(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	cases := []struct {
		path string
		want int
	}{
		{"/v1/activity?limit=999", http.StatusBadRequest},
		{"/v1/activity?limit=abc", http.StatusBadRequest},
		{"/v1/activity?cursor=@@@", http.StatusBadRequest},
		{"/v1/upcoming?limit=0", http.StatusBadRequest},
		{"/v1/upcoming?cursor=not-base64-!!", http.StatusBadRequest},
		{"/v1/upcoming?category=bogus", http.StatusBadRequest},
	}
	for _, c := range cases {
		resp, _ := get(t, env, c.path)
		if resp.StatusCode != c.want {
			t.Fatalf("GET %s — status %d, want %d", c.path, resp.StatusCode, c.want)
		}
	}
}

// ===== Slice 147 — empty-install AC-5 =====
//
// AC-5 (slice 147): "Empty-install integration test: dashboard loads
// cleanly with 0 frameworks + 0 activity events — no placeholders, no
// 500s." On a fresh tenant with no seeded rows, both dashboard endpoints
// MUST return 200 with the documented empty envelope (NOT 500, NOT a
// placeholder body). The frontend re-pointing in slice 147 depends on
// this — a placeholder-free panel renders its empty-state copy off the
// empty envelope.

func TestDashboard_EmptyTenant_FrameworkPostureReturnsEmptyEnvelope(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	resp, body := get(t, env, "/v1/frameworks/posture")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("AC-5: empty tenant GET /v1/frameworks/posture status %d, want 200", resp.StatusCode)
	}
	// AC-5 P0-DASH-2: empty install returns 200 with well-formed envelope,
	// NOT 500. The framework catalog is GLOBAL per canvas §3.5 (platform-
	// bundled, not tenant-scoped); only `controls` is tenant-scoped. A
	// fresh tenant correctly sees the bundled frameworks with no coverage
	// because no controls exist — so we assert ENVELOPE shape (presence
	// of the `frameworks` key + a numeric `count`), NOT row count.
	if _, ok := body["frameworks"].([]any); !ok {
		t.Fatalf("AC-5: response missing `frameworks` array; got body=%v", body)
	}
	if _, ok := body["count"].(float64); !ok {
		t.Fatalf("AC-5: response missing numeric `count`; got body=%v", body)
	}
}

func TestDashboard_EmptyTenant_ActivityReturnsEmptyEnvelope(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	resp, body := get(t, env, "/v1/activity")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("AC-5: empty tenant GET /v1/activity status %d, want 200", resp.StatusCode)
	}
	rows, _ := body["activity"].([]any)
	if len(rows) != 0 {
		t.Fatalf("AC-5: empty tenant returned %d activity rows, want 0", len(rows))
	}
	count, _ := body["count"].(float64)
	if count != 0 {
		t.Fatalf("AC-5: empty tenant count = %v, want 0", count)
	}
	// next_cursor MUST be the empty string on an empty result set so the
	// frontend's pagination affordance correctly hides itself.
	if nc, _ := body["next_cursor"].(string); nc != "" {
		t.Fatalf("AC-5: empty tenant next_cursor = %q, want \"\"", nc)
	}
}
