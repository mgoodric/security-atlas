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
