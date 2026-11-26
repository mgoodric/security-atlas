//go:build integration

// Integration tests for #1816: the demo seed writes each risk's category
// reference (category_id) on both the new-tenant and the in-place path.
//
// Run with: go test -tags=integration -p 1 ./internal/demoseed/...

package demoseed_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/demoseed"
)

// assertRiskCategoriesReferenced fails unless the tenant has risks and every
// one references a category of its own tenant.
func assertRiskCategoriesReferenced(t *testing.T, tenantID uuid.UUID) {
	t.Helper()
	var total, referenced int
	if err := adminPool.QueryRow(context.Background(), `
		SELECT count(*),
		       count(*) FILTER (WHERE c.id IS NOT NULL)
		FROM risks r
		LEFT JOIN org_risk_categories c
		       ON c.tenant_id = r.tenant_id AND c.id = r.category_id
		WHERE r.tenant_id = $1`, tenantID,
	).Scan(&total, &referenced); err != nil {
		t.Fatalf("read risk categories: %v", err)
	}
	if total == 0 {
		t.Fatal("tenant has no risks; want the demo risks")
	}
	if referenced != total {
		t.Errorf("%d of %d risks reference their own tenant's matching category, want all", referenced, total)
	}
}

func TestApply_RisksReferenceCategory(t *testing.T) {
	const slug = "demo-it-category"
	cleanupTenant(t, slug)

	seeder, err := demoseed.NewSeeder(adminPool, demoseed.DefaultScale)
	if err != nil {
		t.Fatalf("NewSeeder: %v", err)
	}
	res, err := seeder.Apply(context.Background(), demoseed.ApplyInput{Slug: slug})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	assertRiskCategoriesReferenced(t, res.TenantID)
}

func TestSeedInPlace_RisksReferenceCategory(t *testing.T) {
	ref := freshInstallTenant(t)
	res, err := soleTenantSeeder(t, ref).Seed(context.Background(), demoseed.SeedInput{})
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if !res.InPlace {
		t.Fatalf("Seed InPlace = false, want an in-place seed")
	}
	assertRiskCategoriesReferenced(t, ref.ID)
}
