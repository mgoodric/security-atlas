//go:build integration

// #2511 — POST /v1/risks with no inherent or residual score creates an
// unscored risk, as the MCP create_risk tool does: both scores stored as
// {}, read as Unscored, and left out of every severity band.
//
// Run with:
//
//	go test -tags=integration -count=1 -run TestCreate_Unscored ./internal/api/risks/
//
// Requires DATABASE_URL_APP (atlas_app role) and DATABASE_URL (admin role).

package risks_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/mgoodric/security-atlas/internal/dbtest"
)

func TestCreate_UnscoredRisk(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshOwnerTenant(t, admin)
	env := testServer(t, app, tenant)

	for name, payload := range map[string]map[string]any{
		"omitted":      {"title": "Logged before assessment", "category": "operational"},
		"null":         {"title": "Null score", "category": "operational", "inherent_score": nil},
		"empty object": {"title": "Empty score", "category": "operational", "inherent_score": map[string]any{}},
		"fair omitted": {"title": "FAIR later", "category": "operational", "methodology": "fair"},
	} {
		resp, body := post(t, env, "/v1/risks", payload, env.bearer)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("%s: status %d, want 201: %v", name, resp.StatusCode, body)
		}
		id := body["risk"].(map[string]any)["id"].(string)
		var inherent, residual string
		if err := admin.QueryRow(context.Background(),
			`SELECT inherent_score::text, residual_score::text FROM risks WHERE id = $1`, id,
		).Scan(&inherent, &residual); err != nil {
			t.Fatalf("%s: read risk: %v", name, err)
		}
		if inherent != "{}" || residual != "{}" {
			t.Errorf("%s: stored %s / %s, want {} / {}", name, inherent, residual)
		}
	}

	// A residual score without an inherent one is refused.
	if resp, body := post(t, env, "/v1/risks", map[string]any{
		"title": "Residual only", "category": "operational", "residual_score": map[string]any{"score": 12},
	}, env.bearer); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("residual without inherent: status %d, want 400: %v", resp.StatusCode, body)
	}

	// Unscored risks are listed, with severity 0, and are in no band.
	resp, body := get(t, env, "/v1/risks")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/risks: status %d: %v", resp.StatusCode, body)
	}
	if total := int(body["total"].(float64)); total != 4 {
		t.Errorf("register total %d, want 4", total)
	}
	for _, r := range body["risks"].([]any) {
		if sev := r.(map[string]any)["severity"].(float64); sev != 0 {
			t.Errorf("unscored risk severity %v, want 0", sev)
		}
	}
	for _, band := range []string{"high", "medium", "low"} {
		if _, b := get(t, env, "/v1/risks?severity_band="+band); int(b["total"].(float64)) != 0 {
			t.Errorf("severity_band=%s counts an unscored risk", band)
		}
	}
	for _, path := range []string{"/v1/risks/heatmap", "/v1/risks/theme-heatmap", "/v1/risks?sort=residual,age"} {
		if resp, b := get(t, env, path); resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s with unscored risks: status %d: %v", path, resp.StatusCode, b)
		}
	}
}
