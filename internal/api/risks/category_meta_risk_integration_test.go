//go:build integration

// #1813 — aggregation meta-risks fall back to the organisation's default
// category (migrate step of #1745). Runs against a real Postgres with the
// shared fixture of #1745 (taxFixture).
//
//   - a meta-risk takes its first scored child's category while that
//     category is active; with every child's category archived it takes
//     the tenant's default, and after set-default new meta-risks follow;
//   - each tenant's rules use that tenant's default;
//   - set-default refuses a custom category until the contract step.

package risks_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// metaRule seeds an active aggregation rule on theme that fires on two
// risks. Risks seeded after it fall inside its window.
func metaRule(t *testing.T, admin *pgxpool.Pool, tenant, theme string) string {
	t.Helper()
	id := uuid.New()
	ruleID := "rule-" + id.String()[:8]
	body := fmt.Sprintf(`{"rule_id":%q,"target_theme":%q,"min_risks":2,"min_teams":1,
		"window_days":30,"parent_level":"org","severity_function":"max"}`, ruleID, theme)
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO aggregation_rules (
		    id, tenant_id, rule_id, target_theme, min_risks, min_teams,
		    window_days, parent_level, severity_function, rule_body,
		    status, activated_by, activated_at
		) VALUES ($1, $2, $3, $4, 2, 1, 30, 'org', 'max', $5::jsonb,
		          'active', 'fixture', now() - interval '1 minute')
	`, id, tenant, ruleID, theme, body); err != nil {
		t.Fatalf("seed aggregation rule: %v", err)
	}
	return ruleID
}

// evaluateAll runs every active rule for the tenant through the API.
func evaluateAll(t *testing.T, env taxEnv) {
	t.Helper()
	if status, body := taxCall(t, env, http.MethodPost, "/v1/aggregation-rules/evaluate", env.admin, nil); status != http.StatusOK {
		t.Fatalf("evaluate: status %d (%v)", status, body)
	}
}

// metaRiskCategory reads the category of the meta-risk ruleID produced:
// the enum and the slug its reference points at, which must agree.
func metaRiskCategory(t *testing.T, admin *pgxpool.Pool, tenant, ruleID string) string {
	t.Helper()
	var enum, slug string
	if err := admin.QueryRow(context.Background(), `
		SELECT r.category::text, c.slug
		FROM risks r JOIN org_risk_categories c ON c.tenant_id = r.tenant_id AND c.id = r.category_id
		WHERE r.tenant_id = $1 AND r.inherent_score->>'rule_id' = $2`, tenant, ruleID).Scan(&enum, &slug); err != nil {
		t.Fatalf("read meta-risk of %s: %v", ruleID, err)
	}
	if enum != slug {
		t.Fatalf("meta-risk of %s: enum %q, reference slug %q", ruleID, enum, slug)
	}
	return slug
}

func setDefault(t *testing.T, env taxEnv, slug string) {
	t.Helper()
	if status, body := taxCall(t, env, http.MethodPost, "/v1/risks/categories/"+slug+"/set-default", env.admin, nil); status != http.StatusOK {
		t.Fatalf("set-default %s: status %d (%v)", slug, status, body)
	}
}

func archive(t *testing.T, env taxEnv, slug string) {
	t.Helper()
	if status, body := taxCall(t, env, http.MethodPost, "/v1/risks/categories/"+slug+"/archive", env.admin, nil); status != http.StatusOK {
		t.Fatalf("archive %s: status %d (%v)", slug, status, body)
	}
}

func TestMetaRiskCategory_FallsBackToOrgDefault(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	env := taxServer(t, dbtest.NewAppPool(t), taxFixture(t, admin))

	// While the children's category is active the meta-risk takes it.
	active := metaRule(t, admin, env.tenant, "meta-active")
	taxSeedRisk(t, admin, env.tenant, "privacy", []string{"meta-active"})
	taxSeedRisk(t, admin, env.tenant, "privacy", []string{"meta-active"})
	evaluateAll(t, env)
	if got := metaRiskCategory(t, admin, env.tenant, active); got != "privacy" {
		t.Fatalf("meta-risk over active privacy children: category %q, want privacy", got)
	}

	// No child has an active category: the organisation's default.
	first := metaRule(t, admin, env.tenant, "meta-first")
	taxSeedRisk(t, admin, env.tenant, "privacy", []string{"meta-first"})
	taxSeedRisk(t, admin, env.tenant, "privacy", []string{"meta-first"})
	archive(t, env, "privacy")
	evaluateAll(t, env)
	if got := metaRiskCategory(t, admin, env.tenant, first); got != "operational" {
		t.Fatalf("meta-risk over archived children: category %q, want the default operational", got)
	}

	// After set-default, new meta-risks follow; the existing one keeps its
	// category.
	setDefault(t, env, "financial")
	second := metaRule(t, admin, env.tenant, "meta-second")
	taxSeedRisk(t, admin, env.tenant, "privacy", []string{"meta-second"})
	taxSeedRisk(t, admin, env.tenant, "privacy", []string{"meta-second"})
	evaluateAll(t, env)
	if got := metaRiskCategory(t, admin, env.tenant, second); got != "financial" {
		t.Fatalf("meta-risk after set-default: category %q, want financial", got)
	}
	if got := metaRiskCategory(t, admin, env.tenant, first); got != "operational" {
		t.Fatalf("earlier meta-risk after set-default: category %q, want operational", got)
	}
}

func TestMetaRiskCategory_EachTenantUsesItsOwnDefault(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	envA := taxServer(t, app, taxFixture(t, admin))
	envB := taxServer(t, app, taxFixture(t, admin))

	setDefault(t, envA, "financial")
	setDefault(t, envB, "regulatory")
	rules := map[string]string{}
	for name, env := range map[string]taxEnv{"A": envA, "B": envB} {
		rules[name] = metaRule(t, admin, env.tenant, "meta-tenant")
		taxSeedRisk(t, admin, env.tenant, "privacy", []string{"meta-tenant"})
		taxSeedRisk(t, admin, env.tenant, "privacy", []string{"meta-tenant"})
		archive(t, env, "privacy")
	}
	evaluateAll(t, envB)
	evaluateAll(t, envA)

	if got := metaRiskCategory(t, admin, envA.tenant, rules["A"]); got != "financial" {
		t.Errorf("tenant A meta-risk: category %q, want A's default financial", got)
	}
	if got := metaRiskCategory(t, admin, envB.tenant, rules["B"]); got != "regulatory" {
		t.Errorf("tenant B meta-risk: category %q, want B's default regulatory", got)
	}
}

func TestMetaRiskCategory_SetDefaultRefusesCustomCategory(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	env := taxServer(t, dbtest.NewAppPool(t), taxFixture(t, admin))

	if status, body := taxCall(t, env, http.MethodPost, "/v1/risks/categories", env.admin,
		map[string]any{"slug": "cloud-security", "name": "Cloud Security"}); status != http.StatusCreated {
		t.Fatalf("add: status %d (%v)", status, body)
	}
	if status, body := taxCall(t, env, http.MethodPost, "/v1/risks/categories/cloud-security/set-default", env.admin, nil); status != http.StatusBadRequest {
		t.Fatalf("set-default custom: status %d (%v), want 400", status, body)
	}
	cats := taxList(t, env, "")
	if cats["operational"]["is_default"] != true || cats["cloud-security"]["is_default"] != false {
		t.Fatalf("default after refused set-default: operational=%v cloud-security=%v",
			cats["operational"]["is_default"], cats["cloud-security"]["is_default"])
	}
}
