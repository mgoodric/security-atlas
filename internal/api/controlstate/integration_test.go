//go:build integration

// Slice 012 — integration tests for the control state evaluation HTTP API.
// Real Postgres + the assembled platform router so the tests exercise the
// full request path (tenancy middleware, RLS, the eval.Engine).
//
// Run with:
//
//	go test -tags=integration -race ./internal/api/controlstate/...
//
// Requires DATABASE_URL_APP (atlas_app role) and DATABASE_URL (admin role).
//
// Coverage:
//
//	ISC-32  GET /v1/controls/{id}/state returns result + counts + freshness
//	ISC-33  GET /v1/controls/{id}/state honors ?as-of=
//	ISC-34  GET /v1/controls/{id}/state honors ?scope=
//	ISC-35  GET /v1/controls/{id}/effectiveness returns the rolling pass rate
//	ISC-37  unknown control id -> 404; missing tenant context -> 401

package controlstate_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/scope"
)

// ----- harness -----

// Slice 435 / 742: the appDSN/adminDSN/openPool pool/DSN boilerplate this file
// used to re-derive now lives in the shared internal/dbtest harness (NewAppPool
// = RLS-enforcing atlas_app default; NewMigratePool = privileged BYPASSRLS for
// seeding + freshTenant cleanup; WithTenantCtx tags the tenant GUC).

func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin,
		"control_evaluations",
		"evidence_records",
		"scope_cells",
		"controls",
	)
}

func seedControl(t *testing.T, admin *pgxpool.Pool, tenant, implType, freshnessClass string) uuid.UUID {
	t.Helper()
	ctrlID := uuid.New()
	var fc *string
	if freshnessClass != "" {
		fc = &freshnessClass
	}
	bundleID := "test-bundle-012api-" + ctrlID.String()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (
			id, tenant_id, title, control_family, implementation_type,
			bundle_id, freshness_class, evidence_queries, applicability_expr
		)
		VALUES ($1, $2, 'slice 012 api test control', 'AAA', $3,
		        $4, $5, '[]'::jsonb, 'true')
	`, ctrlID, tenant, implType, bundleID, fc); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return ctrlID
}

func seedEvidence(t *testing.T, admin *pgxpool.Pool, tenant string, ctrlID uuid.UUID, result string, observedAt time.Time) {
	t.Helper()
	id := uuid.New()
	controlRef := ctrlID.String()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO evidence_records (
			id, tenant_id, control_id, observed_at, ingested_at,
			provenance, result, payload, hash, control_ref
		)
		VALUES ($1, $2, $3, $4, now(), '{}'::jsonb, $5, '{}'::jsonb, $6, $7)
	`, id, tenant, ctrlID, observedAt, result, "hash-012api-"+id.String()[:8], controlRef); err != nil {
		t.Fatalf("seed evidence: %v", err)
	}
}

// testEnv bundles the running server with the bearer token bound to the
// tenant, plus an app-pool Engine the test uses to populate the
// control_evaluations ledger before exercising the read endpoints.
type testEnv struct {
	server *httptest.Server
	bearer string
	engine *eval.Engine
}

func testServer(t *testing.T, app *pgxpool.Pool, tenant string) testEnv {
	t.Helper()
	srv := api.New(api.Config{})
	srv.AttachDB(app)

	// Slice 197: JWT bearer via slice 190 path (owner roles).
	bearer := srv.IssueTestJWT(t, testjwt.OwnerFor(uuid.MustParse(tenant), []string{"owner"}))

	h := srv.HTTPHandlerForTests()
	if h == nil {
		t.Fatal("HTTPHandlerForTests returned nil — DB pool not attached")
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return testEnv{
		server: ts,
		bearer: bearer,
		engine: eval.NewEngine(eval.NewStore(app), scope.NewStore(app)),
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

// ===== ISC-32: GET state returns result + counts + freshness =====

func TestState_ReturnsEvaluatedState(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	ctrlID := seedControl(t, admin, tenant, "automated", "monthly")
	seedEvidence(t, admin, tenant, ctrlID, "pass", time.Now().UTC().Add(-2*24*time.Hour))
	if _, err := env.engine.EvaluateControl(dbtest.WithTenantCtx(t, tenant), ctrlID, eval.TriggerManual, eval.FarFuture); err != nil {
		t.Fatalf("EvaluateControl: %v", err)
	}

	resp, body := get(t, env, "/v1/controls/"+ctrlID.String()+"/state")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET state: status %d, want 200", resp.StatusCode)
	}
	states, ok := body["states"].([]any)
	if !ok || len(states) != 1 {
		t.Fatalf("ISC-32: expected 1 state, got %v", body["states"])
	}
	st := states[0].(map[string]any)
	if st["result"] != "pass" {
		t.Fatalf("ISC-32: result = %v, want pass", st["result"])
	}
	if st["freshness_status"] != "fresh" {
		t.Fatalf("ISC-32: freshness_status = %v, want fresh", st["freshness_status"])
	}
	// evidence_count_in_window + last_observed_at must be present.
	if _, ok := st["evidence_count_in_window"]; !ok {
		t.Fatalf("ISC-32: evidence_count_in_window missing from response")
	}
	if _, ok := st["last_observed_at"]; !ok {
		t.Fatalf("ISC-32: last_observed_at missing from response")
	}
}

// ===== ISC-33: ?as-of= point-in-time horizon =====

func TestState_AsOfHorizon(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	ctrlID := seedControl(t, admin, tenant, "automated", "monthly")
	seedEvidence(t, admin, tenant, ctrlID, "pass", time.Now().UTC().Add(-2*24*time.Hour))

	// Evaluate now.
	before := time.Now().UTC().Add(-1 * time.Hour)
	if _, err := env.engine.EvaluateControl(dbtest.WithTenantCtx(t, tenant), ctrlID, eval.TriggerManual, eval.FarFuture); err != nil {
		t.Fatalf("EvaluateControl: %v", err)
	}

	// An as-of horizon BEFORE the evaluation ran returns no state.
	resp, body := get(t, env, "/v1/controls/"+ctrlID.String()+"/state?as-of="+before.Format(time.RFC3339))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET state ?as-of: status %d, want 200", resp.StatusCode)
	}
	states, _ := body["states"].([]any)
	if len(states) != 0 {
		t.Fatalf("ISC-33: as-of before evaluation should return 0 states, got %d", len(states))
	}

	// An as-of horizon AFTER returns the state.
	after := time.Now().UTC().Add(1 * time.Hour)
	resp, body = get(t, env, "/v1/controls/"+ctrlID.String()+"/state?as-of="+after.Format(time.RFC3339))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET state ?as-of after: status %d", resp.StatusCode)
	}
	states, _ = body["states"].([]any)
	if len(states) != 1 {
		t.Fatalf("ISC-33: as-of after evaluation should return 1 state, got %d", len(states))
	}
}

// ===== ISC-34: ?scope= predicate filter =====

func TestState_ScopeFilter(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	// Two scope cells, a control matching both ("true").
	for _, label := range []string{"prod-us", "prod-eu"} {
		cellID := uuid.New()
		if _, err := admin.Exec(context.Background(), `
			INSERT INTO scope_cells (id, tenant_id, label, dimensions, dimensions_hash)
			VALUES ($1, $2, $3, '{"environment":"prod"}'::jsonb, $4)
		`, cellID, tenant, label, "h-"+cellID.String()[:8]); err != nil {
			t.Fatalf("seed cell: %v", err)
		}
	}
	ctrlID := seedControl(t, admin, tenant, "automated", "monthly")
	seedEvidence(t, admin, tenant, ctrlID, "pass", time.Now().UTC().Add(-1*24*time.Hour))
	if _, err := env.engine.EvaluateControl(dbtest.WithTenantCtx(t, tenant), ctrlID, eval.TriggerManual, eval.FarFuture); err != nil {
		t.Fatalf("EvaluateControl: %v", err)
	}

	// No filter -> 2 cells.
	resp, body := get(t, env, "/v1/controls/"+ctrlID.String()+"/state")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET state: status %d", resp.StatusCode)
	}
	if states, _ := body["states"].([]any); len(states) != 2 {
		t.Fatalf("ISC-34: expected 2 cells unfiltered, got %d", len(states))
	}

	// A scope predicate matching nothing -> 0 cells.
	resp, body = get(t, env, "/v1/controls/"+ctrlID.String()+`/state?scope={"op":"eq","dim":"environment","value":"nope"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET state ?scope: status %d", resp.StatusCode)
	}
	if states, _ := body["states"].([]any); len(states) != 0 {
		t.Fatalf("ISC-34: scope filter matching nothing should return 0, got %d", len(states))
	}
}

// ===== ISC-35: GET effectiveness =====

func TestEffectiveness_ReturnsRollingPassRate(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	ctrlID := seedControl(t, admin, tenant, "automated", "monthly")
	seedEvidence(t, admin, tenant, ctrlID, "pass", time.Now().UTC().Add(-1*24*time.Hour))
	if _, err := env.engine.EvaluateControl(dbtest.WithTenantCtx(t, tenant), ctrlID, eval.TriggerManual, eval.FarFuture); err != nil {
		t.Fatalf("EvaluateControl: %v", err)
	}

	resp, body := get(t, env, "/v1/controls/"+ctrlID.String()+"/effectiveness")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET effectiveness: status %d, want 200", resp.StatusCode)
	}
	if body["control_id"] != ctrlID.String() {
		t.Fatalf("ISC-35: control_id = %v, want %s", body["control_id"], ctrlID)
	}
	// One passing evaluation -> pass_rate 1.0, total_count 1.
	if body["pass_rate"].(float64) != 1.0 {
		t.Fatalf("ISC-35: pass_rate = %v, want 1.0", body["pass_rate"])
	}
	if body["total_count"].(float64) != 1 {
		t.Fatalf("ISC-35: total_count = %v, want 1", body["total_count"])
	}
}

// ===== ISC-37: unknown control id -> 404 =====

func TestState_UnknownControlIs404(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	resp, _ := get(t, env, "/v1/controls/"+uuid.New().String()+"/state")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("ISC-37: unknown control state = %d, want 404", resp.StatusCode)
	}
	resp, _ = get(t, env, "/v1/controls/"+uuid.New().String()+"/effectiveness")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("ISC-37: unknown control effectiveness = %d, want 404", resp.StatusCode)
	}
}

// ===== ISC-37: missing bearer -> 401 =====

func TestState_MissingBearerIs401(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	// No Authorization header — the bearer-auth middleware rejects before
	// the handler is reached.
	resp, err := http.Get(env.server.URL + "/v1/controls/" + uuid.New().String() + "/state")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("ISC-37: missing bearer = %d, want 401", resp.StatusCode)
	}
}
