//go:build integration

// #1811 — organisation theme management (API). Runs against a real
// Postgres with two tenants, each holding risks across several of the seven
// category enum values, an organisation theme, an active aggregation rule
// targeting it, and risks tagged with it.

package risks_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
)

const orgTheme = "cloud-drift"

type themeFixture struct {
	tenant string
	rule   uuid.UUID
	tagged []uuid.UUID // risks carrying orgTheme
	plain  uuid.UUID   // a risk with only a built-in theme
}

// seedThemeFixture seeds one tenant: privacy and operational risks tagged
// with orgTheme (one also with the built-in ownership), a regulatory risk
// tagged ownership only, and an active rule targeting orgTheme whose
// activation predates the risks, so an evaluation counts them.
func seedThemeFixture(t *testing.T, admin *pgxpool.Pool) themeFixture {
	t.Helper()
	tenant := dbtest.SeedTenant(t, admin,
		"aggregation_rule_evaluations", "risk_aggregations", "risks",
		"aggregation_rules", "org_themes", "org_risk_categories")
	ctx := context.Background()
	if _, err := admin.Exec(ctx, `SELECT seed_risk_categories($1)`, tenant); err != nil {
		t.Fatalf("seed categories: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO org_themes (id, tenant_id, theme_name, description)
		VALUES (gen_random_uuid(), $1, $2, 'Configuration drift in cloud accounts.')`,
		tenant, orgTheme); err != nil {
		t.Fatalf("seed org theme: %v", err)
	}
	f := themeFixture{tenant: tenant, rule: uuid.New()}
	f.tagged = []uuid.UUID{
		taxSeedRisk(t, admin, tenant, "privacy", []string{orgTheme, "ownership"}),
		taxSeedRisk(t, admin, tenant, "operational", []string{orgTheme}),
	}
	f.plain = taxSeedRisk(t, admin, tenant, "regulatory", []string{"ownership"})
	ruleID := "rule-" + f.rule.String()[:8]
	body := fmt.Sprintf(`{"rule_id":%q,"target_theme":%q,"min_risks":2,"min_teams":1,
		"window_days":30,"parent_level":"org","severity_function":"max"}`, ruleID, orgTheme)
	if _, err := admin.Exec(ctx, `
		INSERT INTO aggregation_rules (
		    id, tenant_id, rule_id, target_theme, min_risks, min_teams,
		    window_days, parent_level, severity_function, rule_body,
		    status, activated_by, activated_at
		) VALUES ($1, $2, $3, $4, 2, 1, 30, 'org', 'max', $5::jsonb,
		          'active', 'fixture', now() - interval '1 day')
	`, f.rule, tenant, ruleID, orgTheme, body); err != nil {
		t.Fatalf("seed aggregation rule: %v", err)
	}
	return f
}

func themeNames(t *testing.T, env taxEnv) map[string]string {
	t.Helper()
	status, body := taxCall(t, env, http.MethodGet, "/v1/themes", env.admin, nil)
	if status != http.StatusOK {
		t.Fatalf("list themes: status %d (%v)", status, body)
	}
	out := map[string]string{}
	for _, item := range body["themes"].([]any) {
		th := item.(map[string]any)
		out[th["name"].(string)] = th["source"].(string)
	}
	return out
}

func riskThemes(t *testing.T, admin *pgxpool.Pool, id uuid.UUID) []string {
	t.Helper()
	var themes []string
	if err := admin.QueryRow(context.Background(),
		`SELECT themes FROM risks WHERE id = $1`, id).Scan(&themes); err != nil {
		t.Fatalf("read risk themes: %v", err)
	}
	return themes
}

func TestThemeAdmin_CreateIsListedAndAssignable(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	f := seedThemeFixture(t, admin)
	env := taxServer(t, dbtest.NewAppPool(t), f.tenant)

	status, body := taxCall(t, env, http.MethodPost, "/v1/themes", env.admin,
		map[string]any{"name": "  Shadow-IT ", "description": "Unsanctioned SaaS."})
	if status != http.StatusCreated {
		t.Fatalf("create: status %d (%v)", status, body)
	}
	created := body["theme"].(map[string]any)
	if created["name"] != "shadow-it" || created["source"] != "tenant" {
		t.Fatalf("created = %v, want name shadow-it (normalised) source tenant", created)
	}
	if got := themeNames(t, env); got["shadow-it"] != "tenant" || got["ownership"] != "default" {
		t.Fatalf("listed themes = %v", got)
	}
	if status, body := taxCall(t, env, http.MethodPost, "/v1/risks/"+f.plain.String()+"/themes", env.admin,
		map[string]any{"themes": []string{"shadow-it"}}); status != http.StatusOK {
		t.Fatalf("assign new theme: status %d (%v)", status, body)
	}
	if got := riskThemes(t, admin, f.plain); !slices.Equal(got, []string{"ownership", "shadow-it"}) {
		t.Fatalf("risk themes = %v", got)
	}

	// Writes need admin or grc_engineer.
	for _, bearer := range []string{env.owner, env.viewer} {
		if status, _ := taxCall(t, env, http.MethodPost, "/v1/themes", bearer,
			map[string]any{"name": "other"}); status != http.StatusForbidden {
			t.Errorf("non-admin create: status %d, want 403", status)
		}
	}
	// A bad name is a 400.
	if status, _ := taxCall(t, env, http.MethodPost, "/v1/themes", env.admin,
		map[string]any{"name": "has space"}); status != http.StatusBadRequest {
		t.Errorf("bad name: status %d, want 400", status)
	}
}

func TestThemeAdmin_RenameRewritesRisksAndRules(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	a := seedThemeFixture(t, admin)
	b := seedThemeFixture(t, admin)
	app := dbtest.NewAppPool(t)
	envA := taxServer(t, app, a.tenant)
	envB := taxServer(t, app, b.tenant)

	status, body := taxCall(t, envA, http.MethodPatch, "/v1/themes/"+orgTheme, envA.admin,
		map[string]any{"name": "cloud-config"})
	if status != http.StatusOK {
		t.Fatalf("rename: status %d (%v)", status, body)
	}
	if body["risks_updated"] != float64(2) || body["rules_updated"] != float64(1) {
		t.Fatalf("rename counts = %v/%v, want 2/1", body["risks_updated"], body["rules_updated"])
	}
	if got := riskThemes(t, admin, a.tagged[0]); !slices.Equal(got, []string{"cloud-config", "ownership"}) {
		t.Errorf("tagged risk themes = %v", got)
	}
	var stale int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM risks WHERE tenant_id = $1 AND $2 = ANY(themes)`, a.tenant, orgTheme).Scan(&stale); err != nil || stale != 0 {
		t.Fatalf("risks still carrying %s = %d (%v), want 0", orgTheme, stale, err)
	}
	var column, bodyTheme string
	if err := admin.QueryRow(context.Background(),
		`SELECT target_theme, rule_body->>'target_theme' FROM aggregation_rules WHERE id = $1`, a.rule).Scan(&column, &bodyTheme); err != nil {
		t.Fatalf("read rule: %v", err)
	}
	if column != "cloud-config" || bodyTheme != "cloud-config" {
		t.Fatalf("rule target_theme = %q, rule_body target_theme = %q, want cloud-config", column, bodyTheme)
	}
	if got := themeNames(t, envA); got["cloud-config"] != "tenant" || got[orgTheme] != "" {
		t.Fatalf("themes after rename = %v", got)
	}

	// The rule still evaluates, and counts the renamed risks.
	status, body = taxCall(t, envA, http.MethodPost, "/v1/aggregation-rules/evaluate", envA.admin, nil)
	if status != http.StatusOK {
		t.Fatalf("evaluate: status %d (%v)", status, body)
	}
	found := false
	for _, item := range body["evaluations"].([]any) {
		ev := item.(map[string]any)
		if ev["rule_id"] == a.rule.String() {
			found = true
			if ev["risk_count"] != float64(2) {
				t.Errorf("evaluation risk_count = %v, want 2", ev["risk_count"])
			}
		}
	}
	if !found {
		t.Fatalf("no evaluation for the renamed rule: %v", body)
	}
	// The old name is no longer assignable.
	if status, _ := taxCall(t, envA, http.MethodPost, "/v1/risks/"+a.plain.String()+"/themes", envA.admin,
		map[string]any{"themes": []string{orgTheme}}); status != http.StatusBadRequest {
		t.Errorf("assign old name: status %d, want 400", status)
	}

	// Tenant B is untouched: its theme, risks and rule keep the old name.
	if got := riskThemes(t, admin, b.tagged[1]); !slices.Equal(got, []string{orgTheme}) {
		t.Errorf("tenant B risk themes = %v", got)
	}
	if err := admin.QueryRow(context.Background(),
		`SELECT target_theme, rule_body->>'target_theme' FROM aggregation_rules WHERE id = $1`, b.rule).Scan(&column, &bodyTheme); err != nil {
		t.Fatalf("read tenant B rule: %v", err)
	}
	if column != orgTheme || bodyTheme != orgTheme {
		t.Errorf("tenant B rule = %q / %q, want %s", column, bodyTheme, orgTheme)
	}
	if got := themeNames(t, envB); got[orgTheme] != "tenant" || got["cloud-config"] != "" {
		t.Errorf("tenant B themes = %v", got)
	}
	// Tenant B cannot reach A's renamed theme.
	if status, _ := taxCall(t, envB, http.MethodPatch, "/v1/themes/cloud-config", envB.admin,
		map[string]any{"name": "x"}); status != http.StatusNotFound {
		t.Errorf("tenant B renaming A's theme: status %d, want 404", status)
	}

	// Audit: one rename row for A naming before and after; none for B.
	var action, beforeName, afterName, actor string
	if err := admin.QueryRow(context.Background(), `
		SELECT action, before->>'name', after->>'name', actor
		FROM taxonomy_audit_log WHERE tenant_id = $1 AND subject_kind = 'theme'`, a.tenant).
		Scan(&action, &beforeName, &afterName, &actor); err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if action != "rename" || beforeName != orgTheme || afterName != "cloud-config" || actor == "" {
		t.Errorf("audit row = %s %s->%s by %q", action, beforeName, afterName, actor)
	}
	var bRows int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM taxonomy_audit_log WHERE tenant_id = $1`, b.tenant).Scan(&bRows); err != nil || bRows != 0 {
		t.Errorf("tenant B audit rows = %d (%v), want 0", bRows, err)
	}
	// The change reaches the unified admin audit log as kind taxonomy.
	status, body = taxCall(t, envA, http.MethodGet, "/v1/admin/audit-log/unified?kind=taxonomy&"+url.Values{
		"from": {time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)},
		"to":   {time.Now().Add(time.Hour).UTC().Format(time.RFC3339)},
	}.Encode(), envA.admin, nil)
	if status != http.StatusOK {
		t.Fatalf("unified audit log: status %d (%v)", status, body)
	}
	entries, _ := body["entries"].([]any)
	if !slices.ContainsFunc(entries, func(e any) bool {
		m := e.(map[string]any)
		return m["target_type"] == "theme" && m["action"] == "rename"
	}) {
		t.Errorf("unified audit log has no theme rename: %v", body)
	}
}

func TestThemeAdmin_DeleteRefusedWhileInUse(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	f := seedThemeFixture(t, admin)
	env := taxServer(t, dbtest.NewAppPool(t), f.tenant)

	status, body := taxCall(t, env, http.MethodDelete, "/v1/themes/"+orgTheme, env.admin, nil)
	if status != http.StatusConflict {
		t.Fatalf("delete used theme: status %d (%v), want 409", status, body)
	}
	if body["risk_count"] != float64(2) || body["rule_count"] != float64(1) {
		t.Fatalf("409 counts = %v/%v, want 2/1", body["risk_count"], body["rule_count"])
	}
	if got := themeNames(t, env); got[orgTheme] != "tenant" {
		t.Fatalf("used theme gone after refused delete: %v", got)
	}

	status, body = taxCall(t, env, http.MethodPost, "/v1/themes", env.admin, map[string]any{"name": "unused"})
	if status != http.StatusCreated {
		t.Fatalf("create: status %d (%v)", status, body)
	}
	id := body["theme"].(map[string]any)["id"].(string)
	if status, body := taxCall(t, env, http.MethodDelete, "/v1/themes/"+id, env.admin, nil); status != http.StatusNoContent {
		t.Fatalf("delete unused theme by id: status %d (%v)", status, body)
	}
	if got := themeNames(t, env); got["unused"] != "" {
		t.Fatalf("deleted theme still listed: %v", got)
	}
	var actions []string
	rows, err := admin.Query(context.Background(), `
		SELECT action FROM taxonomy_audit_log
		WHERE tenant_id = $1 AND subject_kind = 'theme' ORDER BY occurred_at, id`, f.tenant)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatalf("scan: %v", err)
		}
		actions = append(actions, a)
	}
	rows.Close()
	if !slices.Equal(actions, []string{"create", "delete"}) {
		t.Errorf("audit actions = %v, want [create delete] (the refused delete writes nothing)", actions)
	}
}

func TestThemeAdmin_BuiltInsAreFixed(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	f := seedThemeFixture(t, admin)
	env := taxServer(t, dbtest.NewAppPool(t), f.tenant)

	if status, body := taxCall(t, env, http.MethodPatch, "/v1/themes/ownership", env.admin,
		map[string]any{"name": "owners"}); status != http.StatusConflict {
		t.Errorf("rename built-in: status %d (%v), want 409", status, body)
	}
	if status, body := taxCall(t, env, http.MethodDelete, "/v1/themes/monitoring", env.admin, nil); status != http.StatusConflict {
		t.Errorf("delete built-in: status %d (%v), want 409", status, body)
	}
	// A name that clashes with a built-in is refused, on create and rename.
	if status, body := taxCall(t, env, http.MethodPost, "/v1/themes", env.admin,
		map[string]any{"name": "Ownership"}); status != http.StatusConflict {
		t.Errorf("create clashing with built-in: status %d (%v), want 409", status, body)
	}
	if status, body := taxCall(t, env, http.MethodPatch, "/v1/themes/"+orgTheme, env.admin,
		map[string]any{"name": "monitoring"}); status != http.StatusConflict {
		t.Errorf("rename onto built-in: status %d (%v), want 409", status, body)
	}
	// And one that clashes with the organisation's own.
	if status, body := taxCall(t, env, http.MethodPost, "/v1/themes", env.admin,
		map[string]any{"name": orgTheme}); status != http.StatusConflict {
		t.Errorf("create duplicate: status %d (%v), want 409", status, body)
	}
	if status, body := taxCall(t, env, http.MethodPatch, "/v1/themes/no-such-theme", env.admin,
		map[string]any{"name": "x"}); status != http.StatusNotFound {
		t.Errorf("rename unknown: status %d (%v), want 404", status, body)
	}

	var n int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM taxonomy_audit_log WHERE tenant_id = $1`, f.tenant).Scan(&n); err != nil || n != 0 {
		t.Errorf("audit rows after refused writes = %d (%v), want 0", n, err)
	}
	if got := riskThemes(t, admin, f.plain); !slices.Equal(got, []string{"ownership"}) {
		t.Errorf("built-in theme on risk changed: %v", got)
	}
}
