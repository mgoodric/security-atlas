//go:build integration

package demoseed_test

import (
	"context"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/demoseed"
	"github.com/mgoodric/security-atlas/internal/featureflag"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// routeOn reads, through the strict module read the gate uses
// (featureflag.RouteOn), whether path is switched on for tenantID.
func routeOn(t *testing.T, tenantID uuid.UUID, path string) bool {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenantID.String())
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", path, nil).WithContext(ctx)
	on, err := featureflag.RouteOn(r, featureflag.NewStore(adminPool), path)
	if err != nil {
		t.Fatalf("RouteOn(%s): %v", path, err)
	}
	return on
}

// switchAuditRows counts the demo seed's feature_flag_audit_log rows for
// key in tenantID.
func switchAuditRows(t *testing.T, tenantID uuid.UUID, key string) int {
	t.Helper()
	var n int
	if err := adminPool.QueryRow(context.Background(), `
		SELECT count(*) FROM feature_flag_audit_log
		WHERE tenant_id = $1 AND flag_key = $2 AND actor LIKE 'demo-seed:%' AND to_enabled`,
		tenantID, key,
	).Scan(&n); err != nil {
		t.Fatalf("count switch audit rows: %v", err)
	}
	return n
}

// #2590: the demo seed switches on the modules whose data it writes. One
// tenant has Exceptions and Risk switched off before an in-place seed;
// after it, both read on through RouteOn, each with one audit row naming
// the seed. A second, new demo tenant has Risk on by default: the seed
// leaves Risk alone (no audit row) and switches on Board reporting, which
// is off by default.
func TestSeed2590_SwitchesOnTheModulesItSeeds(t *testing.T) {
	ctx := context.Background()

	off := freshInstallTenant(t)
	tctx, err := tenancy.WithTenant(ctx, off.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"exceptions.enabled", "risk.enabled"} {
		if _, err := featureflag.NewStore(adminPool).Set(tctx, key, false, "test-2590", "test"); err != nil {
			t.Fatalf("switch off %s: %v", key, err)
		}
	}
	if routeOn(t, off.ID, "/v1/exceptions") || routeOn(t, off.ID, "/v1/risks") {
		t.Fatal("precondition: exceptions and risks should read off")
	}
	actor := uuid.New()
	res, err := soleTenantSeeder(t, off).Seed(ctx, demoseed.SeedInput{ActorUserID: actor})
	if err != nil {
		t.Fatalf("Seed in place: %v", err)
	}
	for _, p := range []string{"/v1/exceptions", "/v1/risks", "/v1/board-packs"} {
		if !routeOn(t, off.ID, p) {
			t.Errorf("in place: %s still switched off after the seed", p)
		}
	}
	for _, key := range []string{"exceptions.enabled", "risk.enabled", "board.reporting"} {
		if !slices.Contains(res.SwitchedOn, key) {
			t.Errorf("in place: SwitchedOn = %v; want %s", res.SwitchedOn, key)
		}
		if n := switchAuditRows(t, off.ID, key); n != 1 {
			t.Errorf("in place: %d seed audit rows for %s; want 1", n, key)
		}
	}

	const slug = "demo-it-2590"
	cleanupTenant(t, slug)
	seeder, err := demoseed.NewSeeder(adminPool, 0.2)
	if err != nil {
		t.Fatalf("NewSeeder: %v", err)
	}
	fresh, err := seeder.Apply(ctx, demoseed.ApplyInput{Slug: slug, ActorUserID: actor})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !routeOn(t, fresh.TenantID, "/v1/risks") || !routeOn(t, fresh.TenantID, "/v1/board-packs") {
		t.Error("new tenant: risks or board packs switched off after the seed")
	}
	if slices.Contains(fresh.SwitchedOn, "risk.enabled") || switchAuditRows(t, fresh.TenantID, "risk.enabled") != 0 {
		t.Errorf("new tenant: the seed rewrote risk.enabled, which was already on (SwitchedOn %v)", fresh.SwitchedOn)
	}
	if !slices.Contains(fresh.SwitchedOn, "board.reporting") {
		t.Errorf("new tenant: SwitchedOn = %v; want board.reporting", fresh.SwitchedOn)
	}
	if rowCount(t, "board_packs", fresh.TenantID) == 0 {
		t.Error("new tenant: no board packs seeded")
	}
}
