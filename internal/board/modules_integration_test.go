//go:build integration

package board_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/internal/board"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/featureflag"
	"github.com/mgoodric/security-atlas/internal/moduleaccess"
)

// #2175: a board pack generated while Third-party risk is on keeps its
// vendor burndown after the module is switched off; a pack generated
// afterwards has none, can still be published, and edits to the omitted
// section are refused.
func TestPackGenerator_VendorBurndownFollowsSwitchAtGeneration(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), `DELETE FROM feature_flags WHERE tenant_id = $1`, tenant); err != nil {
			t.Logf("cleanup feature_flags: %v", err)
		}
	})
	ctx := ctxFor(t, tenant)
	flags := featureflag.NewStore(app)
	store := board.NewPackStore(app)
	gen := board.NewPackGenerator(store,
		fixedVendorBurndown{out: board.VendorBurndownReadout{Total: 4, OnTime: 3, PastDue: 1}},
	).WithClock(func() time.Time { return time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC) }).
		WithSwitches(moduleaccess.NewViews(moduleaccess.NewStore(app), flags))

	before, err := gen.Generate(ctx, "2026-06-30")
	if err != nil {
		t.Fatalf("Generate (on): %v", err)
	}
	if _, ok := before.Content.Sections[board.SectionVendorBurndown]; !ok || len(before.Content.Omitted) != 0 {
		t.Fatalf("switched on: vendor burndown missing or sections omitted: %v", before.Content.Omitted)
	}

	if _, err := flags.Set(ctx, "vendor.enabled", false, "test", "#2175"); err != nil {
		t.Fatalf("switch off: %v", err)
	}

	// The pack generated earlier keeps every section, data and narrative.
	kept, err := store.Get(ctx, before.ID)
	if err != nil {
		t.Fatalf("Get earlier pack: %v", err)
	}
	if vb, ok := kept.Content.Sections[board.SectionVendorBurndown]; !ok || vb.Data.VendorBurndownTotal != 4 {
		t.Fatalf("earlier pack lost its vendor burndown after the switch: %+v", vb)
	}
	if !strings.Contains(kept.NarrativeMd, "Vendor risk burndown") {
		t.Error("earlier pack's narrative lost its vendor burndown section")
	}

	after, err := gen.Generate(ctx, "2026-06-30")
	if err != nil {
		t.Fatalf("Generate (off): %v", err)
	}
	if _, ok := after.Content.Sections[board.SectionVendorBurndown]; ok {
		t.Fatal("switched off: new pack still has a vendor burndown section")
	}
	if !slices.Equal(after.Content.Omitted, []string{board.SectionVendorBurndown}) {
		t.Fatalf("switched off: omitted = %v; want [vendor_burndown]", after.Content.Omitted)
	}
	if strings.Contains(after.NarrativeMd, "Vendor risk burndown") {
		t.Error("switched off: new pack's narrative has a vendor burndown section")
	}
	if _, ok := after.Content.Sections[board.SectionTopRisks]; !ok {
		t.Error("switched off: Third-party risk took Risk's top_risks section with it")
	}

	approve := true
	if _, err := store.UpdateSection(ctx, after.ID, board.SectionEdit{SectionKey: board.SectionVendorBurndown, Approved: &approve}); !errors.Is(err, board.ErrUnknownSection) {
		t.Fatalf("approving the omitted section: err = %v; want ErrUnknownSection", err)
	}
	for _, key := range after.Content.Keys() {
		if _, err := store.UpdateSection(ctx, after.ID, board.SectionEdit{SectionKey: key, Approved: &approve}); err != nil {
			t.Fatalf("approve %s: %v", key, err)
		}
	}
	if _, err := store.Publish(ctx, after.ID, "admin@example.test"); err != nil {
		t.Fatalf("publish a pack with an omitted section: %v", err)
	}
}
