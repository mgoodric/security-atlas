//go:build integration

// #2958: a confirmed link_child_risks proposal writes one child_linked row
// on the parent per new link, and a confirmed unlink_child_risk one
// child_unlinked row, both by the confirmer. Filing a proposal writes none.

package mcpwriteproposals_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/dbtest"
)

func TestHTTP_RiskLinkProposals_AreAudited(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := dbtest.SeedTenant(t, admin, "mcp_write_proposals", "risk_audit_log", "risks")
	h := setupHTTPServer(t, tenant)
	ctx := context.Background()

	propose := func(tool, input string) string {
		t.Helper()
		body := fmt.Sprintf(`{"tool_name": %q, "tool_input": %s, "ai_model_name": "m", "ai_model_version": "v"}`, tool, input)
		resp, raw := doJSON(t, http.MethodPost, h.ts.URL+"/v1/mcp/write-proposals", h.bearer, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %s: %d %s", tool, resp.StatusCode, raw)
		}
		var env struct {
			Proposal struct {
				ID string `json:"id"`
			} `json:"proposal"`
		}
		_ = json.Unmarshal(raw, &env)
		return env.Proposal.ID
	}
	confirm := func(id string) {
		t.Helper()
		resp, raw := doJSON(t, http.MethodPost, h.ts.URL+"/v1/mcp/write-proposals/"+id+"/confirm", h.approverBearer, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("confirm %s: %d %s", id, resp.StatusCode, raw)
		}
	}
	// The confirmer is the approver bearer's principal (testjwt.ApproverFor),
	// not the viewer who filed the proposal.
	confirmer := "test-approver:" + tenant
	type row struct{ action, actor, child string }
	auditRows := func(parent uuid.UUID) []row {
		t.Helper()
		rs, err := admin.Query(ctx, `
			SELECT action_type, actor,
			       COALESCE(after_state->>'child_risk_id', before_state->>'child_risk_id')
			FROM risk_audit_log
			WHERE tenant_id = $1 AND risk_id = $2
			  AND action_type IN ('child_linked', 'child_unlinked')
			ORDER BY created_at, id`, tenant, parent)
		if err != nil {
			t.Fatalf("read audit rows: %v", err)
		}
		defer rs.Close()
		var out []row
		for rs.Next() {
			var r row
			if err := rs.Scan(&r.action, &r.actor, &r.child); err != nil {
				t.Fatalf("scan: %v", err)
			}
			out = append(out, r)
		}
		return out
	}

	company := seedLinkRisk(t, admin, tenant, "company")
	org := seedLinkRisk(t, admin, tenant, "org")
	team := seedLinkRisk(t, admin, tenant, "team")

	linkID := propose("link_child_risks",
		fmt.Sprintf(`{"parent_risk_id": %q, "child_risk_ids": [%q, %q]}`, company, org, team))
	if n := len(auditRows(company)); n != 0 {
		t.Fatalf("audit rows before approval = %d, want 0", n)
	}
	confirm(linkID)

	// A second proposal re-linking org writes nothing new.
	confirm(propose("link_child_risks",
		fmt.Sprintf(`{"parent_risk_id": %q, "child_risk_ids": [%q]}`, company, org)))

	confirm(propose("unlink_child_risk",
		fmt.Sprintf(`{"parent_risk_id": %q, "child_risk_id": %q}`, company, org)))

	rows := auditRows(company)
	if len(rows) != 3 {
		t.Fatalf("audit rows = %v, want 2 child_linked + 1 child_unlinked", rows)
	}
	linked := []string{rows[0].child, rows[1].child}
	slices.Sort(linked)
	want := []string{org.String(), team.String()}
	slices.Sort(want)
	for _, r := range rows[:2] {
		if r.action != "child_linked" || r.actor != confirmer {
			t.Errorf("link row = %+v, want child_linked by %q", r, confirmer)
		}
	}
	if !slices.Equal(linked, want) {
		t.Errorf("linked children = %v, want %v", linked, want)
	}
	if r := rows[2]; r.action != "child_unlinked" || r.actor != confirmer || r.child != org.String() {
		t.Errorf("unlink row = %+v, want child_unlinked of %s by %q", r, org, confirmer)
	}
}
