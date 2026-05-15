//go:build integration

// Slice 016 — integration tests for the evidence-freshness + control-drift
// HTTP API. Real Postgres + the assembled platform router so the tests
// exercise the full request path (tenancy middleware, RLS, the freshness /
// drift Stores). The DB is never mocked.
//
// Run with:
//
//	go test -tags=integration -race ./internal/api/freshnessdrift/...
//
// Requires DATABASE_URL_APP (atlas_app role) and DATABASE_URL (admin role).
//
// Coverage:
//
//	AC-1  GET /v1/evidence/freshness?bucket=class returns the class distribution
//	AC-2  a stale record is flagged in the read API but still queryable
//	AC-3  GET /v1/controls/drift?since=7d returns pass->fail flips with delta
//	AC-6  the evidence ledger row is never deleted by the freshness read path
//	RLS   cross-tenant isolation on the read-model endpoints
//	      malformed ?since= / ?bucket= -> 400; missing bearer -> 401

package freshnessdrift_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/drift"
	"github.com/mgoodric/security-atlas/internal/freshness"
	"github.com/mgoodric/security-atlas/internal/tenancy"
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

func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM evidence_freshness WHERE tenant_id = $1`,
			`DELETE FROM control_drift_snapshots WHERE tenant_id = $1`,
			`DELETE FROM control_evaluations WHERE tenant_id = $1`,
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

func ctxFor(t *testing.T, tenant string) context.Context {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return ctx
}

func seedControl(t *testing.T, admin *pgxpool.Pool, tenant, freshnessClass string) uuid.UUID {
	t.Helper()
	ctrlID := uuid.New()
	var fc *string
	if freshnessClass != "" {
		fc = &freshnessClass
	}
	bundleID := "test-bundle-016api-" + ctrlID.String()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (
			id, tenant_id, title, control_family, implementation_type,
			bundle_id, freshness_class, evidence_queries, applicability_expr
		)
		VALUES ($1, $2, 'slice 016 api test control', 'AAA', 'automated',
		        $3, $4, '[]'::jsonb, 'true')
	`, ctrlID, tenant, bundleID, fc); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return ctrlID
}

func seedEvidence(t *testing.T, admin *pgxpool.Pool, tenant string, ctrlID uuid.UUID, observedAt time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	controlRef := ctrlID.String()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO evidence_records (
			id, tenant_id, control_id, observed_at, ingested_at,
			provenance, result, payload, hash, control_ref
		)
		VALUES ($1, $2, $3, $4, now(), '{}'::jsonb, 'pass', '{}'::jsonb, $5, $6)
	`, id, tenant, ctrlID, observedAt, "hash-016api-"+id.String()[:8], controlRef); err != nil {
		t.Fatalf("seed evidence: %v", err)
	}
	return id
}

func seedEvaluation(t *testing.T, admin *pgxpool.Pool, tenant string, ctrlID uuid.UUID, result, freshnessStatus string, evaluatedAt time.Time) {
	t.Helper()
	id := uuid.New()
	runID := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO control_evaluations (
			id, tenant_id, control_id, scope_cell_id, eval_run_id,
			evaluated_at, result, freshness_status,
			evidence_count_in_window, trigger
		)
		VALUES ($1, $2, $3, NULL, $4, $5, $6, $7, 1, 'manual')
	`, id, tenant, ctrlID, runID, evaluatedAt, result, freshnessStatus); err != nil {
		t.Fatalf("seed evaluation: %v", err)
	}
}

func seedSnapshot(t *testing.T, admin *pgxpool.Pool, tenant string, day time.Time, passing []uuid.UUID) {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO control_drift_snapshots (
			id, tenant_id, snapshot_date, controls_passing,
			passing_control_ids, trigger
		)
		VALUES ($1, $2, $3, $4, $5, 'scheduled')
	`, id, tenant, day, len(passing), passing); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}
}

func dayOf(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func countEvidenceRecords(t *testing.T, admin *pgxpool.Pool, tenant string) int {
	t.Helper()
	var n int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM evidence_records WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
		t.Fatalf("count evidence: %v", err)
	}
	return n
}

// testEnv bundles the running server with the bearer token bound to the
// tenant, plus the app-pool Stores the test uses to populate the read models
// before exercising the read endpoints.
type testEnv struct {
	server    *httptest.Server
	bearer    string
	freshness *freshness.Store
	drift     *drift.Store
}

func testServer(t *testing.T, app *pgxpool.Pool, tenant string) testEnv {
	t.Helper()
	srv := api.New(api.Config{})
	srv.AttachDB(app)

	_, bearer, err := srv.IssueBootstrapOwnerCredential(tenant, []string{"owner"})
	if err != nil {
		t.Fatalf("IssueBootstrapOwnerCredential: %v", err)
	}

	h := srv.HTTPHandlerForTests()
	if h == nil {
		t.Fatal("HTTPHandlerForTests returned nil — DB pool not attached")
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return testEnv{
		server:    ts,
		bearer:    bearer,
		freshness: freshness.NewStore(app),
		drift:     drift.NewStore(app),
	}
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

// ===== AC-1: GET /v1/evidence/freshness?bucket=class returns the
// distribution by freshness_class =====

func TestFreshness_ReturnsClassDistribution(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	// A `weekly` control with fresh evidence + a `daily` control with stale
	// (100d old) evidence.
	weeklyCtrl := seedControl(t, admin, tenant, "weekly")
	seedEvidence(t, admin, tenant, weeklyCtrl, time.Now().UTC().Add(-2*24*time.Hour))
	dailyCtrl := seedControl(t, admin, tenant, "daily")
	seedEvidence(t, admin, tenant, dailyCtrl, time.Now().UTC().Add(-100*24*time.Hour))

	// Populate the read model.
	if _, err := env.freshness.Refresh(ctxFor(t, tenant)); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	resp, body := get(t, env, "/v1/evidence/freshness?bucket=class")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET freshness: status %d, want 200", resp.StatusCode)
	}
	if body["bucket"] != "class" {
		t.Errorf("bucket = %v, want class", body["bucket"])
	}
	buckets, ok := body["buckets"].([]any)
	if !ok || len(buckets) != 2 {
		t.Fatalf("AC-1: expected 2 class buckets, got %v", body["buckets"])
	}
	// Find each class bucket and assert its fresh/stale counts.
	seen := map[string]map[string]any{}
	for _, b := range buckets {
		m := b.(map[string]any)
		seen[m["freshness_class"].(string)] = m
	}
	weekly, ok := seen["weekly"]
	if !ok {
		t.Fatal("AC-1: weekly bucket missing")
	}
	if weekly["fresh"].(float64) != 1 || weekly["stale"].(float64) != 0 {
		t.Errorf("AC-1: weekly bucket fresh/stale = %v/%v, want 1/0", weekly["fresh"], weekly["stale"])
	}
	daily, ok := seen["daily"]
	if !ok {
		t.Fatal("AC-1: daily bucket missing")
	}
	if daily["fresh"].(float64) != 0 || daily["stale"].(float64) != 1 {
		t.Errorf("AC-1: daily bucket fresh/stale = %v/%v, want 0/1", daily["fresh"], daily["stale"])
	}
}

// ===== AC-1: omitting ?bucket= still returns the class distribution =====

func TestFreshness_DefaultBucketIsClass(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	ctrlID := seedControl(t, admin, tenant, "monthly")
	seedEvidence(t, admin, tenant, ctrlID, time.Now().UTC().Add(-1*24*time.Hour))
	if _, err := env.freshness.Refresh(ctxFor(t, tenant)); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	resp, body := get(t, env, "/v1/evidence/freshness")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET freshness (no bucket): status %d, want 200", resp.StatusCode)
	}
	if body["bucket"] != "class" {
		t.Errorf("default bucket = %v, want class", body["bucket"])
	}
}

// ===== AC-1: an unsupported ?bucket= value is rejected 400 =====

func TestFreshness_UnsupportedBucketIs400(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	resp, _ := get(t, env, "/v1/evidence/freshness?bucket=control")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("AC-1: ?bucket=control = %d, want 400", resp.StatusCode)
	}
}

// ===== AC-2 / AC-6: a stale record is flagged in the read API (counted in
// the stale total) but the ledger row is NEVER deleted =====

func TestFreshness_StaleFlaggedButLedgerRecordPreserved(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	// A realtime control (24h max-age) with evidence 30 days old — stale.
	ctrlID := seedControl(t, admin, tenant, "realtime")
	seedEvidence(t, admin, tenant, ctrlID, time.Now().UTC().Add(-30*24*time.Hour))

	beforeCount := countEvidenceRecords(t, admin, tenant)
	if _, err := env.freshness.Refresh(ctxFor(t, tenant)); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	resp, body := get(t, env, "/v1/evidence/freshness?bucket=class")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET freshness: status %d", resp.StatusCode)
	}
	// AC-2: the stale record is flagged — total_stale must count it.
	if body["total_stale"].(float64) != 1 {
		t.Errorf("AC-2: total_stale = %v, want 1 — the stale control must be flagged", body["total_stale"])
	}

	// AC-6: the evidence ledger row must still be there, queryable for
	// audit replay. The read path flags; it never deletes.
	afterCount := countEvidenceRecords(t, admin, tenant)
	if afterCount != beforeCount {
		t.Errorf("AC-6: evidence record count went %d -> %d — the freshness read path must NEVER delete from the ledger",
			beforeCount, afterCount)
	}
}

// ===== AC-3: GET /v1/controls/drift?since=7d returns the controls that
// flipped pass->fail with the signed delta =====

func TestDrift_ReturnsPassToFailFlipsWithDelta(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	now := time.Now().UTC()
	stableCtrl := seedControl(t, admin, tenant, "monthly")
	flippedCtrl := seedControl(t, admin, tenant, "monthly")

	// Yesterday: both controls were passing.
	seedSnapshot(t, admin, tenant, dayOf(now.AddDate(0, 0, -1)),
		[]uuid.UUID{stableCtrl, flippedCtrl})

	// Today: stable still pass+fresh; flipped went to fail.
	seedEvaluation(t, admin, tenant, stableCtrl, "pass", "fresh", now.Add(-1*time.Hour))
	seedEvaluation(t, admin, tenant, flippedCtrl, "fail", "fresh", now.Add(-1*time.Hour))
	if _, err := env.drift.CaptureSnapshot(ctxFor(t, tenant), drift.TriggerScheduled); err != nil {
		t.Fatalf("CaptureSnapshot: %v", err)
	}

	resp, body := get(t, env, "/v1/controls/drift?since=7d")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET drift: status %d, want 200", resp.StatusCode)
	}
	// delta = passing(today=1) - passing(yesterday=2) = -1.
	if body["delta"].(float64) != -1 {
		t.Errorf("AC-3: delta = %v, want -1", body["delta"])
	}
	flips, ok := body["flipped_out"].([]any)
	if !ok || len(flips) != 1 {
		t.Fatalf("AC-3: expected 1 flipped-out control, got %v", body["flipped_out"])
	}
	flip := flips[0].(map[string]any)
	if flip["control_id"] != flippedCtrl.String() {
		t.Errorf("AC-3: flipped control_id = %v, want %s", flip["control_id"], flippedCtrl)
	}
	// AC-3: the flip row carries the last-passing date.
	if _, ok := flip["last_passing"]; !ok {
		t.Error("AC-3: flipped-out row missing last_passing date")
	}
	// AC-3: the flip row carries the current (no-longer-passing) result.
	if _, ok := flip["current_result"]; !ok {
		t.Error("AC-3: flipped-out row missing current_result")
	}
}

// ===== AC-3: ?since= defaults to 7d when omitted =====

func TestDrift_SinceDefaultsToSevenDays(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	resp, body := get(t, env, "/v1/controls/drift")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET drift (no since): status %d, want 200", resp.StatusCode)
	}
	// since + through are present and span ~7 days.
	since, sok := body["since"].(string)
	through, tok := body["through"].(string)
	if !sok || !tok {
		t.Fatalf("AC-3: since/through missing from response: %v", body)
	}
	sinceT, _ := time.Parse("2006-01-02", since)
	throughT, _ := time.Parse("2006-01-02", through)
	gap := throughT.Sub(sinceT).Hours() / 24
	if gap != 7 {
		t.Errorf("AC-3: default window span = %v days, want 7", gap)
	}
}

// ===== AC-3: a malformed ?since= is rejected 400 =====

func TestDrift_MalformedSinceIs400(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	for _, bad := range []string{"7", "abc", "7w", "-3d", "d"} {
		resp, _ := get(t, env, "/v1/controls/drift?since="+bad)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("AC-3: ?since=%q = %d, want 400", bad, resp.StatusCode)
		}
	}
}

// ===== RLS: cross-tenant isolation — tenant A's endpoints never see tenant
// B's read-model rows =====

func TestFreshnessDrift_CrossTenantIsolation(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)

	now := time.Now().UTC()
	// Tenant B has a freshness row and a drift snapshot.
	ctrlB := seedControl(t, admin, tenantB, "weekly")
	seedEvidence(t, admin, tenantB, ctrlB, now.Add(-1*24*time.Hour))
	driftB := drift.NewStore(app)
	freshB := freshness.NewStore(app)
	if _, err := freshB.Refresh(ctxFor(t, tenantB)); err != nil {
		t.Fatalf("Refresh B: %v", err)
	}
	seedEvaluation(t, admin, tenantB, ctrlB, "pass", "fresh", now.Add(-1*time.Hour))
	if _, err := driftB.CaptureSnapshot(ctxFor(t, tenantB), drift.TriggerScheduled); err != nil {
		t.Fatalf("CaptureSnapshot B: %v", err)
	}

	// Tenant A's server must see ZERO of tenant B's rows.
	envA := testServer(t, app, tenantA)

	respF, bodyF := get(t, envA, "/v1/evidence/freshness?bucket=class")
	if respF.StatusCode != http.StatusOK {
		t.Fatalf("GET freshness A: status %d", respF.StatusCode)
	}
	if bodyF["total"].(float64) != 0 {
		t.Errorf("RLS: tenant A's freshness total = %v, want 0 — saw tenant B's rows", bodyF["total"])
	}

	respD, bodyD := get(t, envA, "/v1/controls/drift?since=7d")
	if respD.StatusCode != http.StatusOK {
		t.Fatalf("GET drift A: status %d", respD.StatusCode)
	}
	// Tenant A has no snapshots -> delta 0, no flips.
	if bodyD["delta"].(float64) != 0 {
		t.Errorf("RLS: tenant A's drift delta = %v, want 0 — saw tenant B's snapshots", bodyD["delta"])
	}
}

// ===== auth: missing bearer -> 401 on both endpoints =====

func TestFreshnessDrift_MissingBearerIs401(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	for _, path := range []string{"/v1/evidence/freshness", "/v1/controls/drift"} {
		resp, err := http.Get(env.server.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("auth: missing bearer on %s = %d, want 401", path, resp.StatusCode)
		}
	}
}
