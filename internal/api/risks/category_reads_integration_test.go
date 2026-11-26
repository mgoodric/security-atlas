//go:build integration

// #1812 — the risk API reads the category from the reference (migrate step
// of #1745). Runs against a real Postgres with the shared fixture of #1745
// (taxFixture): two tenants with risks across several of the seven seeded
// categories, an aggregation rule targeting a theme, risks tagged with that
// theme, and the meta-risk the rule produced.
//
//   - the risk response carries the category's slug, id and name, read from
//     the reference (a rename shows up on every risk in the category);
//   - ?category= takes one slug or several, per tenant;
//   - an archived category is refused for a new risk and for a move onto
//     it, while a risk already on it reads fine and can be moved off it;
//   - an unknown slug is a 400 naming the valid slugs.

package risks_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// riskIDsByCategory reads the tenant's risk ids in one category straight
// from the table, by the slug of the category each references.
func riskIDsByCategory(t *testing.T, admin *pgxpool.Pool, tenant, category string) []string {
	t.Helper()
	rows, err := admin.Query(context.Background(), `
		SELECT r.id::text FROM risks r
		JOIN org_risk_categories c ON c.tenant_id = r.tenant_id AND c.id = r.category_id
		WHERE r.tenant_id = $1 AND c.slug = $2 ORDER BY r.id`,
		tenant, category)
	if err != nil {
		t.Fatalf("read %s risks: %v", category, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan risk id: %v", err)
		}
		out = append(out, id)
	}
	return out
}

// listRiskIDs calls GET /v1/risks with query and returns the risk ids,
// sorted, checking that count equals the rows returned.
func listRiskIDs(t *testing.T, env taxEnv, query string) []string {
	t.Helper()
	status, body := taxCall(t, env, http.MethodGet, "/v1/risks"+query, env.admin, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /v1/risks%s: status %d (%v)", query, status, body)
	}
	raw, _ := body["risks"].([]any)
	if body["count"] != float64(len(raw)) {
		t.Errorf("GET /v1/risks%s: count %v, rows %d", query, body["count"], len(raw))
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		out = append(out, item.(map[string]any)["id"].(string))
	}
	slices.Sort(out)
	return out
}

func sortedUnion(lists ...[]string) []string {
	var out []string
	for _, l := range lists {
		out = append(out, l...)
	}
	slices.Sort(out)
	return out
}

func TestRiskCategoryReads_ResponseCarriesSlugAndName(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	env := taxServer(t, dbtest.NewAppPool(t), taxFixture(t, admin))

	// Rename privacy: the name now differs from anything the enum holds,
	// so a response carrying it was read from the reference.
	if status, body := taxCall(t, env, http.MethodPatch, "/v1/risks/categories/privacy", env.admin,
		map[string]any{"name": "Data Protection"}); status != http.StatusOK {
		t.Fatalf("rename privacy: status %d (%v)", status, body)
	}
	privacyID := taxList(t, env, "")["privacy"]["id"]
	riskID := riskIDsByCategory(t, admin, env.tenant, "privacy")[0]

	status, body := taxCall(t, env, http.MethodGet, "/v1/risks/"+riskID, env.admin, nil)
	if status != http.StatusOK {
		t.Fatalf("GET risk: status %d (%v)", status, body)
	}
	got := body["risk"].(map[string]any)
	if got["category"] != "privacy" || got["category_name"] != "Data Protection" || got["category_id"] != privacyID {
		t.Fatalf("GET risk: category=%v category_name=%v category_id=%v, want privacy / Data Protection / %v",
			got["category"], got["category_name"], got["category_id"], privacyID)
	}

	status, body = taxCall(t, env, http.MethodGet, "/v1/risks", env.admin, nil)
	if status != http.StatusOK {
		t.Fatalf("list risks: status %d (%v)", status, body)
	}
	for _, item := range body["risks"].([]any) {
		r := item.(map[string]any)
		if r["category"] == "" || r["category_name"] == "" || r["category_id"] == "" {
			t.Errorf("listed risk %v has no category slug/name/id: %v", r["id"], r)
		}
		if r["id"] == riskID && r["category_name"] != "Data Protection" {
			t.Errorf("listed privacy risk: category_name = %v, want Data Protection", r["category_name"])
		}
	}

	// A risk created now carries the same shape.
	status, body = taxCall(t, env, http.MethodPost, "/v1/risks", env.admin, map[string]any{
		"title": "new privacy risk", "category": "privacy",
		"inherent_score": map[string]int{"likelihood": 2, "impact": 2},
	})
	if status != http.StatusCreated {
		t.Fatalf("create: status %d (%v)", status, body)
	}
	created := body["risk"].(map[string]any)
	if created["category"] != "privacy" || created["category_name"] != "Data Protection" {
		t.Fatalf("create: category=%v category_name=%v, want privacy / Data Protection",
			created["category"], created["category_name"])
	}
}

func TestRiskCategoryReads_FilterBySlugPerTenant(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	envA := taxServer(t, app, taxFixture(t, admin))
	envB := taxServer(t, app, taxFixture(t, admin))
	// Tenant B gets a second privacy risk, so the two tenants' answers
	// differ in size as well as in ids.
	taxSeedRisk(t, admin, envB.tenant, "privacy", []string{})

	for _, env := range []taxEnv{envA, envB} {
		privacy := riskIDsByCategory(t, admin, env.tenant, "privacy")
		operational := riskIDsByCategory(t, admin, env.tenant, "operational")
		regulatory := riskIDsByCategory(t, admin, env.tenant, "regulatory")
		availability := riskIDsByCategory(t, admin, env.tenant, "availability")
		privacyID := taxList(t, env, "")["privacy"]["id"].(string)

		cases := []struct {
			query string
			want  []string
		}{
			{"?category=privacy", sortedUnion(privacy)},
			{"?category=" + privacyID, sortedUnion(privacy)},
			{"?category=privacy,regulatory", sortedUnion(privacy, regulatory)},
			{"?category=privacy&category=operational", sortedUnion(privacy, operational)},
			// The availability risk is the rule's meta-risk, a roll-up:
			// left out by default (#2060), returned on request.
			{"?category=availability", sortedUnion()},
			{"?category=availability&include_rollups=true", sortedUnion(availability)},
			{"?category=financial", sortedUnion()},
		}
		for _, tc := range cases {
			got := listRiskIDs(t, env, tc.query)
			if !slices.Equal(got, tc.want) {
				t.Errorf("tenant %s %s: got %v, want %v", env.tenant, tc.query, got, tc.want)
			}
		}
	}
	if a, b := len(listRiskIDs(t, envA, "?category=privacy")), len(listRiskIDs(t, envB, "?category=privacy")); a != 1 || b != 2 {
		t.Errorf("privacy filter: tenant A %d risks, tenant B %d, want 1 and 2", a, b)
	}

	// Tenant A's category id is not a valid filter for tenant B.
	aPrivacy := taxList(t, envA, "")["privacy"]["id"].(string)
	if status, body := taxCall(t, envB, http.MethodGet, "/v1/risks?category="+aPrivacy, envB.admin, nil); status != http.StatusBadRequest {
		t.Fatalf("tenant B filtering by tenant A's category id: status %d (%v), want 400", status, body)
	}
}

func TestRiskCategoryReads_ArchivedCategoryRefusedForNewAssignments(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	env := taxServer(t, dbtest.NewAppPool(t), taxFixture(t, admin))
	riskID := riskIDsByCategory(t, admin, env.tenant, "regulatory")[0]
	otherID := riskIDsByCategory(t, admin, env.tenant, "privacy")[0]

	if status, body := taxCall(t, env, http.MethodPost, "/v1/risks/categories/regulatory/archive", env.admin, nil); status != http.StatusOK {
		t.Fatalf("archive regulatory: status %d (%v)", status, body)
	}

	// A new risk on it is refused.
	if status, body := taxCall(t, env, http.MethodPost, "/v1/risks", env.admin, map[string]any{
		"title": "new regulatory risk", "category": "regulatory",
		"inherent_score": map[string]int{"likelihood": 2, "impact": 2},
	}); status != http.StatusBadRequest {
		t.Fatalf("create on archived category: status %d (%v), want 400", status, body)
	}
	// So is moving another risk onto it.
	if status, body := taxCall(t, env, http.MethodPatch, "/v1/risks/"+otherID, env.admin,
		map[string]any{"category": "regulatory"}); status != http.StatusBadRequest {
		t.Fatalf("move onto archived category: status %d (%v), want 400", status, body)
	}

	// The existing risk still reads fine, and is still filterable.
	status, body := taxCall(t, env, http.MethodGet, "/v1/risks/"+riskID, env.admin, nil)
	if status != http.StatusOK {
		t.Fatalf("GET risk on archived category: status %d (%v)", status, body)
	}
	if got := body["risk"].(map[string]any); got["category"] != "regulatory" || got["category_name"] != "Regulatory" {
		t.Fatalf("GET risk on archived category: category=%v category_name=%v", got["category"], got["category_name"])
	}
	if got := listRiskIDs(t, env, "?category=regulatory"); !slices.Equal(got, []string{riskID}) {
		t.Fatalf("filter by archived category: got %v, want [%s]", got, riskID)
	}

	// And it can be moved off it.
	status, body = taxCall(t, env, http.MethodPatch, "/v1/risks/"+riskID, env.admin,
		map[string]any{"category": "operational"})
	if status != http.StatusOK {
		t.Fatalf("move off archived category: status %d (%v)", status, body)
	}
	if got := body["risk"].(map[string]any); got["category"] != "operational" || got["category_name"] != "Operational" {
		t.Fatalf("move off archived category: category=%v category_name=%v", got["category"], got["category_name"])
	}
	if slug, _, _ := storedCategory(t, admin, riskID); slug != "operational" {
		t.Fatalf("after move: stored reference slug=%s, want operational", slug)
	}
	var audited int
	if err := admin.QueryRow(context.Background(), `
		SELECT count(*) FROM risk_audit_log
		WHERE tenant_id = $1 AND risk_id = $2 AND action_type = 'category_changed'
		  AND before_state->>'category' = 'regulatory' AND after_state->>'category' = 'operational'`,
		env.tenant, riskID).Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("category_changed audit rows = %d (%v), want 1", audited, err)
	}

	// Once off it, the risk cannot go back.
	if status, body := taxCall(t, env, http.MethodPatch, "/v1/risks/"+riskID, env.admin,
		map[string]any{"category": "regulatory"}); status != http.StatusBadRequest {
		t.Fatalf("move back onto archived category: status %d (%v), want 400", status, body)
	}
}

func TestRiskCategoryReads_UnknownSlugListsValidSlugs(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	env := taxServer(t, dbtest.NewAppPool(t), taxFixture(t, admin))
	riskID := riskIDsByCategory(t, admin, env.tenant, "privacy")[0]

	calls := []struct {
		name, method, path string
		payload            any
	}{
		{"filter", http.MethodGet, "/v1/risks?category=technical", nil},
		{"filter, second of two", http.MethodGet, "/v1/risks?category=privacy,technical", nil},
		{"create", http.MethodPost, "/v1/risks", map[string]any{
			"title": "unknown", "category": "technical",
			"inherent_score": map[string]int{"likelihood": 2, "impact": 2},
		}},
		{"move", http.MethodPatch, "/v1/risks/" + riskID, map[string]any{"category": "technical"}},
	}
	for _, c := range calls {
		status, body := taxCall(t, env, c.method, c.path, env.admin, c.payload)
		if status != http.StatusBadRequest {
			t.Fatalf("%s: status %d (%v), want 400", c.name, status, body)
		}
		msg, _ := body["error"].(string)
		if !strings.Contains(msg, `"technical"`) {
			t.Errorf("%s: error %q does not name the unknown slug", c.name, msg)
		}
		for _, slug := range seededCategorySlugs {
			if !strings.Contains(msg, slug) {
				t.Errorf("%s: error %q does not name valid slug %s", c.name, msg, slug)
			}
		}
	}

	// A bare id that is no category of the tenant's is unknown too.
	if status, body := taxCall(t, env, http.MethodGet, "/v1/risks?category="+uuid.NewString(), env.admin, nil); status != http.StatusBadRequest {
		t.Fatalf("unknown id filter: status %d (%v), want 400", status, body)
	}
}
