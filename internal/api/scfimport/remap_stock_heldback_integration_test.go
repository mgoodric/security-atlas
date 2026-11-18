//go:build integration

package scfimport_test

// #2358: the boot seed's hold on a reviewed stock control is recorded and
// listed, and it covers a person's edit made after the approval.
//
//   - A stock control whose re-anchor a person approved is held back on the
//     next boot, and ListHeldBackStockControls names it with the approved
//     revision and the shipped manifest it did not apply. Another tenant
//     sees nothing.
//   - A person edits the approved revision (v2 -> v3, a non-stock
//     manifest). The next boot seed keeps v3: v3 descends from an approved
//     revision. Before #2358 only the approved revision itself was kept, and
//     the shipped manifest superseded v3.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func TestHeldBack2358_ReviewedStockControlListedAndEditsKept(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	truncateCatalog(t, admin)
	ctx := context.Background()

	stockYAML, err := os.ReadFile(filepath.Join("..", "..", "..", "controls", "soc2", stockCC81Bundle, "control.yaml"))
	if err != nil {
		t.Fatalf("read stock manifest: %v", err)
	}
	sum := sha256.Sum256(stockYAML)
	stockHash := hex.EncodeToString(sum[:])
	upload := func(tenant string, yaml []byte, by string) control.UploadResult {
		t.Helper()
		b, err := control.FinalizeBundleForHTTP(yaml)
		if err != nil {
			t.Fatalf("bundle: %v", err)
		}
		res, err := control.NewStore(app).Upload(withTenant(t, tenant), b, by)
		if err != nil {
			t.Fatalf("upload: %v", err)
		}
		return res
	}
	bootSeed := func(tenant string) control.UploadResult {
		t.Helper()
		return upload(tenant, stockYAML, "oauth_client:bootstrap")
	}
	heldBack := func(tenant string) []control.HeldBackStockControl {
		t.Helper()
		hs, err := control.NewStore(app).ListHeldBackStockControls(withTenant(t, tenant))
		if err != nil {
			t.Fatalf("ListHeldBackStockControls: %v", err)
		}
		return hs
	}
	load := func(name string) *scfimport.Catalog {
		c, err := scfimport.Load("testdata/remap-stock-release-" + name + ".json")
		if err != nil {
			t.Fatalf("Load %s: %v", name, err)
		}
		return c
	}

	importRelease(t, admin, load("a"))
	tenant := dbtest.SeedTenant(t, admin, "scf_remap_decisions", "stock_control_holdbacks", "controls")
	other := dbtest.SeedTenant(t, admin, "scf_remap_decisions", "stock_control_holdbacks", "controls")
	legacy := bootSeed(tenant).ControlID
	bootSeed(other)
	importRelease(t, admin, load("b"))
	if _, err := scfimport.RebindControls(ctx, admin); err != nil {
		t.Fatalf("RebindControls: %v", err)
	}
	var proposalID string
	if err := admin.QueryRow(ctx, `SELECT id::text FROM scf_remap_proposals WHERE control_id = $1`, legacy).Scan(&proposalID); err != nil {
		t.Fatalf("read proposal: %v", err)
	}
	d, err := control.NewStore(app).ApproveRemap(withTenant(t, tenant), control.RemapApproveInput{
		ProposalID: proposalID, SCFID: "CHG-21", DecidedBy: uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	approvedID := uuid.MustParse(d.NewControlID)

	if hs := heldBack(tenant); len(hs) != 0 {
		t.Fatalf("held back before any boot after the approval: %+v", hs)
	}

	// The next boot holds the shipped manifest back and records it.
	if res := bootSeed(tenant); !res.KeptReviewedAnchor || res.ControlID != approvedID {
		t.Fatalf("boot seed result = %+v; want the approved revision kept", res)
	}
	hs := heldBack(tenant)
	if len(hs) != 1 {
		t.Fatalf("held back = %+v; want one control", hs)
	}
	h := hs[0]
	if h.BundleID != stockCC81Bundle || h.ActiveControlID != approvedID.String() || h.ActiveVersion != 2 ||
		h.ApprovedControlID != approvedID.String() || h.ApprovedVersion != 2 ||
		h.ApprovedSCFID != "CHG-21" || h.ShippedManifestHash != stockHash ||
		h.ReplacedManifestHash != stockHash || h.ShippedUpdate {
		t.Fatalf("held back = %+v; want %s v2 on CHG-21 holding back the shipped %s, which is the manifest it replaced",
			h, approvedID, stockHash)
	}
	if hs := heldBack(other); len(hs) != 0 {
		t.Fatalf("other tenant sees held-back controls %+v; want none", hs)
	}
	// RLS on its own, without the query's tenant filter.
	otherCtx := withTenant(t, other)
	tx, err := app.Begin(otherCtx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(otherCtx) }()
	if err := tenancy.ApplyTenant(otherCtx, tx); err != nil {
		t.Fatalf("ApplyTenant: %v", err)
	}
	var visible int
	if err := tx.QueryRow(otherCtx, `SELECT count(*) FROM stock_control_holdbacks`).Scan(&visible); err != nil {
		t.Fatalf("count holdbacks: %v", err)
	}
	if visible != 0 {
		t.Fatalf("other tenant reads %d stock_control_holdbacks rows under RLS; want 0", visible)
	}
	_ = tx.Rollback(otherCtx)

	// A person edits the approved revision: v3, not stock and not itself
	// an approved revision.
	var approvedYAML string
	if err := admin.QueryRow(ctx, `SELECT bundle_manifest_yaml FROM controls WHERE id = $1`, approvedID).Scan(&approvedYAML); err != nil {
		t.Fatalf("read approved manifest: %v", err)
	}
	edited := strings.Replace(approvedYAML,
		`title: "Production-deploying repositories enforce branch protection"`,
		`title: "Production-deploying repositories enforce branch protection (tenant edit)"`, 1)
	if edited == approvedYAML {
		t.Fatalf("approved manifest has no title line to edit:\n%s", approvedYAML)
	}
	v3 := upload(tenant, []byte(edited), uuid.NewString())
	if v3.Version != 3 || v3.SupersededID != approvedID {
		t.Fatalf("tenant edit = %+v; want v3 superseding %s", v3, approvedID)
	}

	// The next boot keeps v3.
	res := bootSeed(tenant)
	if !res.KeptReviewedAnchor || res.ControlID != v3.ControlID || res.SupersededID != uuid.Nil {
		t.Fatalf("boot seed after the edit = %+v; want the edited v3 %s kept", res, v3.ControlID)
	}
	if id, version, _ := activeControl(t, admin, tenant, legacy); id != v3.ControlID || version != 3 {
		t.Fatalf("after the boot seed the control is %s v%d; want the edited %s v3", id, version, v3.ControlID)
	}
	hs = heldBack(tenant)
	if len(hs) != 1 || hs[0].ActiveControlID != v3.ControlID.String() || hs[0].ActiveVersion != 3 ||
		hs[0].ApprovedControlID != approvedID.String() {
		t.Fatalf("held back after the edit = %+v; want v3 held behind approved %s", hs, approvedID)
	}
}
