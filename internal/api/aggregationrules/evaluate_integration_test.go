//go:build integration

// #1750 — the slice 054 engine runs on risk writes and on demand. Before
// this, aggrule.Store.Evaluate had no caller outside its package: an active
// rule matched risks and produced nothing, and aggregation_rule_evaluations
// stayed empty. These tests drive the full HTTP path (risk create + theme
// assignment, then POST /v1/aggregation-rules/evaluate) against real
// Postgres with RLS.

package aggregationrules_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/risk/aggrule"
)

// freshRiskTenant seeds a tenant whose cleanup also covers the risks and
// meta-risk links the engine writes, children of the rule tables first.
func freshRiskTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin,
		"aggregation_rule_evaluations",
		"risk_aggregations",
		"aggregation_rule_audit_log",
		"aggregation_rules",
		"risks",
	)
}

// postPath issues an authenticated JSON POST to an arbitrary path.
func postPath(t *testing.T, env testEnv, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, env.server.URL+path, bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+env.bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp, decode(t, resp)
}

// activeRule creates a rule from ruleJSON and activates it, returning its id.
func activeRule(t *testing.T, env testEnv, ruleJSON string) string {
	t.Helper()
	resp, body := post(t, env, "application/json", ruleJSON)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create rule: status %d; body=%v", resp.StatusCode, body)
	}
	id := ruleObj(t, body)["id"].(string)
	if resp, body := patch(t, env, "/v1/aggregation-rules/"+id+"/activate"); resp.StatusCode != http.StatusOK {
		t.Fatalf("activate rule: status %d; body=%v", resp.StatusCode, body)
	}
	return id
}

// writeThemedRisk creates a risk and tags it with theme — the two risk writes
// the engine hooks.
func writeThemedRisk(t *testing.T, env testEnv, title, theme string) {
	t.Helper()
	resp, body := postPath(t, env, "/v1/risks", map[string]any{
		"title":          title,
		"category":       "operational",
		"inherent_score": map[string]int{"likelihood": 3, "impact": 4},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create risk: status %d; body=%v", resp.StatusCode, body)
	}
	id := body["risk"].(map[string]any)["id"].(string)
	resp, body = postPath(t, env, "/v1/risks/"+id+"/themes", map[string]any{"themes": []string{theme}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("assign theme: status %d; body=%v", resp.StatusCode, body)
	}
}

func evaluations(t *testing.T, app *pgxpool.Pool, tenant, ruleID string) []aggrule.Evaluation {
	t.Helper()
	evs, err := aggrule.NewStore(app).Evaluations(dbtest.WithTenantCtx(t, tenant), uuid.MustParse(ruleID))
	if err != nil {
		t.Fatalf("Evaluations: %v", err)
	}
	return evs
}

const ownershipRuleJSON = `{
  "rule_id": "ownership-gaps",
  "target_theme": "ownership",
  "min_risks": 3,
  "min_teams": 1,
  "window_days": 90,
  "parent_level": "org",
  "severity_function": "max",
  "title_template": "Ownership gaps across {theme}",
  "custom_rego": ""
}`

const otherThemeRuleJSON = `{
  "rule_id": "access-review-lag",
  "target_theme": "access-review",
  "min_risks": 1,
  "min_teams": 1,
  "window_days": 90,
  "parent_level": "org",
  "severity_function": "max",
  "title_template": "Access review backlog",
  "custom_rego": ""
}`

// A matching rule fires on the risk write that meets its threshold, and the
// meta-risk is updated (not duplicated) by later writes in the same window.
// A rule on another theme is never evaluated by those writes.
func TestRiskWrite_EvaluatesActiveRules_1750(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshRiskTenant(t, admin)
	env := testServer(t, app, tenant)

	ruleID := activeRule(t, env, ownershipRuleJSON)
	otherID := activeRule(t, env, otherThemeRuleJSON)

	writeThemedRisk(t, env, "Orphaned S3 buckets", "ownership")
	writeThemedRisk(t, env, "Unowned CI runners", "ownership")

	evs := evaluations(t, app, tenant, ruleID)
	if len(evs) == 0 {
		t.Fatal("no evaluations after risk writes: the engine did not run")
	}
	for _, ev := range evs {
		if ev.Outcome == "fired" {
			t.Fatalf("rule fired below its min_risks threshold: %+v", ev)
		}
	}

	writeThemedRisk(t, env, "Shared admin account", "ownership")
	evs = evaluations(t, app, tenant, ruleID)
	if evs[0].Outcome != "fired" || evs[0].MetaRiskID == nil || evs[0].RiskCount != 3 {
		t.Fatalf("newest evaluation after third risk: got %+v, want fired with 3 risks and a meta-risk", evs[0])
	}
	metaRiskID := *evs[0].MetaRiskID

	writeThemedRisk(t, env, "Stale on-call rota", "ownership")
	evs = evaluations(t, app, tenant, ruleID)
	if evs[0].Outcome != "fired" || evs[0].RiskCount != 4 {
		t.Fatalf("newest evaluation after fourth risk: got %+v, want fired with 4 risks", evs[0])
	}
	if *evs[0].MetaRiskID != metaRiskID {
		t.Fatalf("fourth risk created a second meta-risk %s; want the window's %s updated", *evs[0].MetaRiskID, metaRiskID)
	}

	resp, body := get(t, env, "/v1/risks/"+metaRiskID.String()+"/aggregation")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET meta-risk aggregation: status %d; body=%v", resp.StatusCode, body)
	}

	if got := evaluations(t, app, tenant, otherID); len(got) != 0 {
		t.Fatalf("non-matching rule was evaluated by ownership writes: %+v", got)
	}
}

// POST /v1/aggregation-rules/evaluate sweeps every active rule: it writes an
// evaluation row for each, and fires a rule whose threshold the existing
// risks meet.
func TestEvaluateNow_SweepsActiveRules_1750(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshRiskTenant(t, admin)
	env := testServer(t, app, tenant)

	ruleID := activeRule(t, env, ownershipRuleJSON)
	otherID := activeRule(t, env, otherThemeRuleJSON)
	for _, title := range []string{"Orphaned S3 buckets", "Unowned CI runners", "Shared admin account"} {
		writeThemedRisk(t, env, title, "ownership")
	}

	resp, body := postPath(t, env, "/v1/aggregation-rules/evaluate", map[string]any{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST evaluate: status %d; body=%v", resp.StatusCode, body)
	}
	if cnt, _ := body["count"].(float64); int(cnt) != 2 {
		t.Fatalf("evaluate count: got %v, want 2 (one per active rule); body=%v", body["count"], body)
	}
	outcomes := map[string]string{}
	for _, e := range body["evaluations"].([]any) {
		em := e.(map[string]any)
		outcomes[em["rule_id"].(string)] = em["outcome"].(string)
		if em["outcome"] == "fired" && em["meta_risk_id"] == "" {
			t.Fatalf("fired evaluation has no meta_risk_id: %v", em)
		}
	}
	if outcomes[ruleID] != "fired" {
		t.Fatalf("ownership rule outcome: got %q, want fired", outcomes[ruleID])
	}
	if outcomes[otherID] != "no_match" {
		t.Fatalf("access-review rule outcome: got %q, want no_match", outcomes[otherID])
	}
}
