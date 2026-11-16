//go:build integration

// #1809 — organisation-owned risk categories, expand step of #1745.
// Asserts against a live Postgres:
//
//  1. The migration seeds every existing tenant with its own seven
//     categories and points every existing risk at its own tenant's
//     category whose slug equals the risk's enum value; per-tenant counts
//     match.
//  2. Tenant B cannot read tenant A's categories (RLS).
//  3. A write that sends only the enum gets the matching reference, and a
//     reference that disagrees with the enum is refused.
//
// Lives in internal/db/ so the local `just test-integration` package set
// runs it as well as CI.

package db_test

import (
	"context"
	"errors"
	"os"
	"slices"
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
)

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
	up, err := os.ReadFile(riskCategoriesUp)
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	down, err := os.ReadFile(riskCategoriesDown)
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// An atlas_migrate transaction that always rolls back, so the schema and
	// data are untouched for later tests.
	tx, err := adminPool.Begin(ctx)
	if err != nil {
		t.Fatalf("admin Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Back to the pre-#1809 schema: risks carry only the enum.
	if _, err := tx.Exec(ctx, string(down)); err != nil {
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

	if _, err := tx.Exec(ctx, string(up)); err != nil {
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

func TestRiskCategories_DualWriteKeepsReferenceAndEnumInAgreement(t *testing.T) {
	tenant := uuid.NewString()
	withTenantTx(t, tenant, false, func(ctx context.Context, tx pgx.Tx) {
		// Enum only: a bare tenant with no categories is seeded and the
		// risk gets the matching reference.
		riskID := uuid.NewString()
		if _, err := tx.Exec(ctx, `
			INSERT INTO risks (id, tenant_id, title, category, treatment)
			VALUES ($1, $2, 'enum only', 'privacy', 'avoid')`, riskID, tenant); err != nil {
			t.Fatalf("INSERT enum-only risk: %v", err)
		}
		assertRiskCategory(ctx, t, tx, riskID, "privacy")

		// Changing the enum moves the reference.
		if _, err := tx.Exec(ctx,
			`UPDATE risks SET category = 'financial' WHERE id = $1`, riskID); err != nil {
			t.Fatalf("UPDATE category: %v", err)
		}
		assertRiskCategory(ctx, t, tx, riskID, "financial")

		// Changing the reference moves the enum.
		if _, err := tx.Exec(ctx, `
			UPDATE risks SET category_id =
			    (SELECT id FROM org_risk_categories WHERE tenant_id = $2 AND slug = 'integrity')
			WHERE id = $1`, riskID, tenant); err != nil {
			t.Fatalf("UPDATE category_id: %v", err)
		}
		assertRiskCategory(ctx, t, tx, riskID, "integrity")

		// A disagreeing pair is refused.
		err := inSavepoint(ctx, t, tx, func(sp pgx.Tx) error {
			_, err := sp.Exec(ctx, `
				INSERT INTO risks (id, tenant_id, title, category, treatment, category_id)
				VALUES ($1, $2, 'mismatch', 'privacy', 'avoid',
				    (SELECT id FROM org_risk_categories WHERE tenant_id = $2 AND slug = 'financial'))`,
				uuid.NewString(), tenant)
			return err
		})
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != pgErrCheckViolation {
			t.Fatalf("disagreeing category pair: want SQLSTATE %s, got %v", pgErrCheckViolation, err)
		}
	})
}

func assertRiskCategory(ctx context.Context, t *testing.T, tx pgx.Tx, riskID, wantSlug string) {
	t.Helper()
	var enum, slug string
	if err := tx.QueryRow(ctx, `
		SELECT r.category::text, c.slug FROM risks r
		JOIN org_risk_categories c ON c.id = r.category_id AND c.tenant_id = r.tenant_id
		WHERE r.id = $1`, riskID).Scan(&enum, &slug); err != nil {
		t.Fatalf("read risk category: %v", err)
	}
	if enum != wantSlug || slug != wantSlug {
		t.Errorf("risk category enum=%s reference slug=%s, want both %s", enum, slug, wantSlug)
	}
}
