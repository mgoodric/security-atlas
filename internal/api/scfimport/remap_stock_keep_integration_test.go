//go:build integration

package scfimport_test

// #1776 review B1: a person's approved re-anchor of a stock control
// survives the boot seed. Bootstrap re-uploads every controls/soc2/
// manifest on each boot; without the guard, that shipped manifest would
// supersede the approved revision and put the control back on its legacy
// anchor while the history still said "approved". The fixtures are
// testdata/remap-stock-release-*.json. Against release A, one release B
// splits CHG-02 (so the shipped code no longer resolves), the other reuses
// the code CHG-02 for another control (so it still resolves, and an
// unguarded upload would silently move the control onto it).

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

func TestRemapDecide_ApprovedStockReanchorSurvivesBootSeed(t *testing.T) {
	for _, c := range []struct{ releaseB, class, scfID string }{
		{"b", "split", "CHG-21"},
		{"b-reused", "reused_id", ""},
	} {
		t.Run(c.class, func(t *testing.T) { approvedStockSurvivesBootSeed(t, c.releaseB, c.class, c.scfID) })
	}
}

func approvedStockSurvivesBootSeed(t *testing.T, releaseB, wantClass, scfID string) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	truncateCatalog(t, admin)
	ctx := context.Background()

	// The shipped bytes, pinned to test-2026.3: the synthetic release A is
	// imported under that label so they bind in it (#1774).
	stockYAML := stockManifestShipped(t, stockCC81Bundle)
	if !scfimport.IsStockManifest(stockCC81Bundle, string(stockYAML)) {
		t.Fatalf("controls/soc2/%s/control.yaml is not in the stock manifest list", stockCC81Bundle)
	}
	bootSeed := func(tenant string) control.UploadResult {
		t.Helper()
		b, err := control.FinalizeBundleForHTTP(stockYAML)
		if err != nil {
			t.Fatalf("stock bundle: %v", err)
		}
		res, err := control.NewStore(app).Upload(withTenant(t, tenant), b, "oauth_client:bootstrap")
		if err != nil {
			t.Fatalf("boot seed upload: %v", err)
		}
		return res
	}
	load := func(name string) *scfimport.Catalog {
		c, err := scfimport.Load("testdata/remap-stock-release-" + name + ".json")
		if err != nil {
			t.Fatalf("Load %s: %v", name, err)
		}
		return c
	}

	importAsShippedRelease(t, admin, load("a"))
	tenant := dbtest.SeedTenant(t, admin, "scf_remap_decisions", "controls")
	legacy := bootSeed(tenant).ControlID
	catB := load(releaseB)
	importRelease(t, admin, catB)
	if _, err := scfimport.RebindControls(ctx, admin); err != nil {
		t.Fatalf("RebindControls: %v", err)
	}
	var proposalID, class string
	if err := admin.QueryRow(ctx, `SELECT id::text, class FROM scf_remap_proposals WHERE control_id = $1`, legacy).Scan(&proposalID, &class); err != nil {
		t.Fatalf("read proposal: %v", err)
	}
	if class != wantClass {
		t.Fatalf("proposal class %s; want %s", class, wantClass)
	}

	d, err := control.NewStore(app).ApproveRemap(withTenant(t, tenant), control.RemapApproveInput{
		ProposalID: proposalID, SCFID: scfID, DecidedBy: uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	approvedID := uuid.MustParse(d.NewControlID)
	approvedAnchor := anchorID(t, admin, catB.ReleaseVersion, "CHG-21")

	// The next boot: the shipped manifest is uploaded again, then the
	// SCF import's re-bind runs again.
	res := bootSeed(tenant)
	if !res.KeptReviewedAnchor || res.ControlID != approvedID || res.SupersededID != uuid.Nil ||
		!slices.Contains(res.Warnings, control.KeptReviewedAnchorWarning) {
		t.Fatalf("boot seed result = %+v; want the approved revision kept, with the warning", res)
	}
	if _, err := scfimport.RebindControls(ctx, admin); err != nil {
		t.Fatalf("repeat RebindControls: %v", err)
	}

	id, version, anchor := activeControl(t, admin, tenant, legacy)
	if id != approvedID || version != 2 || anchor != approvedAnchor {
		t.Fatalf("after the boot seed the control is %s v%d on %s; want the approved %s v2 on CHG-21 %s",
			id, version, anchor, approvedID, approvedAnchor)
	}
	if p := decideProposal(t, admin, proposalID); p.status != "approved" {
		t.Errorf("proposal status %s after the boot seed; want approved", p.status)
	}
	var queued int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM scf_remap_proposals WHERE tenant_id = $1`, tenant).Scan(&queued); err != nil {
		t.Fatalf("count proposals: %v", err)
	}
	if queued != 1 {
		t.Errorf("tenant has %d proposals after the boot; want the one decided proposal", queued)
	}
}
