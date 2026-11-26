//go:build integration

// #1782 — the concurrent-approval race on a bundle ID, against real Postgres
// with RLS. Two approvals whose organisation IDs slug alike can both pass
// the bundle-ID check before either commits; the second upload then finds
// the first's control. beforeControlUpload commits that control inside the
// window, so the race is deterministic. The rest of approval is covered
// through the API in internal/api/controlimports.
package controlimport

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/scfrelease/scfreleasetest"
)

// seedRaceFixture writes one SCF anchor into the current SCF release (made
// when there is none) and a framework version for the batch to reference,
// and returns the anchor code and the release.
func seedRaceFixture(t *testing.T, admin *pgxpool.Pool) (string, SCFRelease, FrameworkRef) {
	t.Helper()
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := admin.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed: %v\nsql: %s", err, sql)
		}
	}
	exec(`INSERT INTO frameworks (id, tenant_id, slug, name, issuer, description)
	      VALUES ($1, NULL, 'scf', 'Secure Controls Framework', 'SCF Council', '') ON CONFLICT DO NOTHING`, uuid.New())
	var scfFW uuid.UUID
	if err := admin.QueryRow(ctx, `SELECT id FROM frameworks WHERE slug = 'scf' AND tenant_id IS NULL`).Scan(&scfFW); err != nil {
		t.Fatalf("read scf framework: %v", err)
	}
	var rel SCFRelease
	err := admin.QueryRow(ctx, `
		SELECT id::text, version FROM framework_versions
		WHERE framework_id = $1 AND status = 'current' ORDER BY created_at DESC LIMIT 1`, scfFW).Scan(&rel.VersionID, &rel.Version)
	if err != nil {
		exec(`INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
		      VALUES ($1, NULL, $2, 'test-1.0', 'current') ON CONFLICT DO NOTHING`, uuid.New(), scfFW)
		if err := admin.QueryRow(ctx, `
			SELECT id::text, version FROM framework_versions
			WHERE framework_id = $1 AND status = 'current' ORDER BY created_at DESC LIMIT 1`, scfFW).Scan(&rel.VersionID, &rel.Version); err != nil {
			t.Fatalf("read scf release: %v", err)
		}
	}

	suffix := strings.ToUpper(uuid.NewString()[:8])
	anchorID, fwID, fwVer := uuid.New(), uuid.New(), uuid.New()
	code := "RACE" + suffix + "-01"
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM frameworks WHERE id = $1`, fwID)
		_, _ = admin.Exec(context.Background(), `DELETE FROM scf_anchors WHERE id = $1`, anchorID)
	})
	exec(`INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title, description)
	      VALUES ($1, $2, $3, 'IAC', 'Race anchor', 'Race anchor.')`, anchorID, rel.VersionID, code)
	slug := "race-fw-" + strings.ToLower(suffix)
	exec(`INSERT INTO frameworks (id, tenant_id, name, slug, issuer) VALUES ($1, NULL, 'Race', $2, 'test')`, fwID, slug)
	exec(`INSERT INTO framework_versions (id, tenant_id, framework_id, version, status) VALUES ($1, NULL, $2, '1', 'current')`, fwVer, fwID)
	return code, rel, FrameworkRef{Slug: slug, VersionID: fwVer.String(), Version: "1"}
}

// #1774 D7: staging records the release that is current at staging time on
// the batch (a deliberate exclusion, Matt 2026-11-24). Approval binds in that
// recorded release, never in whatever is current at approval. Fixture
// releases A and B each carry their own IAC-06 row; the batch is staged
// while B is current, then A is made current, and the approved control must
// still be on B's IAC-06.
func TestApproveRow_BindsInTheStagedReleaseAfterTheCurrentReleaseChanges(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	// The framework fixture first: it makes an SCF release current when
	// none is. LoadAB then leaves only B current (A legacy).
	_, _, fw := seedRaceFixture(t, admin)
	scfreleasetest.LoadAB(t, admin)
	tenant := dbtest.SeedTenant(t, admin,
		"control_import_candidates", "control_import_rejected_rows", "control_import_rows",
		"control_import_batches", "controls")
	ctx := dbtest.WithTenantCtx(t, tenant)
	store := NewStore(app)

	staged, ok, err := store.ResolveSCFRelease(ctx)
	if err != nil || !ok || staged.Version != scfreleasetest.ReleaseB {
		t.Fatalf("staging release = %+v ok=%v err=%v; want the current release %s", staged, ok, err, scfreleasetest.ReleaseB)
	}
	b, err := store.CreateBatch(ctx, Batch{
		Framework: fw, SourceFilename: "release.csv", SourceFormat: "csv", Status: BatchStatusStaged,
		TotalRows: 1, StagedCount: 1, SCFRelease: &staged, SuggestionsStatus: SuggestionsReady,
		Staged: []BatchControl{{RowNumber: 2, ExternalID: "MFA 01", Title: "MFA", Description: "d", Status: RowStatusPending}},
	})
	if err != nil {
		t.Fatalf("CreateBatch: %v", err)
	}

	// Release A becomes current between staging and approval.
	for _, s := range []struct{ release, status string }{
		{scfreleasetest.ReleaseB, "legacy"}, {scfreleasetest.ReleaseA, "current"},
	} {
		if _, err := admin.Exec(context.Background(),
			`UPDATE framework_versions SET status = $2 WHERE tenant_id IS NULL AND version = $1`, s.release, s.status); err != nil {
			t.Fatalf("set %s %s: %v", s.release, s.status, err)
		}
	}

	d, err := store.ApproveRow(ctx, ApproveRowInput{BatchID: b.ID, RowID: b.Staged[0].ID, Approver: uuid.NewString(), SCFID: "IAC-06"})
	if err != nil {
		t.Fatalf("ApproveRow: %v", err)
	}
	var bound, inB, inA uuid.UUID
	if err := admin.QueryRow(context.Background(), `SELECT scf_anchor_id FROM controls WHERE id = $1`, d.ControlID).Scan(&bound); err != nil {
		t.Fatalf("read control: %v", err)
	}
	for release, dst := range map[string]*uuid.UUID{scfreleasetest.ReleaseB: &inB, scfreleasetest.ReleaseA: &inA} {
		if err := admin.QueryRow(context.Background(), `
			SELECT a.id FROM scf_anchors a JOIN framework_versions fv ON fv.id = a.framework_version_id
			WHERE fv.tenant_id IS NULL AND fv.version = $1 AND a.scf_id = 'IAC-06'`, release).Scan(dst); err != nil {
			t.Fatalf("IAC-06 in %s: %v", release, err)
		}
	}
	if bound != inB {
		t.Fatalf("approved control bound to %s; want the staged release B's IAC-06 %s (A's, now current, is %s)", bound, inB, inA)
	}
}

func TestApproveRow_ConcurrentBundleIDCollisionRollsBack(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	code, rel, fw := seedRaceFixture(t, admin)
	tenant := dbtest.SeedTenant(t, admin,
		"control_import_candidates", "control_import_rejected_rows", "control_import_rows",
		"control_import_batches", "controls")
	ctx := dbtest.WithTenantCtx(t, tenant)
	store := NewStore(app)

	b, err := store.CreateBatch(ctx, Batch{
		Framework: fw, SourceFilename: "race.csv", SourceFormat: "csv", Status: BatchStatusStaged,
		TotalRows: 1, StagedCount: 1, SCFRelease: &rel, SuggestionsStatus: SuggestionsReady,
		Staged: []BatchControl{{RowNumber: 2, ExternalID: "AC 01", Title: "Access control", Description: "d", Status: RowStatusPending}},
	})
	if err != nil {
		t.Fatalf("CreateBatch: %v", err)
	}
	rowID := b.Staged[0].ID

	// Another approval ("AC-01", the same slug) commits its control after
	// this approval's bundle-ID check and before its upload.
	var rival control.UploadResult
	beforeControlUpload = func(_ context.Context, bundleID string) {
		raw := "bundle_schema_version: \"1\"\nbundle_id: " + bundleID + "\ntitle: Rival\nscf_anchor_id: " + code +
			"\nscf_release: \"" + rel.Version + "\"\nimplementation_type: manual_attested\n"
		bundle, err := control.FinalizeBundleForHTTP([]byte(raw))
		if err != nil {
			t.Fatalf("rival bundle: %v", err)
		}
		if rival, err = control.NewStore(app).Upload(ctx, bundle, "rival"); err != nil {
			t.Fatalf("rival upload: %v", err)
		}
	}
	t.Cleanup(func() { beforeControlUpload = nil })

	_, err = store.ApproveRow(ctx, ApproveRowInput{BatchID: b.ID, RowID: rowID, Approver: uuid.NewString(), SCFID: code})
	if !errors.Is(err, ErrBundleIDTaken) {
		t.Fatalf("ApproveRow = %v, want ErrBundleIDTaken", err)
	}
	if !rival.IsNewBundle {
		t.Fatalf("rival upload = %+v, want a new bundle", rival)
	}

	// The rival's control is untouched: one row, version 1, still active.
	var n, version int
	var superseded bool
	if err := admin.QueryRow(context.Background(), `
		SELECT count(*), max(version), bool_or(superseded_by IS NOT NULL)
		FROM controls WHERE tenant_id = $1 AND bundle_id = 'ac-01'`, tenant).Scan(&n, &version, &superseded); err != nil {
		t.Fatalf("read controls: %v", err)
	}
	if n != 1 || version != 1 || superseded {
		t.Errorf("ac-01 controls = %d, max version %d, superseded %v; want the rival's one active v1", n, version, superseded)
	}
	// The row stays pending with no decision and no audit row.
	var status string
	var decided *string
	var audits int
	if err := admin.QueryRow(context.Background(),
		`SELECT status, decided_by FROM control_import_rows WHERE id = $1`, rowID).Scan(&status, &decided); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM control_import_audit_log WHERE row_id = $1`, rowID).Scan(&audits); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if status != RowStatusPending || decided != nil || audits != 0 {
		t.Errorf("row status %q decided_by %v audits %d; want pending, undecided, none", status, decided, audits)
	}
}
