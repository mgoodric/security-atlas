//go:build integration

// #2236: every SCF re-bind leaves a durable record in scf_remap_proposals,
// written in the re-bind's transaction, whichever path moved the control:
// the boot-time RebindControls or an upload's in-place re-bind. A flagged
// control is recorded as an open row with no target anchor; a pinned
// control writes nothing.

package control_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/scfrelease/scfreleasetest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// rebindRecord is one scf_remap_proposals row, as the re-bind wrote it.
type rebindRecord struct {
	tenant        string
	legacyAnchor  uuid.UUID
	legacyRelease string
	targetRelease string
	targetAnchor  *uuid.UUID
	status        string
	source        string
}

func rebindRecords(t *testing.T, admin *pgxpool.Pool, controlID uuid.UUID) []rebindRecord {
	t.Helper()
	rows, err := admin.Query(context.Background(), `
		SELECT tenant_id::text, legacy_anchor_id, legacy_release, target_release,
		       target_anchor_id, status, source
		FROM scf_remap_proposals WHERE control_id = $1`, controlID)
	if err != nil {
		t.Fatalf("read records for %s: %v", controlID, err)
	}
	defer rows.Close()
	var out []rebindRecord
	for rows.Next() {
		var r rebindRecord
		if err := rows.Scan(&r.tenant, &r.legacyAnchor, &r.legacyRelease, &r.targetRelease,
			&r.targetAnchor, &r.status, &r.source); err != nil {
			t.Fatalf("scan record: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("records: %v", err)
	}
	return out
}

// wantMoved asserts exactly one rebound record from source for a control
// moved from legacy (release A) to target (release B).
func wantMoved(t *testing.T, admin *pgxpool.Pool, name, tenant string, controlID, legacy, target uuid.UUID, source string) {
	t.Helper()
	recs := rebindRecords(t, admin, controlID)
	if len(recs) != 1 {
		t.Fatalf("%s: %d records, want exactly 1: %+v", name, len(recs), recs)
	}
	r := recs[0]
	if r.status != scfimport.ProposalRebound || r.source != source || r.tenant != tenant ||
		r.legacyAnchor != legacy || r.targetAnchor == nil || *r.targetAnchor != target ||
		r.legacyRelease != scfreleasetest.ReleaseA || r.targetRelease != scfreleasetest.ReleaseB {
		t.Fatalf("%s: record = %+v (target %v); want rebound by %s, %s %s -> %s %s",
			name, r, r.targetAnchor, source, scfreleasetest.ReleaseA, legacy, scfreleasetest.ReleaseB, target)
	}
}

// wantFlagged asserts exactly one open record from source, naming no
// target anchor: the control was flagged, not moved.
func wantFlagged(t *testing.T, admin *pgxpool.Pool, name string, controlID, legacy uuid.UUID, source string) {
	t.Helper()
	recs := rebindRecords(t, admin, controlID)
	if len(recs) != 1 {
		t.Fatalf("%s: %d records, want exactly 1: %+v", name, len(recs), recs)
	}
	r := recs[0]
	if r.status != scfimport.ProposalOpen || r.source != source || r.targetAnchor != nil || r.legacyAnchor != legacy {
		t.Fatalf("%s: record = %+v (target %v); want open by %s with no target, legacy %s", name, r, r.targetAnchor, source, legacy)
	}
}

func TestRebindRecord_BootWritesOneRecordPerMovedControlAndNoneForPinned(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	scfreleasetest.LoadAB(t, admin) // A legacy, B current
	tenant := freshTenant(t, admin)
	store := control.NewStore(app)
	ctx := context.Background()

	mfaA := anchorIDIn(t, admin, scfreleasetest.ReleaseA, "IAC-06")
	mfaB := anchorIDIn(t, admin, scfreleasetest.ReleaseB, "IAC-06")
	govA := anchorIDIn(t, admin, scfreleasetest.ReleaseA, "GOV-01")

	pinned, err := uploadYAML(t, store, tenant, releaseBundleYAML("rec_pinned", "IAC-06", scfreleasetest.ReleaseA))
	if err != nil {
		t.Fatalf("pinned upload: %v", err)
	}
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
	moved := onLegacy("rec_boot_mfa", "IAC-06", mfaA)
	flagged := onLegacy("rec_boot_gov", "GOV-01", govA)

	if _, err := scfimport.RebindControls(ctx, admin); err != nil {
		t.Fatalf("RebindControls: %v", err)
	}
	wantMoved(t, admin, "moved control", tenant, moved, mfaA, mfaB, scfimport.SourceBoot)
	wantFlagged(t, admin, "flagged control", flagged, govA, scfimport.SourceBoot)
	if recs := rebindRecords(t, admin, pinned.ControlID); len(recs) != 0 {
		t.Fatalf("pinned control: %d records, want none: %+v", len(recs), recs)
	}

	// A second boot finds nothing new to move and writes nothing more.
	if _, err := scfimport.RebindControls(ctx, admin); err != nil {
		t.Fatalf("second RebindControls: %v", err)
	}
	wantMoved(t, admin, "moved control after a second boot", tenant, moved, mfaA, mfaB, scfimport.SourceBoot)
	wantFlagged(t, admin, "flagged control after a second boot", flagged, govA, scfimport.SourceBoot)
}

func TestRebindRecord_UploadInPlaceRebindAndFlagAreRecorded(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	scfreleasetest.LoadAB(t, admin) // A legacy, B current
	tenant := freshTenant(t, admin)
	store := control.NewStore(app)
	ctx := context.Background()

	mfaA := anchorIDIn(t, admin, scfreleasetest.ReleaseA, "IAC-06")
	mfaB := anchorIDIn(t, admin, scfreleasetest.ReleaseB, "IAC-06")
	govA := anchorIDIn(t, admin, scfreleasetest.ReleaseA, "GOV-01")

	// Each control is left on release A by an older install, then its
	// unchanged bundle is re-uploaded: the upload path's in-place re-bind.
	reupload := func(bundleID, code string, legacyAnchor uuid.UUID) control.UploadResult {
		t.Helper()
		y := releaseBundleYAML(bundleID, code, "")
		res, err := uploadYAML(t, store, tenant, y)
		if err != nil {
			t.Fatalf("upload %s: %v", bundleID, err)
		}
		if _, err := admin.Exec(ctx, `UPDATE controls SET scf_anchor_id = $2 WHERE id = $1`, res.ControlID, legacyAnchor); err != nil {
			t.Fatalf("bind %s to release A: %v", bundleID, err)
		}
		again, err := uploadYAML(t, store, tenant, y)
		if err != nil {
			t.Fatalf("re-upload %s: %v", bundleID, err)
		}
		if again.ControlID != res.ControlID {
			t.Fatalf("re-upload %s superseded the control; want it kept in place", bundleID)
		}
		return again
	}
	moved := reupload("rec_upload_mfa", "IAC-06", mfaA)
	if !moved.AnchorRebound || boundAnchor(t, admin, moved.ControlID) != mfaB {
		t.Fatalf("same-title re-upload = %+v; want it re-bound to release B's IAC-06", moved)
	}
	wantMoved(t, admin, "upload-moved control", tenant, moved.ControlID, mfaA, mfaB, scfimport.SourceUpload)

	flagged := reupload("rec_upload_gov", "GOV-01", govA)
	if !flagged.NeedsReanchoring || boundAnchor(t, admin, flagged.ControlID) != govA {
		t.Fatalf("changed-title re-upload = %+v; want it flagged and left on release A", flagged)
	}
	wantFlagged(t, admin, "upload-flagged control", flagged.ControlID, govA, scfimport.SourceUpload)

	// Uploading the flagged bundle again records nothing more, and a boot
	// after the uploads does not record the same move a second time.
	if _, err := uploadYAML(t, store, tenant, releaseBundleYAML("rec_upload_gov", "GOV-01", "")); err != nil {
		t.Fatalf("third upload: %v", err)
	}
	if _, err := scfimport.RebindControls(ctx, admin); err != nil {
		t.Fatalf("RebindControls: %v", err)
	}
	wantMoved(t, admin, "upload-moved control after boot", tenant, moved.ControlID, mfaA, mfaB, scfimport.SourceUpload)
	wantFlagged(t, admin, "upload-flagged control after boot", flagged.ControlID, govA, scfimport.SourceUpload)
}

// insertRecordAsApp inserts a re-bind record through the app role, scoped to
// tenant, and returns the database error, if any.
func insertRecordAsApp(t *testing.T, app *pgxpool.Pool, tenant, rowTenant string, controlID, legacyAnchor, target uuid.UUID) error {
	t.Helper()
	ctx := dbtest.WithTenantCtx(t, tenant)
	tx, err := app.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		t.Fatalf("ApplyTenant: %v", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO scf_remap_proposals (tenant_id, control_id, bundle_id, legacy_anchor_id,
			legacy_scf_id, legacy_release, target_framework_version_id, target_release,
			class, status, successor_scf_ids, source)
		VALUES ($1, $2, 'forged', $3, 'IAC-06', $4,
			(SELECT framework_version_id FROM scf_anchors WHERE id = $5), $6,
			'no_successor', 'open', '{}', 'upload')`,
		rowTenant, controlID, legacyAnchor, scfreleasetest.ReleaseA, target, scfreleasetest.ReleaseB)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func pgCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

// The app role may write re-bind records only for its own tenant's
// controls: a row naming another tenant, or another tenant's control, is
// refused, so no tenant can take another's (control, release) slot and
// drop its real record.
func TestRebindRecord_AppRoleCannotWriteAnotherTenantsRecord(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	scfreleasetest.LoadAB(t, admin) // A legacy, B current
	tenantT := freshTenant(t, admin)
	tenantU := freshTenant(t, admin)
	store := control.NewStore(app)
	ctx := context.Background()

	mfaA := anchorIDIn(t, admin, scfreleasetest.ReleaseA, "IAC-06")
	mfaB := anchorIDIn(t, admin, scfreleasetest.ReleaseB, "IAC-06")
	res, err := uploadYAML(t, store, tenantU, releaseBundleYAML("rec_victim", "IAC-06", ""))
	if err != nil {
		t.Fatalf("upload in U: %v", err)
	}
	victim := res.ControlID
	if _, err := admin.Exec(ctx, `UPDATE controls SET scf_anchor_id = $2 WHERE id = $1`, victim, mfaA); err != nil {
		t.Fatalf("bind U's control to release A: %v", err)
	}

	// 42501: the RLS WITH CHECK refuses a row for tenant U.
	if err := insertRecordAsApp(t, app, tenantT, tenantU, victim, mfaA, mfaB); pgCode(err) != "42501" {
		t.Fatalf("insert as T with tenant_id=U: err = %v; want an RLS violation (42501)", err)
	}
	// 23503: the composite FK refuses T's row naming U's control.
	if err := insertRecordAsApp(t, app, tenantT, tenantT, victim, mfaA, mfaB); pgCode(err) != "23503" {
		t.Fatalf("insert as T naming U's control: err = %v; want a foreign-key violation (23503)", err)
	}

	// U's own re-bind still writes U's record.
	if _, err := scfimport.RebindControls(ctx, admin); err != nil {
		t.Fatalf("RebindControls: %v", err)
	}
	wantMoved(t, admin, "U's control", tenantU, victim, mfaA, mfaB, scfimport.SourceBoot)
}
