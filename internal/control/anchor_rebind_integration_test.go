//go:build integration

// Issue #1971: a new SCF release mints new anchor rows under the same codes
// (#1768 freezes each release). Coverage joins controls to edges by anchor
// row, so a control left on the previous release's row drops out of
// coverage. The bootstrap re-uploads every stock bundle on each boot; an
// unchanged bundle must then move to the current release's anchor in place,
// keeping its control id and version.

package control_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func TestUpload_UnchangedBundleRebindsToNewReleaseAnchor(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	ctx := context.Background()

	const code = "TST-1971"
	oldAnchor, _ := seedSCFAnchor(t, admin, code, "TST")
	var oldVersion uuid.UUID
	if err := admin.QueryRow(ctx, `SELECT framework_version_id FROM scf_anchors WHERE id = $1`, oldAnchor).Scan(&oldVersion); err != nil {
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
	upload := func() control.UploadResult {
		t.Helper()
		b, err := control.FinalizeBundleForHTTP([]byte(yamlFor("rebind_1971", code, "automated")))
		if err != nil {
			t.Fatalf("FinalizeBundleForHTTP: %v", err)
		}
		res, err := store.Upload(tctx, b, "key_admin")
		if err != nil {
			t.Fatalf("Upload: %v", err)
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

	first := upload()
	if boundTo(first.ControlID) != oldAnchor {
		t.Fatal("initial upload is not bound to the current release's anchor")
	}

	// A new SCF release: the old version goes legacy, a new current version
	// carries a new anchor row under the same code (what scfimport does for
	// a new release_version).
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
	newAnchor := uuid.New()
	if _, err := admin.Exec(ctx, `
		INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title)
		VALUES ($1, $2, $3, 'TST', 'Test anchor TST-1971, new release')`, newAnchor, newVersion, code); err != nil {
		t.Fatalf("insert new anchor: %v", err)
	}

	// The boot-time re-upload of the unchanged bundle.
	second := upload()
	if second.ControlID != first.ControlID || second.Version != first.Version {
		t.Fatalf("re-upload changed the control: id %s v%d -> id %s v%d; want it kept in place",
			first.ControlID, first.Version, second.ControlID, second.Version)
	}
	if !second.AnchorRebound {
		t.Fatal("re-upload did not report the anchor rebind")
	}
	if got := boundTo(first.ControlID); got != newAnchor {
		t.Fatalf("control bound to %s after re-upload; want the new release's anchor %s", got, newAnchor)
	}

	// Idempotent: the next boot finds it already on the current anchor.
	if third := upload(); third.AnchorRebound || third.ControlID != first.ControlID {
		t.Fatalf("third upload = %+v; want a no-op on the same control", third)
	}
}
