//go:build integration

// #2867 — a risk with no inherent_score (#2511: an unscored risk, from MCP
// create_risk or POST /v1/risks) reads back 200 from GET /v1/risks/{id},
// with a residual marked unscored and no numeric scores, instead of a 500
// from the residual deriver. Linking a control to it is a 200 too, and
// leaves its stored residual_score `{}`.
//
// Run with:
//
//	go test -tags=integration -count=1 -run TestUnscoredResidual_2867 ./internal/api/risks/
//
// Requires DATABASE_URL_APP (atlas_app role) and DATABASE_URL (admin role).

package risks_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestUnscoredResidual_2867_MCPCreateRiskThenGet(t *testing.T) {
	env := newControlLinkEnv(t)
	raw, _ := json.Marshal(map[string]any{"title": "Unscored over MCP", "category": "operational"})
	code, body := env.do(t, http.MethodPost, "/v1/mcp/write-proposals", env.ownerJWT, map[string]any{
		"tool_name":        "create_risk",
		"tool_input":       json.RawMessage(raw),
		"ai_model_name":    "test-model",
		"ai_model_version": "1",
	})
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("file proposal: status %d: %v", code, body)
	}
	proposalID := body["proposal"].(map[string]any)["id"].(string)
	confirmed := env.confirm(t, proposalID, env.apprJWT, http.StatusOK)
	id, _ := confirmed["proposal"].(map[string]any)["applied_subject"].(string)
	if id == "" {
		t.Fatalf("confirmed proposal has no applied_subject: %v", confirmed)
	}
	checkUnscoredRead(t, env, id)
}

func TestUnscoredResidual_2867_RESTCreateThenGet(t *testing.T) {
	env := newControlLinkEnv(t)
	code, body := env.do(t, http.MethodPost, "/v1/risks", env.ownerJWT,
		map[string]any{"title": "Score later", "category": "operational"})
	if code != http.StatusCreated {
		t.Fatalf("create: status %d: %v", code, body)
	}
	checkUnscoredRead(t, env, riskField(t, body, "id").(string))
}

// checkUnscoredRead asserts the GET and link-control reads of an unscored
// risk: 200, residual unscored with null scores, stored residual kept `{}`.
func checkUnscoredRead(t *testing.T, env transitionEnv, id string) {
	t.Helper()
	code, body := env.do(t, http.MethodGet, "/v1/risks/"+id, env.ownerJWT, nil)
	if code != http.StatusOK {
		t.Fatalf("GET unscored risk: status %d, want 200: %v", code, body)
	}
	checkUnscoredResidual(t, "GET", body)

	ctrl := seedControl(t, env.admin, env.tenant)
	checkUnscoredResidual(t, "link", env.link(t, id, ctrl, http.StatusOK))

	code, body = env.do(t, http.MethodGet, "/v1/risks/"+id, env.ownerJWT, nil)
	if code != http.StatusOK {
		t.Fatalf("GET unscored risk after link: status %d, want 200: %v", code, body)
	}
	res := checkUnscoredResidual(t, "GET after link", body)
	if bd, _ := res["effectiveness_breakdown"].([]any); len(bd) != 1 {
		t.Errorf("GET after link: breakdown has %d entries, want 1: %v", len(bd), res)
	}

	var stored string
	if err := env.admin.QueryRow(context.Background(),
		`SELECT residual_score::text FROM risks WHERE id = $1`, id).Scan(&stored); err != nil {
		t.Fatalf("read residual_score: %v", err)
	}
	if stored != "{}" {
		t.Errorf("stored residual_score = %s after link, want {}", stored)
	}
}

func checkUnscoredResidual(t *testing.T, step string, body map[string]any) map[string]any {
	t.Helper()
	res, ok := body["residual"].(map[string]any)
	if !ok {
		t.Fatalf("%s: no residual object: %v", step, body)
	}
	if res["unscored"] != true {
		t.Errorf("%s: residual.unscored = %v, want true", step, res["unscored"])
	}
	for _, k := range []string{"inherent_score", "residual_score"} {
		if v, present := res[k]; !present || v != nil {
			t.Errorf("%s: residual.%s = %v (present %v), want null", step, k, v, present)
		}
	}
	return res
}
