//go:build integration

// Slice 020 — integration tests for the risk-control linkage HTTP API. Real
// Postgres + the assembled platform router so the tests exercise the full
// request path (tenancy middleware, RLS, the risk.Store + eval.Engine-backed
// ResidualDeriver).
//
// Run with:
//
//	go test -tags=integration -race ./internal/api/risks/...
//
// Requires DATABASE_URL_APP (atlas_app role) and DATABASE_URL (admin role).
//
// Coverage:
//
//	ISC-16  POST /v1/risks/{id}/controls links a control
//	ISC-17  linked control appears in linked_control_ids[] on GET
//	ISC-19  linking an unknown control -> 404
//	ISC-20  linking on an unknown risk  -> 404
//	ISC-21  link endpoint requires tenant context (401 without bearer)
//	ISC-22..26  GET /v1/risks/{id} returns inherent_score, residual_score,
//	            effectiveness breakdown per linked control

package risks_test

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
			`DELETE FROM risk_control_links WHERE tenant_id = $1`,
			`DELETE FROM control_evaluations WHERE tenant_id = $1`,
			`DELETE FROM evidence_records WHERE tenant_id = $1`,
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

func seedControl(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	ctrlID := uuid.New()
	bundleID := "slice-020-api-bundle-" + ctrlID.String()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (
			id, tenant_id, title, control_family, implementation_type,
			bundle_id, freshness_class, evidence_queries, applicability_expr
		)
		VALUES ($1, $2, 'slice 020 api test control', 'AAA', 'automated',
		        $3, 'monthly', '[]'::jsonb, 'true')
	`, ctrlID, tenant, bundleID); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return ctrlID
}

func seedNistRisk(t *testing.T, admin *pgxpool.Pool, tenant string, likelihood, impact int) uuid.UUID {
	t.Helper()
	riskID := uuid.New()
	inherent, _ := json.Marshal(map[string]int{"likelihood": likelihood, "impact": impact})
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO risks (
			id, tenant_id, title, description, category, methodology,
			inherent_score, treatment, treatment_owner, residual_score
		)
		VALUES ($1, $2, 'slice 020 api test risk', '', 'operational', 'nist_800_30',
		        $3::jsonb, 'avoid', 'owner', '{}'::jsonb)
	`, riskID, tenant, string(inherent)); err != nil {
		t.Fatalf("seed nist risk: %v", err)
	}
	return riskID
}

type testEnv struct {
	server *httptest.Server
	bearer string
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
	return testEnv{server: ts, bearer: bearer}
}

func get(t *testing.T, env testEnv, path string) (*http.Response, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, env.server.URL+path, nil)
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

// post issues an authenticated POST. When bearer is "" the Authorization
// header is omitted (the 401 path).
func post(t *testing.T, env testEnv, path string, payload any, bearer string) (*http.Response, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if payload != nil {
		_ = json.NewEncoder(&buf).Encode(payload)
	}
	req, _ := http.NewRequest(http.MethodPost, env.server.URL+path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	_ = resp.Body.Close()
	return resp, body
}

// ===== AC-1 / ISC-16, ISC-17: link a control, it appears on the risk =====

func TestLinkControl_LinksAndAppearsInLinkedControlIDs(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	riskID := seedNistRisk(t, admin, tenant, 4, 4)
	ctrlID := seedControl(t, admin, tenant)

	resp, body := post(t, env, "/v1/risks/"+riskID.String()+"/controls",
		map[string]any{"control_id": ctrlID.String()}, env.bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ISC-16: POST link: status %d, want 200 (body %v)", resp.StatusCode, body)
	}
	// ISC-17: the link appears in linked_control_ids[] on the response risk.
	rk, ok := body["risk"].(map[string]any)
	if !ok {
		t.Fatalf("ISC-17: response missing risk object")
	}
	linked, ok := rk["linked_control_ids"].([]any)
	if !ok || len(linked) != 1 || linked[0] != ctrlID.String() {
		t.Fatalf("ISC-17: linked_control_ids = %v, want [%s]", rk["linked_control_ids"], ctrlID)
	}

	// And it persists — a fresh GET shows the same link.
	resp, body = get(t, env, "/v1/risks/"+riskID.String())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET risk: status %d, want 200", resp.StatusCode)
	}
	rk = body["risk"].(map[string]any)
	linked = rk["linked_control_ids"].([]any)
	if len(linked) != 1 {
		t.Fatalf("ISC-17: GET linked_control_ids = %v, want 1 entry", linked)
	}
}

// ===== ISC-21: link endpoint requires tenant context =====

func TestLinkControl_RequiresAuth(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	riskID := seedNistRisk(t, admin, tenant, 2, 2)
	ctrlID := seedControl(t, admin, tenant)

	// No bearer -> 401-shaped path.
	resp, _ := post(t, env, "/v1/risks/"+riskID.String()+"/controls",
		map[string]any{"control_id": ctrlID.String()}, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("ISC-21: unauthenticated link = %d, want 401", resp.StatusCode)
	}
}

// ===== ISC-19: linking an unknown control -> 404 =====

func TestLinkControl_UnknownControl404(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	riskID := seedNistRisk(t, admin, tenant, 3, 3)
	resp, _ := post(t, env, "/v1/risks/"+riskID.String()+"/controls",
		map[string]any{"control_id": uuid.NewString()}, env.bearer)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("ISC-19: unknown control link = %d, want 404", resp.StatusCode)
	}
}

// ===== ISC-20: linking on an unknown risk -> 404 =====

func TestLinkControl_UnknownRisk404(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	ctrlID := seedControl(t, admin, tenant)
	resp, _ := post(t, env, "/v1/risks/"+uuid.NewString()+"/controls",
		map[string]any{"control_id": ctrlID.String()}, env.bearer)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("ISC-20: unknown risk link = %d, want 404", resp.StatusCode)
	}
}

// ===== AC-2 / ISC-22..26: GET risk returns residual + breakdown =====

func TestGetRisk_ReturnsResidualAndBreakdown(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	riskID := seedNistRisk(t, admin, tenant, 5, 5) // inherent 25
	ctrlID := seedControl(t, admin, tenant)
	// Link a control via the API so the breakdown has an entry.
	resp, _ := post(t, env, "/v1/risks/"+riskID.String()+"/controls",
		map[string]any{"control_id": ctrlID.String()}, env.bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("link setup: status %d, want 200", resp.StatusCode)
	}

	resp, body := get(t, env, "/v1/risks/"+riskID.String())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET risk: status %d, want 200", resp.StatusCode)
	}
	residual, ok := body["residual"].(map[string]any)
	if !ok {
		t.Fatalf("AC-2: response missing residual object: %v", body)
	}
	// ISC-22 / ISC-23: inherent_score + residual_score present and numeric.
	if _, ok := residual["inherent_score"].(float64); !ok {
		t.Fatalf("ISC-22: inherent_score missing or not numeric: %v", residual["inherent_score"])
	}
	if _, ok := residual["residual_score"].(float64); !ok {
		t.Fatalf("ISC-23: residual_score missing or not numeric: %v", residual["residual_score"])
	}
	// ISC-24: effectiveness_breakdown[] present, one entry per linked control.
	bd, ok := residual["effectiveness_breakdown"].([]any)
	if !ok || len(bd) != 1 {
		t.Fatalf("ISC-24: effectiveness_breakdown = %v, want 1 entry", residual["effectiveness_breakdown"])
	}
	entry := bd[0].(map[string]any)
	// ISC-25: design/operational/coverage components present.
	for _, f := range []string{"design_score", "operational_score", "coverage_score"} {
		if _, ok := entry[f].(float64); !ok {
			t.Fatalf("ISC-25: breakdown entry missing numeric %q: %v", f, entry)
		}
	}
	// ISC-26: the composite control_effectiveness present.
	if _, ok := entry["control_effectiveness"].(float64); !ok {
		t.Fatalf("ISC-26: breakdown entry missing control_effectiveness: %v", entry)
	}
}

// ===== AC-7 / ISC-39, ISC-40: GET risk with no controls warns =====

func TestGetRisk_NoControlsWarns(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	riskID := seedNistRisk(t, admin, tenant, 4, 4) // inherent 16

	resp, body := get(t, env, "/v1/risks/"+riskID.String())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET risk: status %d, want 200", resp.StatusCode)
	}
	residual := body["residual"].(map[string]any)
	if residual["warning"] != "no_controls_linked" {
		t.Fatalf("ISC-40: warning = %v, want no_controls_linked", residual["warning"])
	}
	// ISC-39: residual equals inherent when no controls linked.
	if residual["residual_score"].(float64) != residual["inherent_score"].(float64) {
		t.Fatalf("ISC-39: residual_score %v != inherent_score %v",
			residual["residual_score"], residual["inherent_score"])
	}
}
