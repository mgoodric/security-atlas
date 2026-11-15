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

// #1815: create_risk takes a category by slug or by display name, the name
// matched case-insensitively on the trimmed value and only within the
// caller's tenant. An unknown category is refused with the valid slugs; a
// name shared by several categories is refused with theirs.
func TestHTTP_ConfirmCreateRisk_CategoryByNameOrSlug(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := dbtest.SeedTenant(t, admin, "mcp_write_proposals", "risks", "org_risk_categories")
	other := dbtest.SeedTenant(t, admin, "org_risk_categories")
	h := setupHTTPServer(t, tenant)
	ctx := context.Background()

	for _, tn := range []string{tenant, other} {
		if _, err := admin.Exec(ctx, `SELECT seed_risk_categories($1)`, tn); err != nil {
			t.Fatalf("seed categories: %v", err)
		}
	}
	// Another tenant's category name must not resolve here.
	if _, err := admin.Exec(ctx, `UPDATE org_risk_categories SET name = 'Elsewhere'
		WHERE tenant_id = $1 AND slug = 'financial'`, other); err != nil {
		t.Fatalf("rename other tenant's category: %v", err)
	}
	// `availability` is archived and renamed to collide with active
	// `integrity`'s name, so "Integrity" matches two categories.
	if _, err := admin.Exec(ctx, `UPDATE org_risk_categories SET archived = true, name = 'Integrity'
		WHERE tenant_id = $1 AND slug = 'availability'`, tenant); err != nil {
		t.Fatalf("archive availability: %v", err)
	}

	proposeAndConfirm := func(title, category string) (*http.Response, []byte) {
		t.Helper()
		body := fmt.Sprintf(`{"tool_name": "create_risk", "tool_input": {"title": %q, "category": %q}, "ai_model_name": "m", "ai_model_version": "v"}`, title, category)
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

	for _, tc := range []struct{ title, category, wantSlug string }{
		{"by slug", "privacy", "privacy"},
		{"by name", "Regulatory", "regulatory"},
		{"by name, any case, padded", "  oPeRaTiOnAl ", "operational"},
	} {
		resp, raw := proposeAndConfirm(tc.title, tc.category)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("confirm create_risk %s (%q): %d %s", tc.title, tc.category, resp.StatusCode, raw)
		}
		var slug string
		if err := admin.QueryRow(ctx, `
			SELECT c.slug FROM risks r
			JOIN org_risk_categories c ON c.tenant_id = r.tenant_id AND c.id = r.category_id
			WHERE r.tenant_id = $1 AND r.title = $2`, tenant, tc.title).Scan(&slug); err != nil {
			t.Fatalf("read risk %s: %v", tc.title, err)
		}
		if slug != tc.wantSlug {
			t.Fatalf("risk %s category = %s, want %s", tc.title, slug, tc.wantSlug)
		}
	}

	for _, tc := range []struct{ category, want string }{
		// Valid list: the tenant's active slugs (availability is archived).
		{"technical", "valid categories: confidentiality, financial, integrity, operational, privacy, regulatory"},
		{"Elsewhere", "unknown category"},
		{"Integrity", "name one by slug: availability, integrity"},
	} {
		resp, raw := proposeAndConfirm("refused "+tc.category, tc.category)
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(raw), tc.want) {
			t.Fatalf("confirm create_risk in %q: status %d body %s; want 400 mentioning %q", tc.category, resp.StatusCode, raw, tc.want)
		}
	}
}
