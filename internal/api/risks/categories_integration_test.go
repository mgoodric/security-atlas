//go:build integration

// #1809 — organisation-owned risk categories, expand step of #1745.
// Exercises the HTTP surface against a real Postgres:
//
//   - GET /v1/risks/categories gives a new tenant its seven categories, the
//     old enum values as slugs, `operational` the only default;
//   - POST /v1/risks accepts the category by slug and by id, and the
//     risk's reference and enum agree;
//   - an unknown category is a 400 naming the valid slugs;
//   - tenant B cannot see tenant A's categories.

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

var seededCategorySlugs = []string{
	"availability", "confidentiality", "financial", "integrity",
	"operational", "privacy", "regulatory",
}

// freshCategoryTenant returns a new tenant id, cleaning up its risks and
// categories afterwards (risks first: they reference the categories).
func freshCategoryTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin,
		"risks",
		"org_risk_categories",
	)
}

// listCategories returns the tenant's categories keyed by slug.
func listCategories(t *testing.T, env testEnv) map[string]map[string]any {
	t.Helper()
	resp, body := get(t, env, "/v1/risks/categories")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/risks/categories: status %d, want 200 (%v)", resp.StatusCode, body)
	}
	raw, ok := body["categories"].([]any)
	if !ok {
		t.Fatalf("response has no categories array: %v", body)
	}
	out := map[string]map[string]any{}
	for _, item := range raw {
		c := item.(map[string]any)
		out[c["slug"].(string)] = c
	}
	return out
}

// storedCategory reads a risk's enum value and the slug and tenant of the
// category it references, straight from the table.
func storedCategory(t *testing.T, admin *pgxpool.Pool, riskID string) (enum, slug, categoryTenant, riskTenant string) {
	t.Helper()
	if err := admin.QueryRow(context.Background(), `
		SELECT r.category::text, c.slug, c.tenant_id::text, r.tenant_id::text
		FROM risks r JOIN org_risk_categories c ON c.id = r.category_id
		WHERE r.id = $1`, riskID).Scan(&enum, &slug, &categoryTenant, &riskTenant); err != nil {
		t.Fatalf("read stored category: %v", err)
	}
	return enum, slug, categoryTenant, riskTenant
}

func TestRiskCategories_NewTenantHasSevenSeededCategories(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshCategoryTenant(t, admin)
	env := testServer(t, app, tenant)

	cats := listCategories(t, env)
	slugs := make([]string, 0, len(cats))
	var defaults []string
	for slug, c := range cats {
		slugs = append(slugs, slug)
		if c["is_default"] == true {
			defaults = append(defaults, slug)
		}
		if name, _ := c["name"].(string); name == "" {
			t.Errorf("category %s has no display name", slug)
		}
		if c["archived"] != false {
			t.Errorf("category %s archived = %v, want false", slug, c["archived"])
		}
	}
	slices.Sort(slugs)
	if !slices.Equal(slugs, seededCategorySlugs) {
		t.Fatalf("slugs = %v, want %v", slugs, seededCategorySlugs)
	}
	if !slices.Equal(defaults, []string{"operational"}) {
		t.Fatalf("defaults = %v, want [operational]", defaults)
	}

	// Listing again does not seed a second copy.
	if again := listCategories(t, env); len(again) != len(seededCategorySlugs) {
		t.Fatalf("second list has %d categories, want %d", len(again), len(seededCategorySlugs))
	}
}

func TestRiskCategories_CreateRiskBySlugAndByID(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshCategoryTenant(t, admin)
	env := testServer(t, app, tenant)

	privacyID := listCategories(t, env)["privacy"]["id"].(string)

	for _, ref := range []string{"privacy", privacyID} {
		resp, body := post(t, env, "/v1/risks", map[string]any{
			"title":          "category " + ref,
			"category":       ref,
			"inherent_score": map[string]int{"likelihood": 2, "impact": 3},
		}, env.bearer)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create with category %q: status %d, want 201 (%v)", ref, resp.StatusCode, body)
		}
		if got := body["risk"].(map[string]any)["category"]; got != "privacy" {
			t.Errorf("create with category %q: response category = %v, want privacy", ref, got)
		}
		riskID := body["risk"].(map[string]any)["id"].(string)
		enum, slug, catTenant, riskTenant := storedCategory(t, admin, riskID)
		if enum != "privacy" || slug != "privacy" {
			t.Errorf("create with category %q: stored enum=%s reference slug=%s, want both privacy", ref, enum, slug)
		}
		if catTenant != riskTenant || riskTenant != tenant {
			t.Errorf("create with category %q: category tenant %s, risk tenant %s, want both %s",
				ref, catTenant, riskTenant, tenant)
		}
	}
}

func TestRiskCategories_UnknownCategoryIsRejectedWithValidSlugs(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshCategoryTenant(t, admin)
	env := testServer(t, app, tenant)

	for _, ref := range []string{"technical", uuid.NewString()} {
		resp, body := post(t, env, "/v1/risks", map[string]any{
			"title":          "unknown category",
			"category":       ref,
			"inherent_score": map[string]int{"likelihood": 2, "impact": 3},
		}, env.bearer)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("create with category %q: status %d, want 400 (%v)", ref, resp.StatusCode, body)
		}
		msg, _ := body["error"].(string)
		for _, slug := range seededCategorySlugs {
			if !strings.Contains(msg, slug) {
				t.Errorf("create with category %q: error %q does not name valid slug %s", ref, msg, slug)
			}
		}
	}
}

func TestRiskCategories_TenantBCannotSeeTenantAsCategories(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantA := freshCategoryTenant(t, admin)
	tenantB := freshCategoryTenant(t, admin)
	envA := testServer(t, app, tenantA)
	envB := testServer(t, app, tenantB)

	catsA := listCategories(t, envA)
	catsB := listCategories(t, envB)
	for slug, a := range catsA {
		if b, ok := catsB[slug]; !ok || b["id"] == a["id"] {
			t.Errorf("category %s: tenant B has %v, want its own copy distinct from A's %v", slug, b, a["id"])
		}
	}

	// Tenant B cannot file a risk under tenant A's category id.
	resp, body := post(t, envB, "/v1/risks", map[string]any{
		"title":          "cross-tenant category",
		"category":       catsA["privacy"]["id"],
		"inherent_score": map[string]int{"likelihood": 2, "impact": 3},
	}, envB.bearer)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("tenant B with tenant A's category id: status %d, want 400 (%v)", resp.StatusCode, body)
	}
}
