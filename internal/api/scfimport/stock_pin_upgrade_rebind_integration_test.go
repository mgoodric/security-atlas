//go:build integration

// #1774: after the fallback went, the one path that still re-binds a control
// on upload is the in-place stock upgrade. A stock control an older install
// stored unpinned (the pre-#1773 bytes) on an earlier release's anchor; the
// boot then uploads the shipped bytes, which differ only by the scf_release
// line, so the control is updated in place. Its stored manifest declared no
// release, so the upload re-binds or flags it (#1971 D3) and records that as
// SourceUpload. TestUpgradeFromUnpinnedStock_UpdatesInPlace covers the same
// upgrade when the anchor row does not change.

package scfimport_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

func TestUpload_ShippedStockPinUpgradeRebindsInPlaceAndRecordsUpload(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	truncateCatalog(t, admin)
	ctx := context.Background()

	a, err := scfimport.Load("testdata/chg-release-a.json")
	if err != nil {
		t.Fatal(err)
	}
	importRelease(t, admin, a)
	tenant := dbtest.SeedTenant(t, admin, "scf_remap_decisions", "stock_control_holdbacks", "controls")

	shipped := stockManifestShipped(t, stockCC81Bundle)
	prePin := bytes.Replace(shipped, []byte("scf_release: "+shippedStockRelease+"\n"), nil, 1)
	if !scfimport.IsStockManifest(stockCC81Bundle, string(prePin)) {
		t.Fatalf("pre-#1773 %s is not on the stock list", stockCC81Bundle)
	}
	// The older install: the stock control on release A's CHG-02, stored
	// with the unpinned pre-#1773 bytes.
	stored := seedUnpinned(t, admin, app, tenant, string(prePin), "oauth_client:bootstrap")
	legacyAnchor := anchorID(t, admin, a.ReleaseVersion, "CHG-02")
	if stored.AnchorID != legacyAnchor {
		t.Fatalf("stored stock control on %s; want release A's CHG-02 %s", stored.AnchorID, legacyAnchor)
	}

	// The upgrade: a newer release, current, under the label the shipped
	// bytes pin.
	b, err := scfimport.Load("testdata/chg-release-b.json")
	if err != nil {
		t.Fatal(err)
	}
	importAsShippedRelease(t, admin, b)

	next, err := control.FinalizeBundleForHTTP(shipped)
	if err != nil {
		t.Fatalf("shipped bundle: %v", err)
	}
	res, err := control.NewStore(app).Upload(withTenant(t, tenant), next, "oauth_client:bootstrap")
	if err != nil {
		t.Fatalf("upgrade upload: %v", err)
	}
	if !res.UpdatedInPlace || res.ControlID != stored.ControlID {
		t.Fatalf("upgrade upload = %+v; want the stored control %s updated in place", res, stored.ControlID)
	}
	if !res.AnchorRebound && !res.NeedsReanchoring {
		t.Fatalf("upgrade upload = %+v; want the control re-bound or flagged (#1971 D3)", res)
	}
	var source string
	if err := admin.QueryRow(ctx, `SELECT source FROM scf_remap_proposals WHERE control_id = $1`, stored.ControlID).Scan(&source); err != nil {
		t.Fatalf("read the re-bind record: %v", err)
	}
	if source != scfimport.SourceUpload {
		t.Fatalf("re-bind record source = %q; want %q", source, scfimport.SourceUpload)
	}
}
