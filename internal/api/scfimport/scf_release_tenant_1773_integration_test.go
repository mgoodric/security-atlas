//go:build integration

package scfimport_test

// Issue #1773 review: adding scf_release to a stored manifest that declared
// none updates in place only for a shipped stock manifest. A tenant bundle
// that adds one, and a remap approval (#1776) that rewrites one, supersede
// onto the anchor the declared release names, as before #1773. The fixtures
// are testdata/chg-release-{a,b}.json: CHG-02 keeps its code in B and takes
// a new title.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

const tenantCHG02 = "bundle_schema_version: \"1\"\nbundle_id: tenant_chg_1773\ntitle: \"Tenant change control\"\nscf_anchor_id: CHG-02\nimplementation_type: automated\n"

// chgReleases imports chg-release-a then chg-release-b, so B is current and
// A legacy, and returns both labels.
func chgReleases(t *testing.T, admin *pgxpool.Pool) (string, string) {
	t.Helper()
	a, err := scfimport.Load("testdata/chg-release-a.json")
	if err != nil {
		t.Fatal(err)
	}
	b, err := scfimport.Load("testdata/chg-release-b.json")
	if err != nil {
		t.Fatal(err)
	}
	importRelease(t, admin, a)
	importRelease(t, admin, b)
	return a.ReleaseVersion, b.ReleaseVersion
}

// seedUnpinned leaves a control as an install before #1774 stored it from an
// unpinned manifest (an scf_anchor_id code, no scf_release): bound to the
// code's anchor in the release current now, with manifest stored as given.
// Upload refuses that manifest now, so it is uploaded declaring the current
// release and its stored bytes and hash rewritten to manifest.
func seedUnpinned(t *testing.T, admin, app *pgxpool.Pool, tenant, manifest, by string) control.UploadResult {
	t.Helper()
	ctx := context.Background()
	var current string
	if err := admin.QueryRow(ctx, `
		SELECT fv.version FROM framework_versions fv JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = 'scf' AND f.tenant_id IS NULL AND fv.status = 'current'`).Scan(&current); err != nil {
		t.Fatalf("current SCF release: %v", err)
	}
	pinned, err := control.FinalizeBundleForHTTP([]byte(manifest + "scf_release: " + current + "\n"))
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	res, err := control.NewStore(app).Upload(withTenant(t, tenant), pinned, by)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	unpinned, err := control.FinalizeBundleForHTTP([]byte(manifest))
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	if _, err := admin.Exec(ctx,
		`UPDATE controls SET bundle_manifest_yaml = $2, bundle_manifest_hash = $3 WHERE id = $1`,
		res.ControlID, manifest, unpinned.ManifestHashHex); err != nil {
		t.Fatalf("store the unpinned manifest: %v", err)
	}
	return res
}

func uploadManifest(t *testing.T, app *pgxpool.Pool, tenant, manifest string) control.UploadResult {
	t.Helper()
	b, err := control.FinalizeBundleForHTTP([]byte(manifest))
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	res, err := control.NewStore(app).Upload(withTenant(t, tenant), b, "test")
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	return res
}

// A renamed successor that keeps its code: the approval rewrites the
// manifest to scf_anchor_id CHG-02, scf_release B, which differs from the
// stored, unpinned manifest only by the added scf_release. It must still
// supersede onto B's CHG-02.
func TestRemapApprove_SameCodeOnUnpinnedTenantControlSupersedes(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	truncateCatalog(t, admin)
	ctx := context.Background()

	a, err := scfimport.Load("testdata/chg-release-a.json")
	if err != nil {
		t.Fatal(err)
	}
	importRelease(t, admin, a)
	tenant := dbtest.SeedTenant(t, admin, "scf_remap_decisions", "controls")
	legacy := seedUnpinned(t, admin, app, tenant, tenantCHG02, "test").ControlID

	b, err := scfimport.Load("testdata/chg-release-b.json")
	if err != nil {
		t.Fatal(err)
	}
	importRelease(t, admin, b)
	if _, err := scfimport.RebindControls(ctx, admin); err != nil {
		t.Fatalf("RebindControls: %v", err)
	}
	var proposalID string
	if err := admin.QueryRow(ctx, `SELECT id::text FROM scf_remap_proposals WHERE control_id = $1`, legacy).Scan(&proposalID); err != nil {
		t.Fatalf("read proposal: %v", err)
	}

	d, err := control.NewStore(app).ApproveRemap(withTenant(t, tenant), control.RemapApproveInput{
		ProposalID: proposalID, SCFID: "CHG-02", DecidedBy: uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("approve same-code proposal: %v", err)
	}
	id, version, anchor := activeControl(t, admin, tenant, legacy)
	want := anchorID(t, admin, b.ReleaseVersion, "CHG-02")
	if id.String() != d.NewControlID || id == legacy || version != 2 || anchor != want {
		t.Fatalf("after approval the control is %s v%d on %s; want a new version %s v2 on %s@%s %s",
			id, version, anchor, d.NewControlID, "CHG-02", b.ReleaseVersion, want)
	}
}

// A tenant that adds scf_release naming an older release supersedes onto
// that release's anchor, so the stored manifest and the bound anchor agree.
func TestUpload_TenantDeclaringOlderReleaseSupersedesOntoIt(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	truncateCatalog(t, admin)
	relA, relB := chgReleases(t, admin)
	tenant := dbtest.SeedTenant(t, admin, "scf_remap_decisions", "controls")

	prior := seedUnpinned(t, admin, app, tenant, tenantCHG02, "test")
	if want := anchorID(t, admin, relB, "CHG-02"); prior.AnchorID != want {
		t.Fatalf("unpinned control bound %s; want current %s", prior.AnchorID, want)
	}
	next := uploadManifest(t, app, tenant, tenantCHG02+"scf_release: "+relA+"\n")
	want := anchorID(t, admin, relA, "CHG-02")
	if next.UpdatedInPlace || next.SupersededID != prior.ControlID || next.AnchorID != want || next.SCFRelease != relA {
		t.Fatalf("declaring %s: got %+v; want a supersede of %s onto %s", relA, next, prior.ControlID, want)
	}
	if _, _, anchor := activeControl(t, admin, tenant, prior.ControlID); anchor != want {
		t.Fatalf("active control on %s; want CHG-02@%s %s", anchor, relA, want)
	}
}
