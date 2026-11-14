//go:build integration

// #1748: the link_child_risks and unlink_child_risk MCP proposals write
// nothing until a human confirms them, and on confirm they enforce the rules
// POST /v1/risks/{id}/children enforces (level order, no cycle, same
// tenant). A refused confirm rolls back and leaves the proposal pending.

package mcpwriteproposals_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
)

func seedLinkRisk(t *testing.T, admin *pgxpool.Pool, tenant, level string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO risks (
			id, tenant_id, title, description, category, methodology,
			inherent_score, treatment, treatment_owner, residual_score, level
		)
		VALUES ($1, $2, $3, '', 'operational', 'nist_800_30',
		        '{"likelihood":3,"impact":4}'::jsonb, 'untreated', '', '{}'::jsonb, $4::risk_level)
	`, id, tenant, level+" risk", level); err != nil {
		t.Fatalf("seed %s risk: %v", level, err)
	}
	return id
}

func TestHTTP_RiskLinkProposals_ApplyOnlyAfterApproval(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
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
	confirm := func(id string) (int, string) {
		t.Helper()
		resp, raw := doJSON(t, http.MethodPost, h.ts.URL+"/v1/mcp/write-proposals/"+id+"/confirm", h.approverBearer, "")
		var errBody struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(raw, &errBody)
		return resp.StatusCode, errBody.Code
	}
	links := func(parent uuid.UUID) int {
		t.Helper()
		var n int
		if err := admin.QueryRow(ctx,
			`SELECT count(*) FROM risk_aggregations WHERE tenant_id = $1 AND parent_risk_id = $2`,
			tenant, parent).Scan(&n); err != nil {
			t.Fatalf("count links: %v", err)
		}
		return n
	}
	state := func(id string) string {
		t.Helper()
		var s string
		if err := admin.QueryRow(ctx, `SELECT state FROM mcp_write_proposals WHERE id = $1`, id).Scan(&s); err != nil {
			t.Fatalf("read proposal: %v", err)
		}
		return s
	}

	company := seedLinkRisk(t, admin, tenant, "company")
	org := seedLinkRisk(t, admin, tenant, "org")
	team := seedLinkRisk(t, admin, tenant, "team")

	// Filing writes nothing; confirming applies.
	linkID := propose("link_child_risks",
		fmt.Sprintf(`{"parent_risk_id": %q, "child_risk_ids": [%q, %q]}`, company, org, team))
	if n := links(company); n != 0 {
		t.Fatalf("links before approval = %d, want 0", n)
	}
	if status, code := confirm(linkID); status != http.StatusOK {
		t.Fatalf("confirm link: %d %s", status, code)
	}
	if n := links(company); n != 2 {
		t.Fatalf("links after approval = %d, want 2", n)
	}

	// Same level: refused with the API's 400, nothing written, still pending.
	sameLevelID := propose("link_child_risks",
		fmt.Sprintf(`{"parent_risk_id": %q, "child_risk_ids": [%q]}`, org, seedLinkRisk(t, admin, tenant, "org")))
	if status, code := confirm(sameLevelID); status != http.StatusBadRequest || code != "hierarchy_validation_failed" {
		t.Fatalf("same-level confirm: %d %q, want 400 hierarchy_validation_failed", status, code)
	}
	if s := state(sameLevelID); s != "ai_proposed" {
		t.Fatalf("refused proposal state = %q, want ai_proposed", s)
	}
	if n := links(org); n != 0 {
		t.Fatalf("links under org after refusal = %d, want 0", n)
	}

	// Cycle: company is team's parent, so company under team is a 409.
	if _, err := admin.Exec(ctx, `UPDATE risks SET level = 'company' WHERE id = $1`, team); err != nil {
		t.Fatalf("lift team: %v", err)
	}
	if _, err := admin.Exec(ctx, `UPDATE risks SET level = 'team' WHERE id = $1`, company); err != nil {
		t.Fatalf("drop company: %v", err)
	}
	cycleID := propose("link_child_risks",
		fmt.Sprintf(`{"parent_risk_id": %q, "child_risk_ids": [%q]}`, team, company))
	if status, code := confirm(cycleID); status != http.StatusConflict || code != "hierarchy_cycle" {
		t.Fatalf("cycle confirm: %d %q, want 409 hierarchy_cycle", status, code)
	}
	if n := links(team); n != 0 {
		t.Fatalf("links under team after cycle refusal = %d, want 0", n)
	}

	// Unlink: filing writes nothing; confirming detaches; a second detach of
	// the same child is a 404.
	unlinkID := propose("unlink_child_risk",
		fmt.Sprintf(`{"parent_risk_id": %q, "child_risk_id": %q}`, company, org))
	if n := links(company); n != 2 {
		t.Fatalf("links before unlink approval = %d, want 2", n)
	}
	if status, code := confirm(unlinkID); status != http.StatusOK {
		t.Fatalf("confirm unlink: %d %s", status, code)
	}
	if n := links(company); n != 1 {
		t.Fatalf("links after unlink approval = %d, want 1", n)
	}
	againID := propose("unlink_child_risk",
		fmt.Sprintf(`{"parent_risk_id": %q, "child_risk_id": %q}`, company, org))
	if status, code := confirm(againID); status != http.StatusNotFound || code != "link_not_found" {
		t.Fatalf("second unlink confirm: %d %q, want 404 link_not_found", status, code)
	}
}
