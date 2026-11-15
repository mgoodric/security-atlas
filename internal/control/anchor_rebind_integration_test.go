//go:build integration

// Issue #1971: a new SCF release mints new anchor rows under the same codes
// (#1768 freezes each release). Coverage joins controls to edges by anchor
// row, so a control left on the previous release's row drops out of
// coverage. The bootstrap re-uploads every stock bundle on each boot. Matt's
// call (D3): an unchanged bundle moves to the current release's anchor in
// place only when that anchor has the same code AND the same title; when the
// title changed (the code now means something else) the control stays on its
// legacy anchor and is flagged for re-anchoring.

package control_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func TestUpload_UnchangedBundleRebindsOnlyWhenTheAnchorMeansTheSame(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	ctx := context.Background()

	const same, changed = "TST-1971", "TST-1972"
	oldSame, _ := seedSCFAnchor(t, admin, same, "TST")
	oldChanged, _ := seedSCFAnchor(t, admin, changed, "TST")
	var oldVersion uuid.UUID
	if err := admin.QueryRow(ctx, `SELECT framework_version_id FROM scf_anchors WHERE id = $1`, oldSame).Scan(&oldVersion); err != nil {
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
	tctx, err := tenancy.WithTenant(ctx, tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	upload := func(bundleID, code string) control.UploadResult {
		t.Helper()
		b, err := control.FinalizeBundleForHTTP([]byte(yamlFor(bundleID, code, "automated")))
		if err != nil {
			t.Fatalf("FinalizeBundleForHTTP: %v", err)
		}
		res, err := store.Upload(tctx, b, "key_admin")
		if err != nil {
			t.Fatalf("Upload %s: %v", bundleID, err)
		}
		return res
	}
	boundTo := func(id uuid.UUID) uuid.UUID {
		t.Helper()
		var a uuid.UUID
		if err := admin.QueryRow(ctx, `SELECT scf_anchor_id FROM controls WHERE id = $1`, id).Scan(&a); err != nil {
			t.Fatalf("read control anchor: %v", err)
		}
		return a
	}

	firstSame := upload("rebind_1971_same", same)
	firstChanged := upload("rebind_1971_changed", changed)
	if boundTo(firstSame.ControlID) != oldSame || boundTo(firstChanged.ControlID) != oldChanged {
		t.Fatal("initial uploads are not bound to the current release's anchors")
	}

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
	newSame, newChanged := uuid.New(), uuid.New()
	if _, err := admin.Exec(ctx, `
		INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title)
		VALUES ($1, $3, $4, 'TST', 'Test anchor '||$4), ($2, $3, $5, 'TST', 'A different control')`,
		newSame, newChanged, newVersion, same, changed); err != nil {
		t.Fatalf("insert new anchors: %v", err)
	}

	// The boot-time re-upload of the unchanged bundles.
	second := upload("rebind_1971_same", same)
	if second.ControlID != firstSame.ControlID || second.Version != firstSame.Version || !second.AnchorRebound || second.NeedsReanchoring {
		t.Fatalf("same-meaning re-upload = %+v; want the control kept and rebound in place", second)
	}
	if got := boundTo(firstSame.ControlID); got != newSame {
		t.Fatalf("same-meaning control bound to %s; want the new release's anchor %s", got, newSame)
	}
	flagged := upload("rebind_1971_changed", changed)
	if flagged.ControlID != firstChanged.ControlID || flagged.AnchorRebound || !flagged.NeedsReanchoring {
		t.Fatalf("changed-meaning re-upload = %+v; want the control kept, not rebound, and flagged", flagged)
	}
	if got := boundTo(firstChanged.ControlID); got != oldChanged {
		t.Fatalf("changed-meaning control moved to %s; want it left on its legacy anchor %s", got, oldChanged)
	}

	// Idempotent: the next boot finds the first already on the current
	// anchor and flags the second again.
	if third := upload("rebind_1971_same", same); third.AnchorRebound || third.NeedsReanchoring {
		t.Fatalf("third same-meaning upload = %+v; want a no-op", third)
	}
	if again := upload("rebind_1971_changed", changed); !again.NeedsReanchoring || boundTo(again.ControlID) != oldChanged {
		t.Fatalf("second changed-meaning upload = %+v; want it still flagged on its legacy anchor", again)
	}
}
