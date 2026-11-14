//go:build integration

// #1810: a create_risk proposal resolves its category the way POST
// /v1/risks does. A deleted or archived category is a 400 on confirm, not a
// 500 from the category sync trigger, and nothing is written; a valid one
// stores the tenant's category reference.

package mcpwriteproposals_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/dbtest"
)

func TestHTTP_ConfirmCreateRisk_ResolvesCategory(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := dbtest.SeedTenant(t, admin, "mcp_write_proposals", "risks", "org_risk_categories")
	h := setupHTTPServer(t, tenant)
	ctx := context.Background()

	// The tenant deletes `financial` (unused) and archives `privacy`.
	if _, err := admin.Exec(ctx, `SELECT seed_risk_categories($1)`, tenant); err != nil {
		t.Fatalf("seed categories: %v", err)
	}
	if _, err := admin.Exec(ctx,
		`DELETE FROM org_risk_categories WHERE tenant_id = $1 AND slug = 'financial'`, tenant); err != nil {
		t.Fatalf("delete financial: %v", err)
	}
	if _, err := admin.Exec(ctx,
		`UPDATE org_risk_categories SET archived = true WHERE tenant_id = $1 AND slug = 'privacy'`, tenant); err != nil {
		t.Fatalf("archive privacy: %v", err)
	}

	proposeAndConfirm := func(category string) (*http.Response, []byte) {
		t.Helper()
		body := fmt.Sprintf(`{"tool_name": "create_risk", "tool_input": {"title": "t", "category": %q}, "ai_model_name": "m", "ai_model_version": "v"}`, category)
		resp, raw := doJSON(t, http.MethodPost, h.ts.URL+"/v1/mcp/write-proposals", h.bearer, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create proposal: %d %s", resp.StatusCode, raw)
		}
		var env struct {
			Proposal struct {
				ID string `json:"id"`
			} `json:"proposal"`
		}
		_ = json.Unmarshal(raw, &env)
		return doJSON(t, http.MethodPost, h.ts.URL+"/v1/mcp/write-proposals/"+env.Proposal.ID+"/confirm", h.approverBearer, "")
	}

	for category, want := range map[string]string{"financial": "unknown category", "privacy": "archived"} {
		resp, raw := proposeAndConfirm(category)
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(raw), want) {
			t.Fatalf("confirm create_risk in %s: status %d body %s; want 400 mentioning %q", category, resp.StatusCode, raw, want)
		}
	}
	var risks int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM risks WHERE tenant_id = $1`, tenant).Scan(&risks); err != nil || risks != 0 {
		t.Fatalf("risks after refused confirms = %d (%v), want 0", risks, err)
	}

	resp, raw := proposeAndConfirm("operational")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("confirm create_risk in operational: %d %s", resp.StatusCode, raw)
	}
	var enum, slug string
	if err := admin.QueryRow(ctx, `
		SELECT r.category::text, c.slug FROM risks r
		JOIN org_risk_categories c ON c.tenant_id = r.tenant_id AND c.id = r.category_id
		WHERE r.tenant_id = $1`, tenant).Scan(&enum, &slug); err != nil {
		t.Fatalf("read created risk: %v", err)
	}
	if enum != "operational" || slug != "operational" {
		t.Fatalf("created risk category enum=%s ref=%s, want operational for both", enum, slug)
	}
}
