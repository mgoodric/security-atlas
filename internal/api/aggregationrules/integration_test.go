//go:build integration

// Slice 054 — integration tests for the aggregation-rules HTTP API. Real
// Postgres + the assembled platform router so the tests exercise the full
// request path (Content-Type negotiation, tenancy middleware, RLS).
//
// Run with:
//
//	go test -tags=integration -race ./internal/api/aggregationrules/...
//
// Requires DATABASE_URL_APP (atlas_app role) and DATABASE_URL (admin role).
//
// Coverage:
//
//	ISC-24  POST accepts JSON, creates status=staged
//	ISC-25  POST accepts YAML, creates status=staged
//	ISC-26  POST returns 400 with field-level errors on an invalid rule
//	ISC-27  GET lists tenant rules; GET /{id} returns one
//	ISC-28  PATCH /{id}/activate flips staged->active and writes an audit-log row
//	ISC-29  PATCH /{id}/deactivate flips active->inactive and writes an audit-log row

package aggregationrules_test

import (
	"bytes"
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
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/risk/aggrule"
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
			`DELETE FROM aggregation_rule_evaluations WHERE tenant_id = $1`,
			`DELETE FROM aggregation_rule_audit_log WHERE tenant_id = $1`,
			`DELETE FROM aggregation_rules WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

// testEnv bundles the running server with the bearer token whose credential
// is bound to the test tenant. The tenancy middleware lifts that credential's
// tenant onto every request context — the same path production uses.
type testEnv struct {
	server *httptest.Server
	bearer string
}

// testServer builds the platform HTTP handler and issues a bootstrap-owner
// credential bound to `tenant`. Requests must carry the returned bearer
// token; the tenancy middleware then runs every handler under the right
// `app.current_tenant` GUC.
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
	return testEnv{server: ts, bearer: bearer}
}

const validRuleJSON = `{
  "rule_id": "ownership-cross-team",
  "target_theme": "ownership",
  "min_risks": 3,
  "min_teams": 2,
  "window_days": 90,
  "parent_level": "org",
  "severity_function": "max",
  "title_template": "Cross-team {theme} pattern",
  "custom_rego": ""
}`

const validRuleYAML = `rule_id: access-review-lag
target_theme: access-review
min_risks: 2
min_teams: 1
window_days: 30
parent_level: company
severity_function: weighted_max
title_template: "Access review backlog"
custom_rego: ""
`

// post issues a POST with the given content-type and body, authenticated
// with the env's bearer token.
func post(t *testing.T, env testEnv, contentType, body string) (*http.Response, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, env.server.URL+"/v1/aggregation-rules", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+env.bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	return resp, decode(t, resp)
}

// get issues an authenticated GET against the env's server.
func get(t *testing.T, env testEnv, path string) (*http.Response, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, env.server.URL+path, nil)
	if err != nil {
		t.Fatalf("new GET request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+env.bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	return resp, decode(t, resp)
}

func decode(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return out
}

func ruleObj(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	r, ok := body["rule"].(map[string]any)
	if !ok {
		t.Fatalf("response has no `rule` object: %v", body)
	}
	return r
}

// ===== ISC-24: POST accepts JSON, creates status=staged =====

func TestHTTP_PostJSON_CreatesStaged_ISC24(t *testing.T) {
	app := openPool(t, appDSN(t))
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)
	ts := testServer(t, app, tenant)

	resp, body := post(t, ts, "application/json", validRuleJSON)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST json: status %d, want 201; body=%v", resp.StatusCode, body)
	}
	r := ruleObj(t, body)
	if r["status"] != "staged" {
		t.Fatalf("new rule status: got %v, want staged", r["status"])
	}
	if r["rule_id"] != "ownership-cross-team" {
		t.Fatalf("rule_id: got %v", r["rule_id"])
	}
	if r["id"] == "" || r["id"] == nil {
		t.Fatalf("rule has no id")
	}
}

// ===== ISC-25: POST accepts YAML, creates status=staged =====

func TestHTTP_PostYAML_CreatesStaged_ISC25(t *testing.T) {
	app := openPool(t, appDSN(t))
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)
	ts := testServer(t, app, tenant)

	resp, body := post(t, ts, "application/yaml", validRuleYAML)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST yaml: status %d, want 201; body=%v", resp.StatusCode, body)
	}
	r := ruleObj(t, body)
	if r["status"] != "staged" {
		t.Fatalf("new rule status: got %v, want staged", r["status"])
	}
	if r["rule_id"] != "access-review-lag" {
		t.Fatalf("rule_id: got %v", r["rule_id"])
	}
	if r["severity_function"] != "weighted_max" {
		t.Fatalf("severity_function: got %v, want weighted_max", r["severity_function"])
	}
}

// ===== ISC-26: POST returns 400 with field-level errors on an invalid rule =====

func TestHTTP_PostInvalid_FieldLevel400_ISC26(t *testing.T) {
	app := openPool(t, appDSN(t))
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)
	ts := testServer(t, app, tenant)

	// min_risks 0, empty target_theme, bogus parent_level — three field errors.
	badJSON := `{
	  "rule_id": "bad-rule",
	  "target_theme": "",
	  "min_risks": 0,
	  "min_teams": 2,
	  "window_days": 90,
	  "parent_level": "galaxy",
	  "severity_function": "max"
	}`
	resp, body := post(t, ts, "application/json", badJSON)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST invalid: status %d, want 400; body=%v", resp.StatusCode, body)
	}
	fields, ok := body["fields"].([]any)
	if !ok {
		t.Fatalf("400 body has no `fields` array: %v", body)
	}
	got := map[string]bool{}
	for _, f := range fields {
		fm, _ := f.(map[string]any)
		if name, _ := fm["field"].(string); name != "" {
			got[name] = true
		}
	}
	for _, want := range []string{"target_theme", "min_risks", "parent_level"} {
		if !got[want] {
			t.Errorf("expected a field error for %q, got %v", want, got)
		}
	}
}

// ===== ISC-27: GET lists tenant rules; GET /{id} returns one =====

func TestHTTP_ListAndGet_ISC27(t *testing.T) {
	app := openPool(t, appDSN(t))
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)
	ts := testServer(t, app, tenant)

	_, b1 := post(t, ts, "application/json", validRuleJSON)
	_, b2 := post(t, ts, "application/yaml", validRuleYAML)
	id1 := ruleObj(t, b1)["id"].(string)
	id2 := ruleObj(t, b2)["id"].(string)

	// List.
	resp, listBody := get(t, ts, "/v1/aggregation-rules")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET list: status %d, want 200", resp.StatusCode)
	}
	if cnt, _ := listBody["count"].(float64); int(cnt) != 2 {
		t.Fatalf("list count: got %v, want 2", listBody["count"])
	}

	// Get by id.
	resp, getBody := get(t, ts, "/v1/aggregation-rules/"+id1)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET by id: status %d, want 200", resp.StatusCode)
	}
	if ruleObj(t, getBody)["id"] != id1 {
		t.Fatalf("GET by id returned wrong rule")
	}

	// Unknown id -> 404.
	resp, _ = get(t, ts, "/v1/aggregation-rules/"+uuid.NewString())
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET unknown id: status %d, want 404", resp.StatusCode)
	}
	_ = id2
}

// patch issues an authenticated PATCH against a sub-resource transition route.
func patch(t *testing.T, env testEnv, path string) (*http.Response, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPatch, env.server.URL+path, nil)
	if err != nil {
		t.Fatalf("new PATCH request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+env.bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	return resp, decode(t, resp)
}

// auditLogRowCount counts audit-log rows for a rule via the store.
func auditLogRowCount(t *testing.T, app *pgxpool.Pool, tenant, ruleID string) int {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	store := aggrule.NewStore(app)
	id, err := uuid.Parse(ruleID)
	if err != nil {
		t.Fatalf("parse rule id: %v", err)
	}
	rows, err := store.AuditLog(ctx, id)
	if err != nil {
		t.Fatalf("AuditLog: %v", err)
	}
	return len(rows)
}

// ===== ISC-28: PATCH /{id}/activate flips staged->active + writes audit-log row =====

func TestHTTP_Activate_ISC28(t *testing.T) {
	app := openPool(t, appDSN(t))
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)
	ts := testServer(t, app, tenant)

	_, createBody := post(t, ts, "application/json", validRuleJSON)
	id := ruleObj(t, createBody)["id"].(string)

	// After create: exactly one audit-log row ("created").
	if n := auditLogRowCount(t, app, tenant, id); n != 1 {
		t.Fatalf("after create: %d audit-log rows, want 1", n)
	}

	resp, body := patch(t, ts, "/v1/aggregation-rules/"+id+"/activate")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH activate: status %d, want 200; body=%v", resp.StatusCode, body)
	}
	r := ruleObj(t, body)
	if r["status"] != "active" {
		t.Fatalf("after activate: status %v, want active", r["status"])
	}
	if r["activated_by"] == "" || r["activated_by"] == nil {
		t.Fatalf("after activate: activated_by is empty")
	}

	// Activation wrote a second audit-log row.
	if n := auditLogRowCount(t, app, tenant, id); n != 2 {
		t.Fatalf("after activate: %d audit-log rows, want 2 (created + activated)", n)
	}

	// Activating an already-active rule -> 409.
	resp, _ = patch(t, ts, "/v1/aggregation-rules/"+id+"/activate")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("double activate: status %d, want 409", resp.StatusCode)
	}
}

// ===== ISC-29: PATCH /{id}/deactivate flips active->inactive + writes audit-log row =====

func TestHTTP_Deactivate_ISC29(t *testing.T) {
	app := openPool(t, appDSN(t))
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)
	ts := testServer(t, app, tenant)

	_, createBody := post(t, ts, "application/json", validRuleJSON)
	id := ruleObj(t, createBody)["id"].(string)

	// Deactivating a STAGED rule -> 409 (must be active first).
	resp, _ := patch(t, ts, "/v1/aggregation-rules/"+id+"/deactivate")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("deactivate staged rule: status %d, want 409", resp.StatusCode)
	}

	// Activate, then deactivate.
	resp, _ = patch(t, ts, "/v1/aggregation-rules/"+id+"/activate")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("activate: status %d", resp.StatusCode)
	}
	resp, body := patch(t, ts, "/v1/aggregation-rules/"+id+"/deactivate")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH deactivate: status %d, want 200; body=%v", resp.StatusCode, body)
	}
	if ruleObj(t, body)["status"] != "inactive" {
		t.Fatalf("after deactivate: status %v, want inactive", ruleObj(t, body)["status"])
	}

	// Three audit-log rows now: created + activated + deactivated.
	if n := auditLogRowCount(t, app, tenant, id); n != 3 {
		t.Fatalf("after deactivate: %d audit-log rows, want 3", n)
	}
}
