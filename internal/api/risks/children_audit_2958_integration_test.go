//go:build integration

// #2958: risk hierarchy links are audited. POST /v1/risks/{id}/children
// writes one child_linked row on the parent per new link, by the caller; a
// re-link writes none; DELETE /v1/risks/{id}/children/{child_id} writes one
// child_unlinked row. The rows reach the unified audit log and its export.
// Real Postgres with RLS + the assembled router.
//
// Run with:
//
//	go test -tags=integration ./internal/api/risks/ -run ChildrenAudit

package risks_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// childAuditRow is one child_linked / child_unlinked row as stored.
type childAuditRow struct {
	Action string
	Actor  string
	Before map[string]any
	After  map[string]any
}

// childAuditRows reads parent's hierarchy audit rows, oldest first.
func childAuditRows(t *testing.T, admin *pgxpool.Pool, tenant string, parent uuid.UUID) []childAuditRow {
	t.Helper()
	rows, err := admin.Query(context.Background(), `
		SELECT action_type, actor, before_state, after_state
		FROM risk_audit_log
		WHERE tenant_id = $1 AND risk_id = $2
		  AND action_type IN ('child_linked', 'child_unlinked')
		ORDER BY created_at, id`, tenant, parent)
	if err != nil {
		t.Fatalf("read hierarchy audit rows: %v", err)
	}
	defer rows.Close()
	var out []childAuditRow
	for rows.Next() {
		var (
			r             childAuditRow
			before, after []byte
		)
		if err := rows.Scan(&r.Action, &r.Actor, &before, &after); err != nil {
			t.Fatalf("scan audit row: %v", err)
		}
		_ = json.Unmarshal(before, &r.Before)
		_ = json.Unmarshal(after, &r.After)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("audit rows: %v", err)
	}
	return out
}

func TestChildrenAudit_LinkAndUnlinkWriteOneRowEach(t *testing.T) {
	env := newTransitionEnv(t)
	company := seedLeveledRisk(t, env.admin, env.tenant, "company", "enterprise risk", 2, 2)
	teamA := seedLeveledRisk(t, env.admin, env.tenant, "team", "team finding A", 3, 4)
	teamB := seedLeveledRisk(t, env.admin, env.tenant, "team", "team finding B", 5, 5)
	path := "/v1/risks/" + company.String() + "/children"

	// Two new children, one named twice: two rows.
	if code, body := env.do(t, http.MethodPost, path, env.ownerJWT,
		map[string]any{"child_risk_ids": []string{teamA.String(), teamB.String(), teamA.String()}}); code != http.StatusOK {
		t.Fatalf("link: status %d: %v", code, body)
	}
	// A re-link of an existing child writes none.
	if code, body := env.do(t, http.MethodPost, path, env.ownerJWT,
		map[string]any{"child_risk_ids": []string{teamA.String()}}); code != http.StatusOK {
		t.Fatalf("re-link: status %d: %v", code, body)
	}
	if code, body := env.do(t, http.MethodDelete, path+"/"+teamA.String(), env.ownerJWT, nil); code != http.StatusNoContent {
		t.Fatalf("unlink: status %d: %v", code, body)
	}
	// A refused unlink writes none.
	if code, _ := env.do(t, http.MethodDelete, path+"/"+teamA.String(), env.ownerJWT, nil); code != http.StatusNotFound {
		t.Fatalf("second unlink: status %d, want 404", code)
	}

	rows := childAuditRows(t, env.admin, env.tenant, company)
	var actions, linked []string
	for _, r := range rows {
		actions = append(actions, r.Action)
		if r.Actor != env.owner.String() {
			t.Errorf("%s actor = %q, want the caller %s", r.Action, r.Actor, env.owner)
		}
		if r.Action == "child_linked" {
			if r.Before["child_risk_id"] != nil {
				t.Errorf("child_linked before = %v, want child_risk_id null", r.Before)
			}
			linked = append(linked, r.After["child_risk_id"].(string))
		}
	}
	if !slices.Equal(actions, []string{"child_linked", "child_linked", "child_unlinked"}) {
		t.Fatalf("audit actions = %v, want [child_linked child_linked child_unlinked]", actions)
	}
	slices.Sort(linked)
	want := []string{teamA.String(), teamB.String()}
	slices.Sort(want)
	if !slices.Equal(linked, want) {
		t.Errorf("linked children = %v, want %v", linked, want)
	}
	un := rows[2]
	if un.Before["child_risk_id"] != teamA.String() || un.After["child_risk_id"] != nil {
		t.Errorf("child_unlinked = %v -> %v, want %s -> null", un.Before, un.After, teamA)
	}
	if _, ok := un.After["child_risk_id"]; !ok {
		t.Errorf("child_unlinked after = %v, want child_risk_id: null", un.After)
	}

	// A refused link (same level) writes none.
	org := seedLeveledRisk(t, env.admin, env.tenant, "org", "org risk", 2, 2)
	if code, _ := env.do(t, http.MethodPost, "/v1/risks/"+org.String()+"/children", env.ownerJWT,
		map[string]any{"child_risk_ids": []string{company.String()}}); code != http.StatusBadRequest {
		t.Fatalf("level-violating link: status %d, want 400", code)
	}
	if n := len(childAuditRows(t, env.admin, env.tenant, org)); n != 0 {
		t.Fatalf("audit rows after refused link = %d, want 0", n)
	}
}

// The new actions reach the unified audit log and its export, which read
// risk_audit_log as they read every other action.
func TestChildrenAudit_ReachUnifiedLogAndExport(t *testing.T) {
	env := newTransitionEnv(t)
	org := seedLeveledRisk(t, env.admin, env.tenant, "org", "org risk", 2, 2)
	team := seedLeveledRisk(t, env.admin, env.tenant, "team", "team risk", 3, 3)
	path := "/v1/risks/" + org.String() + "/children"
	if code, body := env.do(t, http.MethodPost, path, env.ownerJWT,
		map[string]any{"child_risk_ids": []string{team.String()}}); code != http.StatusOK {
		t.Fatalf("link: status %d: %v", code, body)
	}
	if code, body := env.do(t, http.MethodDelete, path+"/"+team.String(), env.ownerJWT, nil); code != http.StatusNoContent {
		t.Fatalf("unlink: status %d: %v", code, body)
	}

	now := time.Now().UTC()
	q := url.Values{
		"from": {now.Add(-time.Hour).Format(time.RFC3339)},
		"to":   {now.Add(time.Hour).Format(time.RFC3339)},
		"kind": {"risk"},
	}
	code, body := env.do(t, http.MethodGet, "/v1/admin/audit-log/unified?"+q.Encode(), env.ownerJWT, nil)
	if code != http.StatusOK {
		t.Fatalf("unified audit log: status %d: %v", code, body)
	}
	seen := map[string]bool{}
	entries, _ := body["entries"].([]any)
	for _, raw := range entries {
		action, _ := raw.(map[string]any)["action"].(string)
		seen[action] = true
	}
	if !seen["child_linked"] || !seen["child_unlinked"] {
		t.Fatalf("unified entries = %v, want child_linked and child_unlinked", entries)
	}

	q.Set("format", "csv")
	req, _ := http.NewRequest(http.MethodGet, env.server.URL+"/v1/admin/audit-log/export?"+q.Encode(), nil)
	req.Header.Set("Authorization", "Bearer "+env.ownerJWT)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	csv, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export: status %d: %s", resp.StatusCode, csv)
	}
	if !strings.Contains(string(csv), "child_linked") || !strings.Contains(string(csv), "child_unlinked") {
		t.Fatalf("export lacks child_linked/child_unlinked:\n%s", csv)
	}
}
