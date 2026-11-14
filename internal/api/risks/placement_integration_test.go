//go:build integration

// #1749 — integration tests for setting a risk's level and org unit:
// POST /v1/risks with level / org_unit_id, and PATCH /v1/risks/{id} with
// its risk_audit_log trail. Real Postgres + the assembled platform router.
//
// Run with:
//
//	go test -tags=integration ./internal/api/risks/ -run Placement

package risks_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/risk"
)

// freshPlacementTenant returns a tenant id with cleanup for the tables the
// placement tests write. risk_audit_log is append-only, so its cleanup
// DELETE is refused and logged, as action_plan_audit_log's is.
func freshPlacementTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin,
		"risk_audit_log",
		"risks",
		"org_units",
	)
}

func patch(t *testing.T, env testEnv, path string, raw string) (*http.Response, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPatch, env.server.URL+path, bytes.NewBufferString(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+env.bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH %s: %v", path, err)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	_ = resp.Body.Close()
	return resp, body
}

func createPlaced(t *testing.T, env testEnv, extra map[string]any) (*http.Response, map[string]any) {
	t.Helper()
	payload := map[string]any{
		"title":          "placement risk",
		"category":       "operational",
		"inherent_score": map[string]int{"likelihood": 3, "impact": 4},
	}
	for k, v := range extra {
		payload[k] = v
	}
	return post(t, env, "/v1/risks", payload, env.bearer)
}

func riskField(t *testing.T, body map[string]any, field string) any {
	t.Helper()
	rk, ok := body["risk"].(map[string]any)
	if !ok {
		t.Fatalf("response has no risk object: %v", body)
	}
	return rk[field]
}

func placementAudit(t *testing.T, app *pgxpool.Pool, tenant string, riskID string) []risk.PlacementAuditEntry {
	t.Helper()
	entries, err := risk.NewStore(app).ListPlacementAudit(dbtest.WithTenantCtx(t, tenant), uuid.MustParse(riskID))
	if err != nil {
		t.Fatalf("list placement audit: %v", err)
	}
	return entries
}

func TestPlacement_CreateDefaultsToTeamWithNoUnit(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshPlacementTenant(t, admin)
	env := testServer(t, app, tenant)

	resp, body := createPlaced(t, env, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, want 201: %v", resp.StatusCode, body)
	}
	if got := riskField(t, body, "level"); got != "team" {
		t.Fatalf("level = %v, want team", got)
	}
	if got := riskField(t, body, "org_unit_id"); got != nil {
		t.Fatalf("org_unit_id = %v, want absent", got)
	}
}

func TestPlacement_CreateWithUnitTakesUnitLevel(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshPlacementTenant(t, admin)
	env := testServer(t, app, tenant)
	org := seedOrgUnit(t, admin, tenant, "Engineering", "org")

	resp, body := createPlaced(t, env, map[string]any{"org_unit_id": org.String()})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, want 201: %v", resp.StatusCode, body)
	}
	if got := riskField(t, body, "level"); got != "org" {
		t.Fatalf("level = %v, want org", got)
	}
	if got := riskField(t, body, "org_unit_id"); got != org.String() {
		t.Fatalf("org_unit_id = %v, want %s", got, org)
	}

	// The GET path reads the stored row back.
	id := riskField(t, body, "id").(string)
	_, got := get(t, env, "/v1/risks/"+id)
	if riskField(t, got, "level") != "org" || riskField(t, got, "org_unit_id") != org.String() {
		t.Fatalf("GET risk placement = %v / %v", riskField(t, got, "level"), riskField(t, got, "org_unit_id"))
	}
}

func TestPlacement_CreateWithLevelAndNoUnit(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshPlacementTenant(t, admin)
	env := testServer(t, app, tenant)

	resp, body := createPlaced(t, env, map[string]any{"level": "company"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, want 201: %v", resp.StatusCode, body)
	}
	if got := riskField(t, body, "level"); got != "company" {
		t.Fatalf("level = %v, want company", got)
	}
}

func TestPlacement_CreateRejectsBadPlacement(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshPlacementTenant(t, admin)
	env := testServer(t, app, tenant)
	team := seedOrgUnit(t, admin, tenant, "AppSec", "team")

	// A unit in another tenant is invisible under RLS.
	other := freshPlacementTenant(t, admin)
	foreign := seedOrgUnit(t, admin, other, "Elsewhere", "org")

	cases := map[string]map[string]any{
		"level differs from unit": {"level": "company", "org_unit_id": team.String()},
		"unknown level":           {"level": "division"},
		"unknown unit":            {"org_unit_id": uuid.NewString()},
		"cross-tenant unit":       {"org_unit_id": foreign.String()},
		"malformed unit":          {"org_unit_id": "not-a-uuid"},
	}
	for name, extra := range cases {
		resp, body := createPlaced(t, env, extra)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400: %v", name, resp.StatusCode, body)
		}
	}
	_, list := get(t, env, "/v1/risks")
	if list["count"] != float64(0) {
		t.Fatalf("a refused create stored a risk: %v", list)
	}
}

func TestPlacement_PatchMovesRiskAndAudits(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshPlacementTenant(t, admin)
	env := testServer(t, app, tenant)
	team := seedOrgUnit(t, admin, tenant, "AppSec", "team")
	company := seedOrgUnit(t, admin, tenant, "Acme", "company")

	_, created := createPlaced(t, env, map[string]any{"org_unit_id": team.String()})
	id := riskField(t, created, "id").(string)

	// Move to the company unit: the level follows the unit.
	resp, body := patch(t, env, "/v1/risks/"+id, `{"org_unit_id":"`+company.String()+`"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200: %v", resp.StatusCode, body)
	}
	if riskField(t, body, "level") != "company" || riskField(t, body, "org_unit_id") != company.String() {
		t.Fatalf("after move: level %v, unit %v", riskField(t, body, "level"), riskField(t, body, "org_unit_id"))
	}
	if riskField(t, body, "title") != "placement risk" {
		t.Fatalf("PATCH changed another field: %v", body)
	}

	entries := placementAudit(t, app, tenant, id)
	if len(entries) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.ActionType != "placement_changed" || e.Actor == "" {
		t.Fatalf("audit row = %+v", e)
	}
	var before, after struct {
		Level     string  `json:"level"`
		OrgUnitID *string `json:"org_unit_id"`
	}
	_ = json.Unmarshal(e.BeforeState, &before)
	_ = json.Unmarshal(e.AfterState, &after)
	if before.Level != "team" || before.OrgUnitID == nil || *before.OrgUnitID != team.String() {
		t.Fatalf("audit before = %s", e.BeforeState)
	}
	if after.Level != "company" || after.OrgUnitID == nil || *after.OrgUnitID != company.String() {
		t.Fatalf("audit after = %s", e.AfterState)
	}

	// Unbind: an explicit null clears the unit and keeps the level.
	resp, body = patch(t, env, "/v1/risks/"+id, `{"org_unit_id":null}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unbind status %d: %v", resp.StatusCode, body)
	}
	if riskField(t, body, "org_unit_id") != nil || riskField(t, body, "level") != "company" {
		t.Fatalf("after unbind: level %v, unit %v", riskField(t, body, "level"), riskField(t, body, "org_unit_id"))
	}

	// With no unit any level is allowed.
	resp, body = patch(t, env, "/v1/risks/"+id, `{"level":"org"}`)
	if resp.StatusCode != http.StatusOK || riskField(t, body, "level") != "org" {
		t.Fatalf("level-only move: status %d, body %v", resp.StatusCode, body)
	}

	// A no-op writes no audit row.
	resp, _ = patch(t, env, "/v1/risks/"+id, `{"level":"org"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("no-op status %d", resp.StatusCode)
	}
	if n := len(placementAudit(t, app, tenant, id)); n != 3 {
		t.Fatalf("audit rows = %d, want 3 (move, unbind, level)", n)
	}

	// The trail is append-only, even for the owner role.
	if _, err := admin.Exec(context.Background(),
		`UPDATE risk_audit_log SET actor = 'tampered' WHERE tenant_id = $1`, tenant); err == nil {
		t.Fatal("UPDATE on risk_audit_log should be denied")
	}
	if _, err := admin.Exec(context.Background(),
		`DELETE FROM risk_audit_log WHERE tenant_id = $1`, tenant); err == nil {
		t.Fatal("DELETE on risk_audit_log should be denied")
	}
}

func TestPlacement_PatchRejects(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshPlacementTenant(t, admin)
	env := testServer(t, app, tenant)
	team := seedOrgUnit(t, admin, tenant, "AppSec", "team")

	_, created := createPlaced(t, env, map[string]any{"org_unit_id": team.String()})
	id := riskField(t, created, "id").(string)

	bad := map[string]string{
		"level differs from current unit": `{"level":"company"}`,
		"level differs from new unit":     `{"level":"org","org_unit_id":"` + team.String() + `"}`,
		"unknown unit":                    `{"org_unit_id":"` + uuid.NewString() + `"}`,
		"unknown level":                   `{"level":"division"}`,
		"other field":                     `{"title":"renamed"}`,
		"empty body":                      `{}`,
	}
	for name, raw := range bad {
		resp, body := patch(t, env, "/v1/risks/"+id, raw)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400: %v", name, resp.StatusCode, body)
		}
	}
	if n := len(placementAudit(t, app, tenant, id)); n != 0 {
		t.Fatalf("a refused PATCH wrote %d audit rows", n)
	}

	resp, _ := patch(t, env, "/v1/risks/"+uuid.NewString(), `{"level":"org"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown risk: status %d, want 404", resp.StatusCode)
	}
}
