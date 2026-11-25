//go:build integration

// Issue #1971: a new SCF release mints new anchor rows under the same codes
// (#1768 freezes each release). Coverage joins controls to edges by anchor
// row, so a control left on the previous release's row drops out of
// coverage. Matt's call (D3): an unchanged control moves to the current
// release's anchor only when that anchor has the same code AND the same
// title; otherwise it stays on its legacy anchor and is flagged.
//
// #1774: an unchanged bundle that names its code with no scf_release is no
// longer re-bound by re-uploading it, because the upload itself is refused.
// A control an install stored that way before #1774 keeps its anchor until
// the boot-time re-bind (scfimport.RebindControls, covered in
// scf_release_rebind_integration_test.go) moves or flags it.

package control_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

func TestUpload_UnchangedUnpinnedBundleIsRefusedAndLeftInPlace(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	ctx := context.Background()

	const same, changed = "TST-1971", "TST-1972"
	oldSame, _ := seedSCFAnchor(t, admin, same, "TST")
	oldChanged, _ := seedSCFAnchor(t, admin, changed, "TST")
	var oldVersion uuid.UUID
	var oldRelease string
	if err := admin.QueryRow(ctx, `
		SELECT a.framework_version_id, fv.version FROM scf_anchors a
		JOIN framework_versions fv ON fv.id = a.framework_version_id
		WHERE a.id = $1`, oldSame).Scan(&oldVersion, &oldRelease); err != nil {
		t.Fatalf("read anchor version: %v", err)
	}

	// Registered before the tenant so it runs after the tenant's controls
	// are gone (t.Cleanup is LIFO): drop the simulated release and make the
	// original version current again.
	newVersion := uuid.New()
	t.Cleanup(func() {
		if _, err := admin.Exec(ctx, `DELETE FROM framework_versions WHERE id = $1`, newVersion); err != nil {
			t.Errorf("cleanup new release: %v", err)
		}
		if _, err := admin.Exec(ctx, `UPDATE framework_versions SET status = 'current' WHERE id = $1`, oldVersion); err != nil {
			t.Errorf("cleanup restore current: %v", err)
		}
	})
	tenant := freshTenant(t, admin)
	store := control.NewStore(app)

	// Controls an install before #1774 stored unpinned on the old release.
	sameID := storedUnpinned(t, admin, store, tenant, "rebind_1971_same", same, oldRelease)
	changedID := storedUnpinned(t, admin, store, tenant, "rebind_1971_changed", changed, oldRelease)

	// A new SCF release: the old version goes legacy; the new current
	// version carries both codes, one with the same title (seedSCFAnchor's
	// "Test anchor <code>") and one whose title changed.
	var frameworkID uuid.UUID
	if err := admin.QueryRow(ctx, `SELECT framework_id FROM framework_versions WHERE id = $1`, oldVersion).Scan(&frameworkID); err != nil {
		t.Fatalf("read framework: %v", err)
	}
	if _, err := admin.Exec(ctx, `UPDATE framework_versions SET status = 'legacy' WHERE id = $1`, oldVersion); err != nil {
		t.Fatalf("demote old release: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
		VALUES ($1, NULL, $2, 'test-rebind-1971', 'current')`, newVersion, frameworkID); err != nil {
		t.Fatalf("insert new release: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title)
		VALUES ($1, $3, $4, 'TST', 'Test anchor '||$4), ($2, $3, $5, 'TST', 'A different control')`,
		uuid.New(), uuid.New(), newVersion, same, changed); err != nil {
		t.Fatalf("insert new anchors: %v", err)
	}

	// Re-uploading either unchanged bundle is refused, and neither control
	// moves: the code is never bound in the new current release.
	for _, tc := range []struct {
		bundleID, code string
		id, legacy     uuid.UUID
	}{
		{"rebind_1971_same", same, sameID, oldSame},
		{"rebind_1971_changed", changed, changedID, oldChanged},
	} {
		_, err := uploadYAML(t, store, tenant, releaseBundleYAML(tc.bundleID, tc.code, ""))
		if !errors.Is(err, control.ErrSCFReleaseRequired) {
			t.Fatalf("re-upload %s: err = %v; want ErrSCFReleaseRequired", tc.bundleID, err)
		}
		if got := boundAnchor(t, admin, tc.id); got != tc.legacy {
			t.Fatalf("refused re-upload moved %s to %s; want it on its legacy anchor %s", tc.bundleID, got, tc.legacy)
		}
	}

	// Declaring the new release is the fix: the same-title control then
	// binds there (a new version, since scf_release changed).
	res, err := uploadYAML(t, store, tenant, releaseBundleYAML("rebind_1971_same", same, "test-rebind-1971"))
	if err != nil {
		t.Fatalf("re-upload declaring the new release: %v", err)
	}
	var newSame uuid.UUID
	if err := admin.QueryRow(ctx, `SELECT id FROM scf_anchors WHERE framework_version_id = $1 AND scf_id = $2`,
		newVersion, same).Scan(&newSame); err != nil {
		t.Fatalf("new anchor: %v", err)
	}
	if got := boundAnchor(t, admin, res.ControlID); got != newSame || res.SCFRelease != "test-rebind-1971" {
		t.Fatalf("declared re-upload bound %s in %q; want the new release's anchor %s", got, res.SCFRelease, newSame)
	}
}
