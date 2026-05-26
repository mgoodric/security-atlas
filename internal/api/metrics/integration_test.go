//go:build integration

// Slice 293 — integration coverage for the metrics HTTP API. Real
// Postgres + the assembled platform router so the tests exercise the
// full request path: tenancy middleware, RLS, the catalog + observation
// + target queries the unit-only suite cannot reach.
//
// Load-bearing functions exercised here (the post-auth DB branches that
// handlers_test.go can't unit-test):
//
//   - ListCatalog — happy path (seeded catalog row visible to every
//     authenticated tenant), unfiltered + level filter + category filter
//   - GetCatalog — happy path, not-found (404), parents/children populated
//   - GetCascade — happy path on a seeded cascade edge, depth-truncation
//     header path, default-level branch (no ?level), depth > MaxCascadeDepth
//   - ListObservations — happy path (a manual input triggers a
//     replicated observation; the list returns it), since/until/limit
//     branches
//   - CreateInput — happy path through the runInTx tx-lifecycle: seeded
//     `manual_input` catalog row, valid numeric body, dimensions
//     defaulted to {}, observed_at provided, observation row created
//     by the metric_inputs replicate trigger; the wrong-strategy 409
//     conflict branch; the metric-not-found 404 branch
//   - GetTarget — no-target-set 404 path and (after UpsertTarget seeds
//     it) the success path
//   - UpsertTarget — insert-then-update flow exercising the upsert
//     branch, every accepted direction, all three thresholds set vs
//     unset, owner present vs absent
//
// Run with:
//
//	go test -tags=integration -race ./internal/api/metrics/...
//
// Requires DATABASE_URL_APP (atlas_app role) and DATABASE_URL (admin role).

package metrics_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
)

// ----- harness -----

func appDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL_APP")
	if v == "" {
		t.Skip("DATABASE_URL_APP not set; skipping slice 293 integration test")
	}
	return v
}

func adminDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL")
	if v == "" {
		t.Skip("DATABASE_URL not set; skipping slice 293 integration test")
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

// freshTenant returns a brand-new tenant UUID and registers cleanup of
// every row the slice 293 tests can introduce.
func freshTenant(t *testing.T, admin *pgxpool.Pool) uuid.UUID {
	t.Helper()
	tenant := uuid.New()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM metric_inputs       WHERE tenant_id = $1`,
			`DELETE FROM metric_observations WHERE tenant_id = $1`,
			`DELETE FROM metric_targets      WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

// seedCatalog inserts the catalog rows + cascade edge the slice 293
// integration tests require, idempotently (every row is platform-shared
// `tenant_id NULL`). Inserts run through the BYPASSRLS admin role since
// atlas_app cannot mutate global catalog rows (slice 076 policy).
func seedCatalog(t *testing.T, admin *pgxpool.Pool, boardID, programID string) {
	t.Helper()
	ctx := context.Background()
	rows := []struct {
		id, level, strategy string
		evaluator           any // *string or nil
	}{
		{boardID, "board", "manual_input", nil},
		{programID, "program", "manual_input", nil},
	}
	for _, r := range rows {
		if _, err := admin.Exec(ctx, `
			INSERT INTO metrics_catalog (
				id, tenant_id, level, category, name, description, unit,
				cadence, compute_strategy, compute_evaluator,
				source_slices, notes
			) VALUES (
				$1, NULL, $2, 'test', $1, 'slice 293 fixture', 'percent',
				'weekly', $3, $4, ARRAY[]::TEXT[], ''
			)
			ON CONFLICT (id) DO NOTHING
		`, r.id, r.level, r.strategy, r.evaluator); err != nil {
			t.Fatalf("seed catalog %q: %v", r.id, err)
		}
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO metric_cascade_edges (parent_id, child_id, weight, notes)
		VALUES ($1, $2, 1.0, 'slice 293 fixture edge')
		ON CONFLICT (parent_id, child_id) DO NOTHING
	`, boardID, programID); err != nil {
		t.Fatalf("seed cascade edge: %v", err)
	}
	t.Cleanup(func() {
		// Cascade edges are platform-shared; remove the test edge but
		// leave the catalog rows in place (other parallel tests in the
		// same DB may use them, and they're ON CONFLICT DO NOTHING).
		for _, stmt := range []string{
			`DELETE FROM metric_cascade_edges WHERE parent_id = $1 AND child_id = $2`,
		} {
			if _, err := admin.Exec(context.Background(), stmt, boardID, programID); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
}

// testEnv bundles the running server with admin + owner bearers. The
// admin bearer unlocks CreateInput + UpsertTarget; the owner bearer
// pins the 403 admin-gate branches.
type testEnv struct {
	server      *httptest.Server
	adminBearer string
	ownerBearer string
}

func testServer(t *testing.T, app *pgxpool.Pool, tenant uuid.UUID) testEnv {
	t.Helper()
	srv := api.New(api.Config{})
	srv.AttachDB(app)

	// CreateInput parses cred.UserID as a UUID (it lands in the
	// EnteredByUserID column). The default testjwt.AdminFor sets
	// Subject = "test-admin:<tenant>", which is NOT a UUID — so we
	// override Subject with a real UUID for these tests.
	adminClaims := testjwt.AdminFor(tenant)
	adminClaims.Subject = uuid.NewString()
	admin := srv.IssueTestJWT(t, adminClaims)

	ownerClaims := testjwt.OwnerFor(tenant, []string{"control_owner"})
	ownerClaims.Subject = uuid.NewString()
	owner := srv.IssueTestJWT(t, ownerClaims)

	h := srv.HTTPHandlerForTests()
	if h == nil {
		t.Fatal("HTTPHandlerForTests returned nil — DB pool not attached")
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return testEnv{
		server:      ts,
		adminBearer: admin,
		ownerBearer: owner,
	}
}

// ----- request helpers -----

func do(t *testing.T, method, url, bearer, contentType, body string) *http.Response {
	t.Helper()
	var br *bytes.Reader
	if body != "" {
		br = bytes.NewReader([]byte(body))
	}
	var req *http.Request
	var err error
	if br != nil {
		req, err = http.NewRequest(method, url, br)
	} else {
		req, err = http.NewRequest(method, url, nil)
	}
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

func decodeMap(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

// ----- tests -----

func TestIntegration_ListCatalog_ReturnsSeededRows(t *testing.T) {
	app := openPool(t, appDSN(t))
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)

	boardID := "ms.slice293.board." + uuid.NewString()[:8]
	programID := "ms.slice293.program." + uuid.NewString()[:8]
	seedCatalog(t, admin, boardID, programID)

	env := testServer(t, app, tenant)
	resp := do(t, http.MethodGet, env.server.URL+"/v1/metrics", env.adminBearer, "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ListCatalog: status %d", resp.StatusCode)
	}
	body := decodeMap(t, resp)
	if body["count"] == nil {
		t.Fatal("expected count field")
	}

	// With ?level=board the page must include OUR board row.
	resp = do(t, http.MethodGet, env.server.URL+"/v1/metrics?level=board", env.adminBearer, "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ListCatalog?level=board: status %d", resp.StatusCode)
	}
	body = decodeMap(t, resp)
	metrics, ok := body["metrics"].([]any)
	if !ok {
		t.Fatalf("expected metrics array; got %+v", body)
	}
	if !containsID(metrics, boardID) {
		t.Fatalf("expected level=board response to include %q; got %+v", boardID, metrics)
	}

	// ?category=test narrows further.
	resp = do(t, http.MethodGet, env.server.URL+"/v1/metrics?category=test", env.adminBearer, "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ListCatalog?category=test: status %d", resp.StatusCode)
	}
}

func TestIntegration_GetCatalog_HappyPathAndNotFound(t *testing.T) {
	app := openPool(t, appDSN(t))
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)

	boardID := "ms.slice293.board." + uuid.NewString()[:8]
	programID := "ms.slice293.program." + uuid.NewString()[:8]
	seedCatalog(t, admin, boardID, programID)

	env := testServer(t, app, tenant)

	// Happy path: GET the board metric, expect children = [program].
	resp := do(t, http.MethodGet, env.server.URL+"/v1/metrics/"+boardID, env.adminBearer, "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GetCatalog: status %d", resp.StatusCode)
	}
	body := decodeMap(t, resp)
	children, _ := body["children"].([]any)
	if !containsIDDirect(children, programID) {
		t.Fatalf("expected children to include %q; got %+v", programID, children)
	}

	// Not found.
	resp = do(t, http.MethodGet, env.server.URL+"/v1/metrics/does-not-exist", env.adminBearer, "", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404; got %d", resp.StatusCode)
	}
}

func TestIntegration_GetCascade_HappyAndTruncation(t *testing.T) {
	app := openPool(t, appDSN(t))
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)

	boardID := "ms.slice293.board." + uuid.NewString()[:8]
	programID := "ms.slice293.program." + uuid.NewString()[:8]
	seedCatalog(t, admin, boardID, programID)

	env := testServer(t, app, tenant)

	// Default level (no ?level => "board").
	resp := do(t, http.MethodGet, env.server.URL+"/v1/metrics/cascade", env.adminBearer, "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GetCascade default level: status %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Cascade-Truncated") == "true" {
		t.Fatal("default depth (3) should NOT mark truncated")
	}

	// Explicit level + depth.
	resp = do(t, http.MethodGet, env.server.URL+"/v1/metrics/cascade?level=board&depth=2", env.adminBearer, "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GetCascade level=board: status %d", resp.StatusCode)
	}

	// Depth > MaxCascadeDepth should set the truncated header.
	resp = do(t, http.MethodGet, env.server.URL+"/v1/metrics/cascade?level=board&depth=99", env.adminBearer, "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GetCascade depth=99: status %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Cascade-Truncated") != "true" {
		t.Fatalf("expected X-Cascade-Truncated=true with depth=99")
	}

	// Garbage depth falls back to default (no error, no truncation).
	resp = do(t, http.MethodGet, env.server.URL+"/v1/metrics/cascade?level=board&depth=garbage", env.adminBearer, "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GetCascade depth=garbage: status %d", resp.StatusCode)
	}
}

func TestIntegration_CreateInput_HappyAndWrongStrategy(t *testing.T) {
	app := openPool(t, appDSN(t))
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)

	boardID := "ms.slice293.board." + uuid.NewString()[:8]
	programID := "ms.slice293.program." + uuid.NewString()[:8]
	seedCatalog(t, admin, boardID, programID)

	// Also seed a 'computed' catalog row so we can drive the 409 path —
	// the manual_input replicate trigger only fires for manual rows, and
	// the handler enforces the strategy check before that.
	computedID := "ms.slice293.computed." + uuid.NewString()[:8]
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO metrics_catalog (
			id, tenant_id, level, category, name, description, unit,
			cadence, compute_strategy, compute_evaluator,
			source_slices, notes
		) VALUES (
			$1, NULL, 'team', 'test', $1, 'slice 293 computed fixture',
			'percent', 'daily', 'computed', 'closed_findings_30d',
			ARRAY[]::TEXT[], ''
		)
		ON CONFLICT (id) DO NOTHING
	`, computedID); err != nil {
		t.Fatalf("seed computed catalog: %v", err)
	}

	env := testServer(t, app, tenant)

	now := time.Now().UTC().Format(time.RFC3339)
	body := fmt.Sprintf(`{"numeric_value": 0.93, "observed_at": %q, "dimensions": {"env":"prod"}, "notes": "slice 293 manual"}`, now)

	// Happy path on a manual_input row.
	resp := do(t, http.MethodPost,
		env.server.URL+"/v1/metrics/"+boardID+"/inputs",
		env.adminBearer, "application/json", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("CreateInput happy: status %d; want 201", resp.StatusCode)
	}
	out := decodeMap(t, resp)
	if out["metric_id"] != boardID {
		t.Fatalf("metric_id mismatch: %+v", out)
	}

	// Default-time path: omit observed_at + dimensions; let the handler
	// stamp now() and default dimensions to {}.
	defaultsBody := `{"numeric_value": 0.50}`
	resp = do(t, http.MethodPost,
		env.server.URL+"/v1/metrics/"+boardID+"/inputs",
		env.adminBearer, "application/json", defaultsBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("CreateInput defaults: status %d (body=%v)", resp.StatusCode, decodeMap(t, resp))
	}

	// Wrong strategy => 409.
	resp = do(t, http.MethodPost,
		env.server.URL+"/v1/metrics/"+computedID+"/inputs",
		env.adminBearer, "application/json", body)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("CreateInput on computed: status %d; want 409", resp.StatusCode)
	}

	// Metric does not exist => 404.
	resp = do(t, http.MethodPost,
		env.server.URL+"/v1/metrics/does-not-exist/inputs",
		env.adminBearer, "application/json", body)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("CreateInput unknown: status %d; want 404", resp.StatusCode)
	}

	// Non-admin owner => 403 (pre-DB rejection — pins the admin gate
	// through the live middleware path).
	resp = do(t, http.MethodPost,
		env.server.URL+"/v1/metrics/"+boardID+"/inputs",
		env.ownerBearer, "application/json", body)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("CreateInput as owner: status %d; want 403", resp.StatusCode)
	}
}

func TestIntegration_ListObservations_ReflectsManualInput(t *testing.T) {
	app := openPool(t, appDSN(t))
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)

	boardID := "ms.slice293.board." + uuid.NewString()[:8]
	programID := "ms.slice293.program." + uuid.NewString()[:8]
	seedCatalog(t, admin, boardID, programID)

	env := testServer(t, app, tenant)

	// Seed one manual input (the replicate trigger writes the matching
	// observation row).
	inputBody := `{"numeric_value": 0.81, "notes": "slice 293 observation seed"}`
	resp := do(t, http.MethodPost,
		env.server.URL+"/v1/metrics/"+boardID+"/inputs",
		env.adminBearer, "application/json", inputBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed input: status %d", resp.StatusCode)
	}
	_ = decodeMap(t, resp)

	// Now list observations: count should be >= 1.
	resp = do(t, http.MethodGet,
		env.server.URL+"/v1/metrics/"+boardID+"/observations",
		env.adminBearer, "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ListObservations: status %d", resp.StatusCode)
	}
	body := decodeMap(t, resp)
	count, _ := body["count"].(float64)
	if count < 1 {
		t.Fatalf("expected count >= 1 after manual input; got %v body=%+v", count, body)
	}

	// since + until + limit branches — narrow window covering the seed,
	// then a non-covering window proving the since/until parse worked.
	now := time.Now().UTC()
	since := now.Add(-1 * time.Hour).Format(time.RFC3339)
	until := now.Add(1 * time.Hour).Format(time.RFC3339)
	resp = do(t, http.MethodGet,
		env.server.URL+"/v1/metrics/"+boardID+"/observations?since="+since+"&until="+until+"&limit=10",
		env.adminBearer, "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ListObservations with bounds: status %d", resp.StatusCode)
	}

	// Non-covering window (year 1900 -> 1901) returns 0 rows.
	resp = do(t, http.MethodGet,
		env.server.URL+"/v1/metrics/"+boardID+"/observations?since=1900-01-01T00:00:00Z&until=1901-01-01T00:00:00Z",
		env.adminBearer, "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ListObservations non-covering: status %d", resp.StatusCode)
	}
	body = decodeMap(t, resp)
	if c, _ := body["count"].(float64); c != 0 {
		t.Fatalf("expected zero count for 1900 window; got %v", c)
	}
}

func TestIntegration_Target_UpsertAndRead(t *testing.T) {
	app := openPool(t, appDSN(t))
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)

	boardID := "ms.slice293.board." + uuid.NewString()[:8]
	programID := "ms.slice293.program." + uuid.NewString()[:8]
	seedCatalog(t, admin, boardID, programID)

	env := testServer(t, app, tenant)

	// GetTarget before any upsert: 404.
	resp := do(t, http.MethodGet,
		env.server.URL+"/v1/metrics/"+boardID+"/target",
		env.adminBearer, "", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GetTarget pre-upsert: status %d; want 404", resp.StatusCode)
	}

	// UpsertTarget with all three thresholds + owner.
	owner := uuid.NewString()
	upsertBody := fmt.Sprintf(`{
		"target_value": 0.95,
		"warning_threshold": 0.85,
		"critical_threshold": 0.70,
		"direction": "higher_is_better",
		"owner_user_id": %q,
		"notes": "slice 293 target"
	}`, owner)
	resp = do(t, http.MethodPut,
		env.server.URL+"/v1/metrics/"+boardID+"/target",
		env.adminBearer, "application/json", upsertBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("UpsertTarget initial: status %d (body=%+v)", resp.StatusCode, decodeMap(t, resp))
	}
	out := decodeMap(t, resp)
	if out["direction"] != "higher_is_better" {
		t.Fatalf("direction round-trip: %+v", out)
	}

	// Read it back.
	resp = do(t, http.MethodGet,
		env.server.URL+"/v1/metrics/"+boardID+"/target",
		env.adminBearer, "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GetTarget post-upsert: status %d", resp.StatusCode)
	}
	out = decodeMap(t, resp)
	if out["target_value"] == nil {
		t.Fatalf("expected target_value populated; got %+v", out)
	}
	if out["owner_user_id"] != owner {
		t.Fatalf("owner round-trip: got %v; want %v", out["owner_user_id"], owner)
	}

	// Update — verify upsert branch (UPDATE path), thresholds nil.
	updateBody := `{"direction": "lower_is_better"}`
	resp = do(t, http.MethodPut,
		env.server.URL+"/v1/metrics/"+boardID+"/target",
		env.adminBearer, "application/json", updateBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("UpsertTarget update: status %d", resp.StatusCode)
	}
	out = decodeMap(t, resp)
	if out["direction"] != "lower_is_better" {
		t.Fatalf("direction not updated: %+v", out)
	}

	// target_is_better — third direction branch.
	resp = do(t, http.MethodPut,
		env.server.URL+"/v1/metrics/"+boardID+"/target",
		env.adminBearer, "application/json", `{"direction":"target_is_better","target_value":0.5}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("UpsertTarget target_is_better: status %d", resp.StatusCode)
	}

	// Non-admin owner cannot upsert => 403.
	resp = do(t, http.MethodPut,
		env.server.URL+"/v1/metrics/"+boardID+"/target",
		env.ownerBearer, "application/json", updateBody)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("UpsertTarget as owner: status %d; want 403", resp.StatusCode)
	}
}

// ----- helpers -----

func containsID(metrics []any, want string) bool {
	for _, m := range metrics {
		obj, ok := m.(map[string]any)
		if !ok {
			continue
		}
		if obj["id"] == want {
			return true
		}
	}
	return false
}

func containsIDDirect(rows []any, want string) bool {
	// rows are metricWire objects directly (children / parents lists).
	for _, r := range rows {
		obj, ok := r.(map[string]any)
		if !ok {
			continue
		}
		if obj["id"] == want {
			return true
		}
	}
	return false
}
