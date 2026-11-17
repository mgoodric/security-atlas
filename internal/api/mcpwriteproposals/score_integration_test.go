//go:build integration

// #2502: a create_risk proposal's scores take the rule POST /v1/risks
// applies. A score REST refuses is refused when the proposal is filed,
// with REST's status and message, and again on confirm for a proposal
// that reached the table some other way. FAIR is stored as REST stores it.

package mcpwriteproposals_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/mgoodric/security-atlas/internal/dbtest"
)

func TestHTTP_CreateRisk_ScoreRuleMatchesREST(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := dbtest.SeedTenant(t, admin, "mcp_write_proposals", "risks", "org_risk_categories")
	h := setupHTTPServer(t, tenant)
	ctx := context.Background()
	if _, err := admin.Exec(ctx, `SELECT seed_risk_categories($1)`, tenant); err != nil {
		t.Fatalf("seed categories: %v", err)
	}

	errorOf := func(raw []byte) string {
		var body struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &body)
		return body.Error
	}

	cases := []struct {
		name, methodology, score string
		status                   int
	}{
		{"string severity", "nist_800_30", `{"likelihood":4,"impact":5,"severity":"20"}`, http.StatusUnprocessableEntity},
		{"out-of-range severity", "nist_800_30", `{"likelihood":4,"impact":5,"severity":99}`, http.StatusUnprocessableEntity},
		{"string impact", "nist_800_30", `{"likelihood":4,"impact":"5"}`, http.StatusBadRequest},
		{"malformed 5x5", "qualitative_5x5", `{"note":"high"}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		input := fmt.Sprintf(`{"title":"t","category":"operational","methodology":%q,"inherent_score":%s}`, tc.methodology, tc.score)

		restResp, restRaw := doJSON(t, http.MethodPost, h.ts.URL+"/v1/risks", h.approverBearer, input)
		if restResp.StatusCode != tc.status {
			t.Fatalf("%s: REST status %d %s, want %d", tc.name, restResp.StatusCode, restRaw, tc.status)
		}

		body := fmt.Sprintf(`{"tool_name":"create_risk","tool_input":%s,"ai_model_name":"m","ai_model_version":"v"}`, input)
		resp, raw := doJSON(t, http.MethodPost, h.ts.URL+"/v1/mcp/write-proposals", h.bearer, body)
		if resp.StatusCode != tc.status || errorOf(raw) != errorOf(restRaw) {
			t.Fatalf("%s: file proposal %d %q, want REST's %d %q", tc.name, resp.StatusCode, errorOf(raw), tc.status, errorOf(restRaw))
		}

		// A proposal already in the table (filed before #2502) is refused
		// on confirm with the same status, and writes nothing.
		var id string
		if err := admin.QueryRow(ctx, `
			INSERT INTO mcp_write_proposals (tenant_id, tool_name, tool_input, ai_model_name, ai_model_version, created_by)
			VALUES ($1, 'create_risk', $2::jsonb, 'm', 'v', 'key_test') RETURNING id`, tenant, input).Scan(&id); err != nil {
			t.Fatalf("%s: seed proposal: %v", tc.name, err)
		}
		resp, raw = doJSON(t, http.MethodPost, h.ts.URL+"/v1/mcp/write-proposals/"+id+"/confirm", h.approverBearer, "")
		if resp.StatusCode != tc.status {
			t.Fatalf("%s: confirm %d %s, want %d", tc.name, resp.StatusCode, raw, tc.status)
		}
	}
	var risks int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM risks WHERE tenant_id = $1`, tenant).Scan(&risks); err != nil || risks != 0 {
		t.Fatalf("risks after refusals = %d (%v), want 0", risks, err)
	}

	// FAIR: filed, confirmed and stored as REST stores it.
	input := `{"title":"fair","category":"operational","methodology":"fair","inherent_score":{"lef":2,"lm":1000}}`
	resp, raw := doJSON(t, http.MethodPost, h.ts.URL+"/v1/mcp/write-proposals", h.bearer,
		fmt.Sprintf(`{"tool_name":"create_risk","tool_input":%s,"ai_model_name":"m","ai_model_version":"v"}`, input))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("file fair proposal: %d %s", resp.StatusCode, raw)
	}
	var env struct {
		Proposal struct {
			ID string `json:"id"`
		} `json:"proposal"`
	}
	_ = json.Unmarshal(raw, &env)
	if resp, raw = doJSON(t, http.MethodPost, h.ts.URL+"/v1/mcp/write-proposals/"+env.Proposal.ID+"/confirm", h.approverBearer, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("confirm fair proposal: %d %s", resp.StatusCode, raw)
	}
	if resp, raw = doJSON(t, http.MethodPost, h.ts.URL+"/v1/risks", h.approverBearer, input); resp.StatusCode != http.StatusCreated {
		t.Fatalf("REST fair create: %d %s", resp.StatusCode, raw)
	}
	var rows, distinct int
	if err := admin.QueryRow(ctx, `
		SELECT count(*), count(DISTINCT (inherent_score, residual_score)) FROM risks
		WHERE tenant_id = $1 AND title = 'fair' AND inherent_score = residual_score
		  AND inherent_score ? 'annualized_loss_exposure'`, tenant).Scan(&rows, &distinct); err != nil || rows != 2 || distinct != 1 {
		t.Fatalf("fair rows = %d, distinct shapes = %d (%v); want MCP and REST to store one canonical shape", rows, distinct, err)
	}
}
