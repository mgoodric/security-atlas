//go:build integration

// #2060 — GET /v1/risks leaves roll-up parents out of the register's rows
// and count unless ?include_rollups=true. Real Postgres with RLS + the
// assembled platform router.
//
// Run with:
//
//	go test -tags=integration ./internal/api/risks/ -run RollUps

package risks_test

import (
	"net/http"
	"testing"

	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// listedIDs reads the ids out of a GET /v1/risks body and checks the
// envelope's count agrees with the rows.
func listedIDs(t *testing.T, body map[string]any) map[string]map[string]any {
	t.Helper()
	ids := linkedIDs(t, body, "risks")
	if got, _ := body["count"].(float64); int(got) != len(ids) {
		t.Fatalf("count = %v, want %d (the row count)", body["count"], len(ids))
	}
	return ids
}

// AC: with 3 risks and 1 roll-up of 2 of them, the register count is 3 by
// default and 4 with the toggle.
func TestRollUps_ListExcludesRollUpsUnlessAsked(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshChildrenTenant(t, admin)
	env := testServer(t, app, tenant)

	parent := seedLeveledRisk(t, admin, tenant, "company", "roll-up", 5, 5)
	teamA := seedLeveledRisk(t, admin, tenant, "team", "team finding A", 3, 4)
	teamB := seedLeveledRisk(t, admin, tenant, "team", "team finding B", 4, 4)
	teamC := seedLeveledRisk(t, admin, tenant, "team", "team finding C", 1, 2)

	if resp, body := linkChildren(t, env, parent, teamA, teamB); resp.StatusCode != http.StatusOK {
		t.Fatalf("link status %d, want 200: %v", resp.StatusCode, body)
	}

	resp, body := get(t, env, "/v1/risks")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status %d: %v", resp.StatusCode, body)
	}
	def := listedIDs(t, body)
	if len(def) != 3 {
		t.Fatalf("default list has %d risks, want 3: %v", len(def), body)
	}
	if _, ok := def[parent.String()]; ok {
		t.Fatalf("default list includes the roll-up %s", parent)
	}
	for _, id := range []string{teamA.String(), teamB.String(), teamC.String()} {
		if _, ok := def[id]; !ok {
			t.Fatalf("default list is missing child risk %s", id)
		}
		if _, ok := def[id]["child_count"]; ok {
			t.Fatalf("risk %s carries child_count but is not a roll-up", id)
		}
	}

	resp, body = get(t, env, "/v1/risks?include_rollups=true")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status %d: %v", resp.StatusCode, body)
	}
	all := listedIDs(t, body)
	if len(all) != 4 {
		t.Fatalf("list with roll-ups has %d risks, want 4: %v", len(all), body)
	}
	if got, _ := all[parent.String()]["child_count"].(float64); got != 2 {
		t.Fatalf("roll-up child_count = %v, want 2", all[parent.String()]["child_count"])
	}

	resp, body = get(t, env, "/v1/risks?include_rollups=maybe")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed include_rollups status %d, want 400: %v", resp.StatusCode, body)
	}
}

// AC: the dashboard's top risks (GET /v1/risks?sort=residual,age, the
// top-risks panel feed) leave the roll-up out even though it outranks its
// children on severity.
func TestRollUps_DashboardTopRisksExcludeRollUps(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshChildrenTenant(t, admin)
	env := testServer(t, app, tenant)

	parent := seedLeveledRisk(t, admin, tenant, "company", "roll-up", 5, 5)
	child := seedLeveledRisk(t, admin, tenant, "team", "team finding", 3, 4)
	if resp, body := linkChildren(t, env, parent, child); resp.StatusCode != http.StatusOK {
		t.Fatalf("link status %d, want 200: %v", resp.StatusCode, body)
	}

	resp, body := get(t, env, "/v1/risks?sort=residual,age")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status %d: %v", resp.StatusCode, body)
	}
	ids := listedIDs(t, body)
	if _, ok := ids[parent.String()]; ok {
		t.Fatalf("top risks include the roll-up %s: %v", parent, body)
	}
	if _, ok := ids[child.String()]; !ok {
		t.Fatalf("top risks are missing the child %s: %v", child, body)
	}
}
