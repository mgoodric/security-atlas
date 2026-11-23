//go:build integration

// #2835 — every risk create path leaves a `created` row in the risk's
// audit trail (POST /v1/risks, POST /v1/risks/aggregate, an applied MCP
// create_risk proposal), naming the actor and the risk's initial title,
// category, treatment and owner; and assigning or removing a theme writes
// a `themes_changed` row. Real Postgres under RLS + the assembled router.

package risks_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// rawAuditRows reads the risk's whole audit trail oldest first, the created
// row included (auditRows drops it).
func (env transitionEnv) rawAuditRows(t *testing.T, id string) []map[string]any {
	t.Helper()
	code, body := env.do(t, http.MethodGet, "/v1/risks/"+id+"/audit-log", env.ownerJWT, nil)
	if code != http.StatusOK {
		t.Fatalf("audit-log: status %d: %v", code, body)
	}
	raw, _ := body["entries"].([]any)
	out := make([]map[string]any, len(raw))
	for i, e := range raw {
		out[len(raw)-1-i] = e.(map[string]any)
	}
	return out
}

// createdRowTenant is the tenant_id the risk's created row holds, read past
// RLS, so a test sees the row is the risk's own tenant's.
func (env transitionEnv) createdRowTenant(t *testing.T, id string) string {
	t.Helper()
	var tenant string
	if err := env.admin.QueryRow(context.Background(),
		`SELECT tenant_id::text FROM risk_audit_log WHERE risk_id = $1 AND action_type = 'created'`,
		id).Scan(&tenant); err != nil {
		t.Fatalf("read created row tenant: %v", err)
	}
	return tenant
}

// checkCreated fails unless rows is exactly one created row by actor whose
// after state holds want.
func checkCreated(t *testing.T, rows []map[string]any, actor string, want map[string]any) {
	t.Helper()
	if len(rows) != 1 || rows[0]["action"] != "created" {
		t.Fatalf("audit rows = %v, want one created row", rows)
	}
	row := rows[0]
	if row["actor"] != actor {
		t.Errorf("created actor = %v, want %s", row["actor"], actor)
	}
	if before, _ := row["before"].(map[string]any); len(before) != 0 {
		t.Errorf("created before = %v, want {}", before)
	}
	after, _ := row["after"].(map[string]any)
	for k, v := range want {
		if after[k] != v {
			t.Errorf("created after[%s] = %v, want %v (after = %v)", k, after[k], v, after)
		}
	}
	for _, k := range []string{"title", "category_id", "treatment", "owner_user_id", "treatment_owner"} {
		if _, ok := after[k]; !ok {
			t.Errorf("created after has no %s: %v", k, after)
		}
	}
}

func TestCreatedAudit_RESTCreate_2835(t *testing.T) {
	env := newTransitionEnv(t)
	code, body := env.do(t, http.MethodPost, "/v1/risks", env.ownerJWT, map[string]any{
		"title":          "Created over REST",
		"category":       "operational",
		"inherent_score": map[string]int{"likelihood": 2, "impact": 3},
		"owner_user_id":  env.approver.String(),
	})
	if code != http.StatusCreated {
		t.Fatalf("create: status %d: %v", code, body)
	}
	id := riskField(t, body, "id").(string)

	checkCreated(t, env.rawAuditRows(t, id), env.owner.String(), map[string]any{
		"title":           "Created over REST",
		"category_id":     riskField(t, body, "category_id"),
		"treatment":       "untreated",
		"owner_user_id":   env.approver.String(),
		"treatment_owner": "",
	})
	if got := env.createdRowTenant(t, id); got != env.tenant {
		t.Errorf("created row tenant = %s, want %s", got, env.tenant)
	}
}

func TestCreatedAudit_MCPCreateRisk_2835(t *testing.T) {
	env := newTransitionEnv(t)
	raw, _ := json.Marshal(map[string]any{
		"title":           "Created over MCP",
		"category":        "operational",
		"treatment_owner": "platform team",
		"inherent_score":  map[string]int{"likelihood": 2, "impact": 2},
	})
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

	// The approver confirms, so the created row names the confirming
	// person, not the proposal's filer.
	confirmed := env.confirm(t, proposalID, env.apprJWT, http.StatusOK)
	id, _ := confirmed["proposal"].(map[string]any)["applied_subject"].(string)
	if id == "" {
		t.Fatalf("confirmed proposal has no applied_subject: %v", confirmed)
	}
	_, got := env.do(t, http.MethodGet, "/v1/risks/"+id, env.ownerJWT, nil)

	checkCreated(t, env.rawAuditRows(t, id), env.approver.String(), map[string]any{
		"title":           "Created over MCP",
		"category_id":     riskField(t, got, "category_id"),
		"treatment":       "untreated",
		"owner_user_id":   nil,
		"treatment_owner": "platform team",
	})
	if tenant := env.createdRowTenant(t, id); tenant != env.tenant {
		t.Errorf("created row tenant = %s, want %s", tenant, env.tenant)
	}
}

func TestCreatedAudit_ManualAggregate_2835(t *testing.T) {
	env := newTransitionEnv(t)
	a, b := env.newRisk(t), env.newRisk(t)
	code, body := env.do(t, http.MethodPost, "/v1/risks/aggregate", env.apprJWT, map[string]any{
		"parent": map[string]any{
			"title":             "Aggregated parent",
			"level":             "org",
			"severity_function": "max",
		},
		"child_risk_ids": []string{a, b},
	})
	if code != http.StatusCreated {
		t.Fatalf("aggregate: status %d: %v", code, body)
	}
	id := riskField(t, body, "id").(string)

	// The parent's trail also holds a child_linked row per child (#2961),
	// written in the same transaction, so the order among them is not
	// fixed.
	var created []map[string]any
	linked := 0
	for _, r := range env.rawAuditRows(t, id) {
		switch r["action"] {
		case "created":
			created = append(created, r)
		case "child_linked":
			linked++
		default:
			t.Errorf("unexpected audit row %v", r)
		}
	}
	if linked != 2 {
		t.Errorf("child_linked rows = %d, want 2", linked)
	}
	checkCreated(t, created, env.approver.String(), map[string]any{
		"title":         "Aggregated parent",
		"category_id":   riskField(t, body, "category_id"),
		"treatment":     "untreated",
		"owner_user_id": nil,
	})
}

func TestThemesChanged_AssignAndRemoveAreLogged_2835(t *testing.T) {
	env := newTransitionEnv(t)
	id := env.newRisk(t)

	assign := func(themes ...string) {
		t.Helper()
		code, body := env.do(t, http.MethodPost, "/v1/risks/"+id+"/themes", env.ownerJWT, map[string]any{"themes": themes})
		if code != http.StatusOK {
			t.Fatalf("assign %v: status %d: %v", themes, code, body)
		}
	}
	remove := func(theme string) {
		t.Helper()
		code, body := env.do(t, http.MethodDelete, "/v1/risks/"+id+"/themes/"+theme, env.ownerJWT, nil)
		if code != http.StatusNoContent {
			t.Fatalf("remove %s: status %d: %v", theme, code, body)
		}
	}
	assign("ownership")
	assign("ownership") // the same set: no row
	assign("monitoring")
	remove("ownership")
	remove("ownership") // absent: no row

	rows := env.auditRows(t, id)
	type change struct{ before, after string }
	want := []change{
		{"[]", "[ownership]"},
		{"[ownership]", "[monitoring ownership]"},
		{"[monitoring ownership]", "[monitoring]"},
	}
	if len(rows) != len(want) {
		t.Fatalf("audit rows = %v, want %d themes_changed rows", rows, len(want))
	}
	for i, w := range want {
		r := rows[i]
		if r["action"] != "themes_changed" || r["actor"] != env.owner.String() {
			t.Fatalf("row %d = %v, want themes_changed by %s", i, r, env.owner)
		}
		before := r["before"].(map[string]any)["themes"]
		after := r["after"].(map[string]any)["themes"]
		if got := jsonString(t, before); got != jsonString(t, splitThemes(w.before)) {
			t.Errorf("row %d before = %s, want %s", i, got, w.before)
		}
		if got := jsonString(t, after); got != jsonString(t, splitThemes(w.after)) {
			t.Errorf("row %d after = %s, want %s", i, got, w.after)
		}
	}
}

func jsonString(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %v: %v", v, err)
	}
	return string(b)
}

// splitThemes turns "[a b]" into ["a","b"], decoded as JSON decodes them.
func splitThemes(s string) []any {
	out := []any{}
	cur := ""
	for _, r := range s[1 : len(s)-1] {
		if r == ' ' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
