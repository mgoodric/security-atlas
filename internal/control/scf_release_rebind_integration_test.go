//go:build integration

// #1971 D3 meets #1770: after a new SCF release, the upgrade re-binds a
// control on the legacy release only when the current release has the same
// code with the same title. A bundle that declares scf_release is pinned to
// that release on purpose and is never moved, by the boot-time re-bind or
// by a re-upload.

package control_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/scfrelease/scfreleasetest"
)

func TestRebind_PinnedLegacyBundleIsNeverMoved(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	scfreleasetest.LoadAB(t, admin) // A legacy, B current
	tenant := freshTenant(t, admin)
	store := control.NewStore(app)
	ctx := context.Background()

	// IAC-06 has the same title in A and B; GOV-01's title changed.
	mfaA := anchorIDIn(t, admin, scfreleasetest.ReleaseA, "IAC-06")
	mfaB := anchorIDIn(t, admin, scfreleasetest.ReleaseB, "IAC-06")
	govA := anchorIDIn(t, admin, scfreleasetest.ReleaseA, "GOV-01")

	pinnedYAML := releaseBundleYAML("pinned_a_mfa", "IAC-06", scfreleasetest.ReleaseA)
	pinned, err := uploadYAML(t, store, tenant, pinnedYAML)
	if err != nil {
		t.Fatalf("pinned upload: %v", err)
	}
	if boundAnchor(t, admin, pinned.ControlID) != mfaA {
		t.Fatal("pinned bundle is not bound to release A's IAC-06")
	}

	// Two unpinned controls left on release A by an older install: one whose
	// code kept its title in B, one whose title changed.
	onLegacy := func(bundleID, code string, legacyAnchor uuid.UUID) uuid.UUID {
		t.Helper()
		res, err := uploadYAML(t, store, tenant, releaseBundleYAML(bundleID, code, ""))
		if err != nil {
			t.Fatalf("upload %s: %v", bundleID, err)
		}
		if _, err := admin.Exec(ctx, `UPDATE controls SET scf_anchor_id = $2 WHERE id = $1`, res.ControlID, legacyAnchor); err != nil {
			t.Fatalf("bind %s to release A: %v", bundleID, err)
		}
		return res.ControlID
	}
	sameTitle := onLegacy("legacy_mfa", "IAC-06", mfaA)
	changedTitle := onLegacy("legacy_gov", "GOV-01", govA)

	report, err := scfimport.RebindControls(ctx, admin)
	if err != nil {
		t.Fatalf("RebindControls: %v", err)
	}
	in := func(list []scfimport.ControlOnLegacyAnchor, id uuid.UUID) bool {
		for _, c := range list {
			if c.ControlID == id {
				return true
			}
		}
		return false
	}
	if !in(report.Pinned, pinned.ControlID) || in(report.Rebound, pinned.ControlID) || in(report.NeedsReanchoring, pinned.ControlID) {
		t.Fatalf("pinned control: rebound=%v flagged=%v pinned=%v; want only pinned",
			in(report.Rebound, pinned.ControlID), in(report.NeedsReanchoring, pinned.ControlID), in(report.Pinned, pinned.ControlID))
	}
	if got := boundAnchor(t, admin, pinned.ControlID); got != mfaA {
		t.Fatalf("pinned control moved to %s; want it kept on release A's IAC-06 %s", got, mfaA)
	}
	if !in(report.Rebound, sameTitle) || boundAnchor(t, admin, sameTitle) != mfaB {
		t.Fatal("unpinned same-title control was not re-bound to release B's IAC-06")
	}
	if !in(report.NeedsReanchoring, changedTitle) || boundAnchor(t, admin, changedTitle) != govA {
		t.Fatal("unpinned changed-title control was not flagged and left on release A's GOV-01")
	}

	// A re-upload of the unchanged pinned bundle leaves it where it is too.
	again, err := uploadYAML(t, store, tenant, pinnedYAML)
	if err != nil {
		t.Fatalf("pinned re-upload: %v", err)
	}
	if again.AnchorRebound || again.NeedsReanchoring || boundAnchor(t, admin, pinned.ControlID) != mfaA {
		t.Fatalf("pinned re-upload = %+v; want it left on release A's IAC-06, not rebound or flagged", again)
	}
}

// A bundle whose scf_anchor_id is an anchor UUID is pinned to that row by
// scfrelease.Resolve, with no scf_release. Neither re-bind may move it: if
// the boot moved it to B, the next unchanged re-upload would resolve the
// UUID back to A and move it again, flipping on every boot (#1999 review
// B1). A control whose stored control.yaml no longer parses is not moved
// either; it is flagged for re-anchoring (N1).
func TestRebind_UUIDRefAndUnreadableManifestAreNeverMoved(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	scfreleasetest.LoadAB(t, admin) // A legacy, B current
	tenant := freshTenant(t, admin)
	store := control.NewStore(app)
	ctx := context.Background()

	mfaA := anchorIDIn(t, admin, scfreleasetest.ReleaseA, "IAC-06")
	uuidYAML := releaseBundleYAML("uuid_ref_mfa", mfaA.String(), "")
	byID, err := uploadYAML(t, store, tenant, uuidYAML)
	if err != nil {
		t.Fatalf("uuid-ref upload: %v", err)
	}
	if boundAnchor(t, admin, byID.ControlID) != mfaA {
		t.Fatal("uuid-ref bundle is not bound to the anchor row it names")
	}

	// An unpinned same-title control on A whose stored manifest is corrupt.
	broken, err := uploadYAML(t, store, tenant, releaseBundleYAML("broken_manifest", "IAC-06", ""))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if _, err := admin.Exec(ctx, `UPDATE controls SET scf_anchor_id = $2, bundle_manifest_yaml = 'not: [valid' WHERE id = $1`,
		broken.ControlID, mfaA); err != nil {
		t.Fatalf("corrupt stored manifest: %v", err)
	}

	report, err := scfimport.RebindControls(ctx, admin)
	if err != nil {
		t.Fatalf("RebindControls: %v", err)
	}
	find := func(list []scfimport.ControlOnLegacyAnchor, id uuid.UUID) (scfimport.ControlOnLegacyAnchor, bool) {
		for _, c := range list {
			if c.ControlID == id {
				return c, true
			}
		}
		return scfimport.ControlOnLegacyAnchor{}, false
	}
	if _, ok := find(report.Pinned, byID.ControlID); !ok {
		t.Fatalf("uuid-ref control not reported as pinned: %+v", report)
	}
	if _, moved := find(report.Rebound, byID.ControlID); moved || boundAnchor(t, admin, byID.ControlID) != mfaA {
		t.Fatal("boot re-bind moved the uuid-ref control off the anchor row it names")
	}
	if f, ok := find(report.NeedsReanchoring, broken.ControlID); !ok || f.Note == "" {
		t.Fatalf("control with an unreadable manifest not flagged with a note: %+v", f)
	}
	if boundAnchor(t, admin, broken.ControlID) != mfaA {
		t.Fatal("boot re-bind moved a control whose manifest it could not read")
	}

	// The unchanged uuid-ref bundle's re-upload leaves it in place too, so
	// nothing flips on the next boot.
	again, err := uploadYAML(t, store, tenant, uuidYAML)
	if err != nil {
		t.Fatalf("uuid-ref re-upload: %v", err)
	}
	if again.AnchorRebound || again.NeedsReanchoring || boundAnchor(t, admin, byID.ControlID) != mfaA {
		t.Fatalf("uuid-ref re-upload = %+v; want it left on release A's IAC-06", again)
	}
	second, err := scfimport.RebindControls(ctx, admin)
	if err != nil {
		t.Fatalf("second RebindControls: %v", err)
	}
	if _, moved := find(second.Rebound, byID.ControlID); moved {
		t.Fatal("second boot moved the uuid-ref control")
	}
}
