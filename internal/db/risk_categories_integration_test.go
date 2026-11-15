//go:build integration

// #1809 / #1819 — organisation-owned risk categories, the expand and
// contract steps of #1745. Asserts against a live Postgres:
//
//  1. The expand migration seeds every existing tenant with its own seven
//     categories and points every existing risk at its own tenant's
//     category whose slug equals the risk's enum value; per-tenant counts
//     match. The contract migration then drops the enum column and type
//     and leaves every reference as it was.
//  2. Tenant B cannot read tenant A's categories (RLS).
//  3. The contract migration refuses to drop the enum while any risk's
//     reference disagrees with its enum value.
//  4. risk_category_id(tenant, slug) seeds a bare tenant and resolves a
//     slug to that tenant's category, and refuses an unknown slug.
//
// Lives in internal/db/ so the local `just test-integration` package set
// runs it as well as CI.

package db_test

import (
	"context"
	"errors"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const (
	riskCategoriesUp   = "../../migrations/sql/20260923000000_risk_categories.sql"
	riskCategoriesDown = "../../migrations/sql/20260923000000_risk_categories.down.sql"
	dropEnumUp         = "../../migrations/sql/20261121000000_drop_risk_category_enum.sql"
	dropEnumDown       = "../../migrations/sql/20261121000000_drop_risk_category_enum.down.sql"
)

func readMigration(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration %s: %v", path, err)
	}
	return string(b)
}

// riskCategoryColumnExists reports whether risks still has the enum column.
func riskCategoryColumnExists(ctx context.Context, t *testing.T, tx pgx.Tx) bool {
	t.Helper()
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
		    SELECT 1 FROM information_schema.columns
		    WHERE table_schema = current_schema()
		      AND table_name = 'risks' AND column_name = 'category')`,
	).Scan(&exists); err != nil {
		t.Fatalf("read risks columns: %v", err)
	}
	return exists
}

var seededCategorySlugs = []string{
	"availability", "confidentiality", "financial", "integrity",
	"operational", "privacy", "regulatory",
}

// categoryFixtureTenant is one tenant of the #1745 testing fixture: risks
// across several enum values, an aggregation rule targeting a theme, and
// risks tagged with that theme.
type categoryFixtureTenant struct {
	id         string
	categories []string // one risk per entry
	theme      string
}

func seedCategoryFixture(ctx context.Context, t *testing.T, tx pgx.Tx, ft categoryFixtureTenant) {
	t.Helper()
	if _, err := tx.Exec(ctx,
		`INSERT INTO tenants (id, name, is_bootstrap_tenant) VALUES ($1, $2, false)`,
		ft.id, "cat-1809-"+ft.id[:8],
	); err != nil {
		t.Fatalf("INSERT tenants: %v", err)
	}
	for i, cat := range ft.categories {
		themes := []string{}
		if i%2 == 0 {
			themes = []string{ft.theme}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO risks (id, tenant_id, title, category, treatment, themes)
			VALUES ($1, $2, $3, $4::risk_category, 'avoid', $5::text[])
		`, uuid.NewString(), ft.id, "risk "+cat, cat, themes); err != nil {
			t.Fatalf("INSERT risks (%s): %v", cat, err)
		}
	}
	ruleID := uuid.NewString()
	if _, err := tx.Exec(ctx, `
		INSERT INTO aggregation_rules (
		    id, tenant_id, rule_id, target_theme, min_risks, min_teams,
		    window_days, parent_level, severity_function, rule_body
		) VALUES ($1, $2, $3, $4, 3, 2, 30, 'team', 'max', '{}'::jsonb)
	`, ruleID, ft.id, "rule-"+ruleID[:8], ft.theme); err != nil {
		t.Fatalf("INSERT aggregation_rules: %v", err)
	}
}

func TestRiskCategoriesMigration_BackfillsEveryRiskToItsTenantsCategory(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; the migration test needs the migrate role")
	}
	up := readMigration(t, riskCategoriesUp)
	down := readMigration(t, riskCategoriesDown)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// An atlas_migrate transaction that always rolls back, so the schema and
	// data are untouched for later tests.
	tx, err := adminPool.Begin(ctx)
	if err != nil {
		t.Fatalf("admin Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Back to the pre-#1809 schema: undo the contract step, then the
	// expand step, so risks carry only the enum.
	if _, err := tx.Exec(ctx, readMigration(t, dropEnumDown)); err != nil {
		t.Fatalf("apply contract down migration: %v", err)
	}
	if _, err := tx.Exec(ctx, down); err != nil {
		t.Fatalf("apply down migration: %v", err)
	}
	fixture := []categoryFixtureTenant{
		{id: uuid.NewString(), theme: "ownership",
			categories: []string{"confidentiality", "privacy", "privacy", "operational", "financial"}},
		{id: uuid.NewString(), theme: "access-control",
			categories: []string{"integrity", "availability", "regulatory", "privacy"}},
	}
	for _, ft := range fixture {
		seedCategoryFixture(ctx, t, tx, ft)
	}

	if _, err := tx.Exec(ctx, up); err != nil {
		t.Fatalf("apply up migration: %v", err)
	}

	for _, ft := range fixture {
		// Seven categories, the old slugs, operational the only default.
		rows, err := tx.Query(ctx, `
			SELECT slug, is_default FROM org_risk_categories
			WHERE tenant_id = $1 ORDER BY slug`, ft.id)
		if err != nil {
			t.Fatalf("query categories: %v", err)
		}
		var slugs, defaults []string
		for rows.Next() {
			var slug string
			var isDefault bool
			if err := rows.Scan(&slug, &isDefault); err != nil {
				t.Fatalf("scan category: %v", err)
			}
			slugs = append(slugs, slug)
			if isDefault {
				defaults = append(defaults, slug)
			}
		}
		rows.Close()
		if !slices.Equal(slugs, seededCategorySlugs) {
			t.Errorf("tenant %s categories = %v, want %v", ft.id, slugs, seededCategorySlugs)
		}
		if !slices.Equal(defaults, []string{"operational"}) {
			t.Errorf("tenant %s defaults = %v, want [operational]", ft.id, defaults)
		}

		// Every risk references its own tenant's category with the same slug.
		var total, matched int
		if err := tx.QueryRow(ctx, `
			SELECT count(*),
			       count(*) FILTER (WHERE c.tenant_id = r.tenant_id AND c.slug = r.category::text)
			FROM risks r
			LEFT JOIN org_risk_categories c ON c.id = r.category_id
			WHERE r.tenant_id = $1`, ft.id).Scan(&total, &matched); err != nil {
			t.Fatalf("count risks: %v", err)
		}
		if total != len(ft.categories) || matched != total {
			t.Errorf("tenant %s: %d risks, %d reference a matching own category; want %d and %d",
				ft.id, total, matched, len(ft.categories), len(ft.categories))
		}

		// Per-category counts match the enum distribution.
		want := map[string]int{}
		for _, c := range ft.categories {
			want[c]++
		}
		got := map[string]int{}
		rows, err = tx.Query(ctx, `
			SELECT c.slug, count(*) FROM risks r
			JOIN org_risk_categories c ON c.id = r.category_id
			WHERE r.tenant_id = $1 GROUP BY c.slug`, ft.id)
		if err != nil {
			t.Fatalf("group risks: %v", err)
		}
		for rows.Next() {
			var slug string
			var n int
			if err := rows.Scan(&slug, &n); err != nil {
				t.Fatalf("scan group: %v", err)
			}
			got[slug] = n
		}
		rows.Close()
		if len(got) != len(want) {
			t.Errorf("tenant %s per-category counts = %v, want %v", ft.id, got, want)
		}
		for slug, n := range want {
			if got[slug] != n {
				t.Errorf("tenant %s category %s: %d risks, want %d", ft.id, slug, got[slug], n)
			}
		}
	}

	// The themes and the aggregation rule are untouched by the migration.
	for _, ft := range fixture {
		var tagged, rules int
		if err := tx.QueryRow(ctx, `
			SELECT (SELECT count(*) FROM risks WHERE tenant_id = $1 AND themes @> ARRAY[$2::text]),
			       (SELECT count(*) FROM aggregation_rules WHERE tenant_id = $1 AND target_theme = $2)`,
			ft.id, ft.theme).Scan(&tagged, &rules); err != nil {
			t.Fatalf("count themes: %v", err)
		}
		if wantTagged := (len(ft.categories) + 1) / 2; tagged != wantTagged || rules != 1 {
			t.Errorf("tenant %s: %d risks tagged %s and %d rules, want %d and 1",
				ft.id, tagged, ft.theme, rules, wantTagged)
		}
	}

	// #1819: the contract step drops the enum and keeps every reference.
	if _, err := tx.Exec(ctx, readMigration(t, dropEnumUp)); err != nil {
		t.Fatalf("apply contract migration: %v", err)
	}
	if riskCategoryColumnExists(ctx, t, tx) {
		t.Error("risks.category still exists after the contract migration")
	}
	var typeExists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'risk_category')`,
	).Scan(&typeExists); err != nil {
		t.Fatalf("read pg_type: %v", err)
	}
	if typeExists {
		t.Error("type risk_category still exists after the contract migration")
	}
	for _, ft := range fixture {
		want := map[string]int{}
		for _, c := range ft.categories {
			want[c]++
		}
		rows, err := tx.Query(ctx, `
			SELECT c.slug, count(*) FROM risks r
			JOIN org_risk_categories c ON c.id = r.category_id AND c.tenant_id = r.tenant_id
			WHERE r.tenant_id = $1 GROUP BY c.slug`, ft.id)
		if err != nil {
			t.Fatalf("group risks after contract: %v", err)
		}
		got := map[string]int{}
		for rows.Next() {
			var slug string
			var n int
			if err := rows.Scan(&slug, &n); err != nil {
				t.Fatalf("scan group: %v", err)
			}
			got[slug] = n
		}
		rows.Close()
		if !maps.Equal(got, want) {
			t.Errorf("tenant %s per-category counts after contract = %v, want %v", ft.id, got, want)
		}
	}
}

func TestRiskCategories_RLSIsolatesTenants(t *testing.T) {
	tenantA := uuid.NewString()
	tenantB := uuid.NewString()

	withTenantTx(t, tenantA, false, func(ctx context.Context, tx pgx.Tx) {
		if _, err := tx.Exec(ctx, `SELECT seed_risk_categories($1)`, tenantA); err != nil {
			t.Fatalf("seed A: %v", err)
		}
		// Seeding another tenant from tenant A's session is refused by the
		// write policy.
		err := inSavepoint(ctx, t, tx, func(sp pgx.Tx) error {
			_, err := sp.Exec(ctx, `SELECT seed_risk_categories($1)`, tenantB)
			return err
		})
		if err == nil {
			t.Fatal("tenant A seeded tenant B's categories; want an RLS refusal")
		}

		ctxB, err := tenancy.WithTenant(ctx, tenantB)
		if err != nil {
			t.Fatalf("WithTenant(B): %v", err)
		}
		if err := tenancy.ApplyTenant(ctxB, tx); err != nil {
			t.Fatalf("ApplyTenant(B): %v", err)
		}
		var visible int
		if err := tx.QueryRow(ctxB,
			`SELECT count(*) FROM org_risk_categories WHERE tenant_id = $1`, tenantA,
		).Scan(&visible); err != nil {
			t.Fatalf("count A's categories as B: %v", err)
		}
		if visible != 0 {
			t.Errorf("tenant B sees %d of tenant A's categories, want 0", visible)
		}
	})
}

// #1819: the contract migration refuses to drop the enum while a risk's
// reference and enum value disagree, and drops nothing in that case.
func TestDropRiskCategoryEnum_RefusesADisagreeingRisk(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; the migration test needs the migrate role")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tx, err := adminPool.Begin(ctx)
	if err != nil {
		t.Fatalf("admin Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Back to the dual-written schema, with one risk whose enum says
	// privacy while its reference says financial. The sync trigger refuses
	// that pair, so it is disabled for the one write: the state the guard
	// exists for is one the trigger should never have let through.
	if _, err := tx.Exec(ctx, readMigration(t, dropEnumDown)); err != nil {
		t.Fatalf("apply contract down migration: %v", err)
	}
	// The down step gives a risk on a custom category (another test's, in
	// this shared database) the placeholder `operational`, which disagrees
	// with its reference. Point those at their tenant's `operational`
	// category so that the only disagreement is the one made below.
	if _, err := tx.Exec(ctx, `
		UPDATE risks r SET category_id = c.id
		FROM org_risk_categories c, org_risk_categories cur
		WHERE c.tenant_id = r.tenant_id AND c.slug = r.category::text
		  AND cur.tenant_id = r.tenant_id AND cur.id = r.category_id
		  AND cur.slug <> r.category::text`); err != nil {
		t.Fatalf("normalise placeholder risks: %v", err)
	}
	tenant := uuid.NewString()
	riskID := uuid.NewString()
	if _, err := tx.Exec(ctx, `
		INSERT INTO risks (id, tenant_id, title, category, treatment)
		VALUES ($1, $2, 'disagreeing', 'privacy', 'avoid')`, riskID, tenant); err != nil {
		t.Fatalf("INSERT risk: %v", err)
	}
	// With every pair in agreement the guard lets the drop through.
	if err := inSavepoint(ctx, t, tx, func(sp pgx.Tx) error {
		_, err := sp.Exec(ctx, readMigration(t, dropEnumUp))
		return err
	}); err != nil {
		t.Fatalf("contract migration over agreeing risks: %v", err)
	}
	if _, err := tx.Exec(ctx, `ALTER TABLE risks DISABLE TRIGGER risks_category_sync_trg`); err != nil {
		t.Fatalf("disable sync trigger: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE risks SET category_id =
		    (SELECT id FROM org_risk_categories WHERE tenant_id = $2 AND slug = 'financial')
		WHERE id = $1`, riskID, tenant); err != nil {
		t.Fatalf("UPDATE category_id: %v", err)
	}
	if _, err := tx.Exec(ctx, `ALTER TABLE risks ENABLE TRIGGER risks_category_sync_trg`); err != nil {
		t.Fatalf("enable sync trigger: %v", err)
	}

	err = inSavepoint(ctx, t, tx, func(sp pgx.Tx) error {
		_, err := sp.Exec(ctx, readMigration(t, dropEnumUp))
		return err
	})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != pgErrCheckViolation {
		t.Fatalf("contract migration over a disagreeing risk: want SQLSTATE %s, got %v", pgErrCheckViolation, err)
	}
	if !strings.Contains(pgErr.Message, "refusing to drop risks.category: 1 risk(s)") || !strings.Contains(pgErr.Message, riskID) {
		t.Errorf("guard message = %q, want it to name the one disagreeing risk %s", pgErr.Message, riskID)
	}
	if !riskCategoryColumnExists(ctx, t, tx) {
		t.Error("risks.category was dropped despite the guard")
	}
}

// #1819: a SQL writer names a category by slug through risk_category_id,
// which seeds a bare tenant's categories on the way, as the dropped sync
// trigger did.
func TestRiskCategoryID_ResolvesASlugForABareTenant(t *testing.T) {
	tenant := uuid.NewString()
	withTenantTx(t, tenant, false, func(ctx context.Context, tx pgx.Tx) {
		riskID := uuid.NewString()
		if _, err := tx.Exec(ctx, `
			INSERT INTO risks (id, tenant_id, title, category_id, treatment)
			VALUES ($1, $2, 'by slug', risk_category_id($2, 'privacy'), 'avoid')`, riskID, tenant); err != nil {
			t.Fatalf("INSERT risk by slug: %v", err)
		}
		var slug string
		if err := tx.QueryRow(ctx, `
			SELECT c.slug FROM risks r
			JOIN org_risk_categories c ON c.id = r.category_id AND c.tenant_id = r.tenant_id
			WHERE r.id = $1`, riskID).Scan(&slug); err != nil {
			t.Fatalf("read risk category: %v", err)
		}
		if slug != "privacy" {
			t.Errorf("risk category slug = %s, want privacy", slug)
		}

		err := inSavepoint(ctx, t, tx, func(sp pgx.Tx) error {
			_, err := sp.Exec(ctx, `SELECT risk_category_id($1, 'no-such-category')`, tenant)
			return err
		})
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != pgErrForeignKeyViolation {
			t.Fatalf("unknown slug: want SQLSTATE %s, got %v", pgErrForeignKeyViolation, err)
		}
	})
}
