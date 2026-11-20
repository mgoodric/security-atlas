//go:build integration

package scfimport_test

// #2568: each tenant's overlay carry runs as atlas_app under that tenant,
// and one tenant's failed carry does not stop the others. The fixtures are
// #1804's (overlayremap_integration_test.go): TRM-01 keeps its code and
// title in release B, so an overlay edge on it qualifies for the carry.

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/mappingoverlay"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// setupTwoTenantCarry gives two tenants each an overlay edge on TRM-01 and
// imports release B, without queueing the overlay remaps. The tenants are
// returned in the order the carry visits them.
func setupTwoTenantCarry(t *testing.T) (admin *pgxpool.Pool, tenants []uuid.UUID) {
	t.Helper()
	admin, _ = setupEdgeRemap(t, func(admin *pgxpool.Pool) {
		cleanup := []string{"scf_overlay_remap_decisions", "scf_overlay_remap_proposals", "fw_to_scf_overlay_edges"}
		store := mappingoverlay.NewStore(dbtest.NewAppPool(t))
		for range 2 {
			tenant := uuid.MustParse(dbtest.SeedTenant(t, admin, cleanup...))
			if _, err := store.Create(withTenant(t, tenant.String()), newOverlay(tenantAOverlays["TRM-01"]), "seed"); err != nil {
				t.Fatalf("overlay on TRM-01 in %s: %v", tenant, err)
			}
			tenants = append(tenants, tenant)
		}
	})
	slices.SortFunc(tenants, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	return admin, tenants
}

func TestOverlayCarry_RunsAsAtlasAppUnderEachTenant(t *testing.T) {
	admin, tenants := setupTwoTenantCarry(t)
	type seen struct{ role, tenant string }
	var got []seen
	scfimport.SetCarryTenantHook(t, func(ctx context.Context, tx pgx.Tx, _ uuid.UUID) error {
		var s seen
		if err := tx.QueryRow(ctx, "SELECT current_user, current_setting($1, true)", tenancy.GUCName).Scan(&s.role, &s.tenant); err != nil {
			return err
		}
		got = append(got, s)
		return nil
	})
	report, err := scfimport.QueueOverlayRemapProposals(context.Background(), admin)
	if err != nil {
		t.Fatalf("QueueOverlayRemapProposals: %v", err)
	}
	if report.Carried != 2 || len(report.Failed) != 0 {
		t.Fatalf("report carried=%d failed=%v; want 2 carried, none failed", report.Carried, report.Failed)
	}
	want := []seen{{"atlas_app", tenants[0].String()}, {"atlas_app", tenants[1].String()}}
	if !slices.Equal(got, want) {
		t.Fatalf("carry transactions ran as %v; want %v", got, want)
	}
}

func TestOverlayCarry_OneTenantsFailureDoesNotStopTheRest(t *testing.T) {
	admin, tenants := setupTwoTenantCarry(t)
	failing, other := tenants[0], tenants[1]
	boom := errors.New("injected carry failure")
	scfimport.SetCarryTenantHook(t, func(_ context.Context, _ pgx.Tx, tenant uuid.UUID) error {
		if tenant == failing {
			return boom
		}
		return nil
	})
	report, err := scfimport.QueueOverlayRemapProposals(context.Background(), admin)
	if err != nil {
		t.Fatalf("QueueOverlayRemapProposals returned %v; a tenant's failure belongs in the report", err)
	}
	if report.Carried != 1 || len(report.Failed) != 1 || report.Failed[0].TenantID != failing || !errors.Is(report.Failed[0].Err, boom) {
		t.Fatalf("report carried=%d failed=%+v; want 1 carried and %s failed with %v", report.Carried, report.Failed, failing, boom)
	}
	if s := overlayProposals(t, admin, failing)["TRM-01"].status; s != "open" {
		t.Errorf("failed tenant's proposal is %s; want open, for a re-run", s)
	}
	if s := overlayProposals(t, admin, other)["TRM-01"].status; s != "approved" {
		t.Errorf("the other tenant's proposal is %s; want approved (carried)", s)
	}

	// Once the fault clears, a re-run carries the tenant that failed.
	scfimport.SetCarryTenantHook(t, nil)
	report, err = scfimport.QueueOverlayRemapProposals(context.Background(), admin)
	if err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if report.Carried != 1 || len(report.Failed) != 0 {
		t.Fatalf("re-run carried=%d failed=%+v; want the failed tenant's one edge carried", report.Carried, report.Failed)
	}
	carriedOverlay(t, admin, failing, overlayProposals(t, admin, failing)["TRM-01"].id)
}
