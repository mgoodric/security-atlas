//go:build integration

// #1810 — organisation category management (API). Runs against a real
// Postgres with the shared fixture of #1745: two tenants with risks across
// several of the seven seeded categories, an aggregation rule targeting a
// theme, risks tagged with that theme, and a meta-risk the rule produced.

package risks_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

const taxTheme = "ownership"

// taxEnv is one tenant's server with an admin bearer and two non-admin
// bearers (a control owner and a viewer).
type taxEnv struct {
	url    string
	tenant string
	admin  string
	owner  string
	viewer string
}

func taxServer(t *testing.T, app *pgxpool.Pool, tenant string) taxEnv {
	t.Helper()
	srv := api.New(api.Config{})
	srv.AttachDB(app)
	tid := uuid.MustParse(tenant)
	env := taxEnv{
		tenant: tenant,
		admin:  srv.IssueTestJWT(t, testjwt.AdminFor(tid)),
		owner:  srv.IssueTestJWT(t, testjwt.OwnerFor(tid, []string{"control_owner"})),
		viewer: srv.IssueTestJWT(t, testjwt.ViewerFor(tid)),
	}
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)
	env.url = ts.URL
	return env
}

func taxCall(t *testing.T, env taxEnv, method, path, bearer string, payload any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if payload != nil {
		_ = json.NewEncoder(&buf).Encode(payload)
	}
	req, _ := http.NewRequest(method, env.url+path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

// taxList returns the tenant's listed categories keyed by slug.
func taxList(t *testing.T, env taxEnv, query string) map[string]map[string]any {
	t.Helper()
	status, body := taxCall(t, env, http.MethodGet, "/v1/risks/categories"+query, env.admin, nil)
	if status != http.StatusOK {
		t.Fatalf("list categories: status %d (%v)", status, body)
	}
	out := map[string]map[string]any{}
	for _, item := range body["categories"].([]any) {
		c := item.(map[string]any)
		out[c["slug"].(string)] = c
	}
	return out
}

func taxCategory(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	c, ok := body["category"].(map[string]any)
	if !ok {
		t.Fatalf("response has no category: %v", body)
	}
	return c
}

func taxSeedRisk(t *testing.T, admin *pgxpool.Pool, tenant, category string, themes []string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO risks (
			id, tenant_id, title, description, category, methodology,
			inherent_score, treatment, treatment_owner, residual_score, themes
		) VALUES ($1, $2, 'taxonomy fixture', '', $3::risk_category, 'qualitative_5x5',
		          '{"likelihood":3,"impact":3}'::jsonb, 'untreated', '', '{}'::jsonb, $4)
	`, id, tenant, category, themes); err != nil {
		t.Fatalf("seed %s risk: %v", category, err)
	}
	return id
}

// taxFixture seeds one tenant: privacy, operational and regulatory risks,
// two of them tagged with taxTheme, an aggregation rule targeting that
// theme, and an `availability` meta-risk the rule produced from them.
func taxFixture(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := dbtest.SeedTenant(t, admin,
		"risk_aggregations", "risks", "aggregation_rules", "org_risk_categories")
	ctx := context.Background()
	if _, err := admin.Exec(ctx, `SELECT seed_risk_categories($1)`, tenant); err != nil {
		t.Fatalf("seed categories: %v", err)
	}
	a := taxSeedRisk(t, admin, tenant, "privacy", []string{taxTheme})
	b := taxSeedRisk(t, admin, tenant, "operational", []string{taxTheme})
	taxSeedRisk(t, admin, tenant, "regulatory", []string{})
	ruleID := uuid.New()
	if _, err := admin.Exec(ctx, `
		INSERT INTO aggregation_rules (
		    id, tenant_id, rule_id, target_theme, min_risks, min_teams,
		    window_days, parent_level, severity_function, rule_body
		) VALUES ($1, $2, $3, $4, 2, 1, 30, 'org', 'max', '{}'::jsonb)
	`, ruleID, tenant, "rule-"+ruleID.String()[:8], taxTheme); err != nil {
		t.Fatalf("seed aggregation rule: %v", err)
	}
	meta := taxSeedRisk(t, admin, tenant, "availability", []string{taxTheme})
	for _, child := range []uuid.UUID{a, b} {
		if _, err := admin.Exec(ctx, `
			INSERT INTO risk_aggregations (parent_risk_id, child_risk_id, rule_id, tenant_id)
			VALUES ($1, $2, $3, $4)`, meta, child, ruleID, tenant); err != nil {
			t.Fatalf("link meta-risk: %v", err)
		}
	}
	return tenant
}

func TestCategoryAdmin_ListShowsUsageCounts(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	env := taxServer(t, dbtest.NewAppPool(t), taxFixture(t, admin))

	cats := taxList(t, env, "")
	for slug, want := range map[string][2]float64{
		"privacy": {1, 0}, "operational": {1, 0}, "regulatory": {1, 0},
		"availability": {1, 1}, "financial": {0, 0},
	} {
		c := cats[slug]
		if c["risk_count"] != want[0] || c["rule_count"] != want[1] {
			t.Errorf("%s: risk_count=%v rule_count=%v, want %v/%v",
				slug, c["risk_count"], c["rule_count"], want[0], want[1])
		}
	}
}

func TestCategoryAdmin_AddThenRenameKeepsSlug(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	env := taxServer(t, dbtest.NewAppPool(t), taxFixture(t, admin))

	status, body := taxCall(t, env, http.MethodPost, "/v1/risks/categories", env.admin,
		map[string]any{"slug": "cloud-security", "name": "Cloud Security"})
	if status != http.StatusCreated {
		t.Fatalf("add: status %d (%v)", status, body)
	}
	created := taxCategory(t, body)
	if created["slug"] != "cloud-security" || created["is_default"] != false || created["archived"] != false {
		t.Fatalf("created = %v", created)
	}

	// Until #1745's contract step a new category cannot hold risks: a 400,
	// not a 500.
	if status, body := taxCall(t, env, http.MethodPost, "/v1/risks", env.admin, map[string]any{
		"title": "in a new category", "category": "cloud-security",
		"inherent_score": map[string]int{"likelihood": 2, "impact": 2},
	}); status != http.StatusBadRequest {
		t.Fatalf("risk in new category: status %d (%v), want 400", status, body)
	}

	// A duplicate slug is a conflict.
	if status, body := taxCall(t, env, http.MethodPost, "/v1/risks/categories", env.admin,
		map[string]any{"slug": "cloud-security", "name": "Other"}); status != http.StatusConflict {
		t.Fatalf("duplicate slug: status %d (%v), want 409", status, body)
	}
	// The body cannot carry a slug on rename.
	if status, body := taxCall(t, env, http.MethodPatch, "/v1/risks/categories/cloud-security", env.admin,
		map[string]any{"slug": "cloud"}); status != http.StatusBadRequest {
		t.Fatalf("rename with slug: status %d (%v), want 400", status, body)
	}

	// Rename by id; seeded categories rename by slug too.
	status, body = taxCall(t, env, http.MethodPatch, "/v1/risks/categories/"+created["id"].(string), env.admin,
		map[string]any{"name": "Cloud & Platform Security", "description": "IaaS and PaaS"})
	if status != http.StatusOK {
		t.Fatalf("rename: status %d (%v)", status, body)
	}
	renamed := taxCategory(t, body)
	if renamed["slug"] != "cloud-security" || renamed["name"] != "Cloud & Platform Security" ||
		renamed["description"] != "IaaS and PaaS" || renamed["id"] != created["id"] {
		t.Fatalf("renamed = %v", renamed)
	}
	if status, body := taxCall(t, env, http.MethodPatch, "/v1/risks/categories/operational", env.admin,
		map[string]any{"name": "Operational Resilience"}); status != http.StatusOK ||
		taxCategory(t, body)["slug"] != "operational" {
		t.Fatalf("rename operational: status %d (%v)", status, body)
	}
	cats := taxList(t, env, "")
	if cats["cloud-security"]["name"] != "Cloud & Platform Security" || cats["operational"]["name"] != "Operational Resilience" {
		t.Fatalf("list after rename: %v / %v", cats["cloud-security"], cats["operational"])
	}
	if cats["operational"]["risk_count"] != float64(1) {
		t.Errorf("operational risk_count after rename = %v, want 1", cats["operational"]["risk_count"])
	}
}

func TestCategoryAdmin_ArchiveAndUnarchive(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	env := taxServer(t, dbtest.NewAppPool(t), taxFixture(t, admin))

	if status, body := taxCall(t, env, http.MethodPost, "/v1/risks/categories/privacy/archive", env.admin, nil); status != http.StatusOK ||
		taxCategory(t, body)["archived"] != true {
		t.Fatalf("archive: status %d (%v)", status, body)
	}
	if _, listed := taxList(t, env, "")["privacy"]; listed {
		t.Fatal("archived category is in the default list")
	}
	archived, listed := taxList(t, env, "?include_archived=true")["privacy"]
	if !listed || archived["archived"] != true || archived["risk_count"] != float64(1) {
		t.Fatalf("include_archived list: privacy = %v (listed %v)", archived, listed)
	}

	// Kept on the existing risk, refused for a new one.
	var stillPrivacy int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM risks WHERE tenant_id = $1 AND category = 'privacy'`, env.tenant).Scan(&stillPrivacy); err != nil || stillPrivacy != 1 {
		t.Fatalf("existing privacy risks = %d (%v), want 1", stillPrivacy, err)
	}
	if status, body := taxCall(t, env, http.MethodPost, "/v1/risks", env.admin, map[string]any{
		"title": "new", "category": "privacy",
		"inherent_score": map[string]int{"likelihood": 2, "impact": 2},
	}); status != http.StatusBadRequest {
		t.Fatalf("create on archived category: status %d (%v), want 400", status, body)
	}

	// The default cannot be archived.
	if status, body := taxCall(t, env, http.MethodPost, "/v1/risks/categories/operational/archive", env.admin, nil); status != http.StatusConflict {
		t.Fatalf("archive default: status %d (%v), want 409", status, body)
	}

	if status, body := taxCall(t, env, http.MethodPost, "/v1/risks/categories/privacy/unarchive", env.admin, nil); status != http.StatusOK ||
		taxCategory(t, body)["archived"] != false {
		t.Fatalf("unarchive: status %d (%v)", status, body)
	}
	if c, listed := taxList(t, env, "")["privacy"]; !listed || c["archived"] != false {
		t.Fatalf("after unarchive: privacy = %v (listed %v)", c, listed)
	}
}

func TestCategoryAdmin_SetDefaultMovesTheDefault(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	env := taxServer(t, dbtest.NewAppPool(t), taxFixture(t, admin))

	if status, body := taxCall(t, env, http.MethodPost, "/v1/risks/categories/regulatory/set-default", env.admin, nil); status != http.StatusOK ||
		taxCategory(t, body)["is_default"] != true {
		t.Fatalf("set-default: status %d (%v)", status, body)
	}
	var defaults []string
	for slug, c := range taxList(t, env, "?include_archived=true") {
		if c["is_default"] == true {
			defaults = append(defaults, slug)
		}
	}
	if len(defaults) != 1 || defaults[0] != "regulatory" {
		t.Fatalf("defaults = %v, want [regulatory]", defaults)
	}

	// Setting it again is a no-op; an archived category cannot be the default.
	if status, _ := taxCall(t, env, http.MethodPost, "/v1/risks/categories/regulatory/set-default", env.admin, nil); status != http.StatusOK {
		t.Fatalf("repeat set-default: status %d", status)
	}
	if status, _ := taxCall(t, env, http.MethodPost, "/v1/risks/categories/financial/archive", env.admin, nil); status != http.StatusOK {
		t.Fatalf("archive financial: status %d", status)
	}
	if status, body := taxCall(t, env, http.MethodPost, "/v1/risks/categories/financial/set-default", env.admin, nil); status != http.StatusConflict {
		t.Fatalf("set-default archived: status %d (%v), want 409", status, body)
	}
}

func TestCategoryAdmin_DeleteRefusedWhileUsed(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	env := taxServer(t, dbtest.NewAppPool(t), taxFixture(t, admin))

	status, body := taxCall(t, env, http.MethodDelete, "/v1/risks/categories/availability", env.admin, nil)
	if status != http.StatusConflict || body["risk_count"] != float64(1) || body["rule_count"] != float64(1) {
		t.Fatalf("delete used (meta-risk): status %d body %v, want 409 with risk_count 1 rule_count 1", status, body)
	}
	status, body = taxCall(t, env, http.MethodDelete, "/v1/risks/categories/privacy", env.admin, nil)
	if status != http.StatusConflict || body["risk_count"] != float64(1) || body["rule_count"] != float64(0) {
		t.Fatalf("delete used: status %d body %v, want 409 with risk_count 1 rule_count 0", status, body)
	}
	if status, body := taxCall(t, env, http.MethodDelete, "/v1/risks/categories/operational", env.admin, nil); status != http.StatusConflict {
		t.Fatalf("delete default: status %d (%v), want 409", status, body)
	}

	if status, body := taxCall(t, env, http.MethodDelete, "/v1/risks/categories/financial", env.admin, nil); status != http.StatusNoContent {
		t.Fatalf("delete unused: status %d (%v), want 204", status, body)
	}
	if _, listed := taxList(t, env, "?include_archived=true")["financial"]; listed {
		t.Fatal("deleted category still listed")
	}
	if status, _ := taxCall(t, env, http.MethodDelete, "/v1/risks/categories/financial", env.admin, nil); status != http.StatusNotFound {
		t.Fatalf("delete again: status %d, want 404", status)
	}
}

func TestCategoryAdmin_NonAdminRefusedAuditLoggedTenantBUnaffected(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	envA := taxServer(t, app, taxFixture(t, admin))
	envB := taxServer(t, app, taxFixture(t, admin))
	before := taxList(t, envB, "?include_archived=true")

	writes := []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/v1/risks/categories", map[string]any{"slug": "iam", "name": "IAM"}},
		{http.MethodPatch, "/v1/risks/categories/privacy", map[string]any{"name": "Data Protection"}},
		{http.MethodPost, "/v1/risks/categories/financial/archive", nil},
		{http.MethodPost, "/v1/risks/categories/financial/unarchive", nil},
		{http.MethodPost, "/v1/risks/categories/regulatory/set-default", nil},
		{http.MethodDelete, "/v1/risks/categories/iam", nil},
	}
	for _, w := range writes {
		for name, bearer := range map[string]string{"control owner": envA.owner, "viewer": envA.viewer} {
			if status, body := taxCall(t, envA, w.method, w.path, bearer, w.body); status != http.StatusForbidden {
				t.Fatalf("%s %s as %s: status %d (%v), want 403", w.method, w.path, name, status, body)
			}
		}
	}
	// A refused write changes nothing and logs nothing.
	var refused int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM taxonomy_audit_log WHERE tenant_id = $1`, envA.tenant).Scan(&refused); err != nil || refused != 0 {
		t.Fatalf("audit rows after refused writes = %d (%v), want 0", refused, err)
	}
	// Reads stay open to program roles.
	if status, _ := taxCall(t, envA, http.MethodGet, "/v1/risks/categories", envA.owner, nil); status != http.StatusOK {
		t.Fatalf("list as control owner: status %d, want 200", status)
	}

	for _, w := range writes {
		if status, body := taxCall(t, envA, w.method, w.path, envA.admin, w.body); status >= 300 {
			t.Fatalf("%s %s as admin: status %d (%v)", w.method, w.path, status, body)
		}
	}

	rows, err := admin.Query(context.Background(), `
		SELECT action, subject_slug, actor, before IS NULL, after IS NULL
		FROM taxonomy_audit_log WHERE tenant_id = $1 ORDER BY occurred_at, id`, envA.tenant)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	var got []string
	for rows.Next() {
		var action, slug, actor string
		var noBefore, noAfter bool
		if err := rows.Scan(&action, &slug, &actor, &noBefore, &noAfter); err != nil {
			t.Fatalf("scan audit row: %v", err)
		}
		if actor == "" {
			t.Errorf("%s %s has no actor", action, slug)
		}
		if noBefore != (action == "create") || noAfter != (action == "delete") {
			t.Errorf("%s %s: before null=%v after null=%v", action, slug, noBefore, noAfter)
		}
		got = append(got, action+" "+slug)
	}
	rows.Close()
	want := []string{"create iam", "rename privacy", "archive financial", "unarchive financial", "set_default regulatory", "delete iam"}
	if len(got) != len(want) {
		t.Fatalf("audit log = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("audit log = %v, want %v", got, want)
		}
	}

	// The changes reach the unified admin audit log as kind taxonomy.
	status, body := taxCall(t, envA, http.MethodGet, "/v1/admin/audit-log/unified?kind=taxonomy&"+url.Values{
		"from": {time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)},
		"to":   {time.Now().Add(time.Hour).UTC().Format(time.RFC3339)},
	}.Encode(), envA.admin, nil)
	if status != http.StatusOK {
		t.Fatalf("unified audit log: status %d (%v)", status, body)
	}
	entries, _ := body["entries"].([]any)
	if len(entries) != len(want) {
		t.Fatalf("unified taxonomy entries = %d, want %d (%v)", len(entries), len(want), body)
	}

	// Tenant B: same categories as before, no audit rows.
	after := taxList(t, envB, "?include_archived=true")
	if len(after) != len(before) {
		t.Fatalf("tenant B categories %d -> %d", len(before), len(after))
	}
	for slug, c := range before {
		a := after[slug]
		if a["name"] != c["name"] || a["archived"] != c["archived"] || a["is_default"] != c["is_default"] {
			t.Errorf("tenant B %s changed: %v -> %v", slug, c, a)
		}
	}
	if _, has := after["iam"]; has {
		t.Error("tenant B sees tenant A's category")
	}
	var bRows int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM taxonomy_audit_log WHERE tenant_id = $1`, envB.tenant).Scan(&bRows); err != nil || bRows != 0 {
		t.Fatalf("tenant B audit rows = %d (%v), want 0", bRows, err)
	}
}
