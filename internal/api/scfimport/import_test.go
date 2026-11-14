//go:build integration

package scfimport_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// truncateCatalog wipes any prior SCF rows so each test starts clean. The
// fixture sample uses a deterministic release_version, so reruns would
// otherwise stack with previous test runs in the same DB.
//
// Slice 007 added fw_to_scf_edges + framework_requirements, both of which
// depend on scf_anchors / framework_versions. Wipe them first so the FK
// cascade order is correct.
func truncateCatalog(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	// Slice 461: TRUNCATE controls CASCADE first. Under the full
	// alphabetical wildcard run (`go test ./internal/api/...`) the
	// internal/api/controls* packages run BEFORE scfimport and leave
	// controls rows whose `scf_anchor_id` FK is `ON DELETE RESTRICT`,
	// which blocks the `DELETE FROM scf_anchors` below. CI's curated
	// order runs scfimport first so it never hit this, but the suite
	// must be order-independent. Mirrors ucfcoverage's wipeTenantControls.
	for _, stmt := range []string{
		"TRUNCATE controls RESTART IDENTITY CASCADE",
		"DELETE FROM fw_to_scf_edges",
		"DELETE FROM framework_requirements",
		"DELETE FROM scf_anchors",
		"DELETE FROM framework_versions WHERE tenant_id IS NULL",
		"DELETE FROM frameworks WHERE tenant_id IS NULL",
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("cleanup %q: %v", stmt, err)
		}
	}
}

func loadFixture(t *testing.T) *scfimport.Catalog {
	t.Helper()
	cat, err := scfimport.Load("../../../migrations/fixtures/scf-sample.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cat
}

func TestImport_FirstRunCreatesFrameworkVersionAndAnchors(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	truncateCatalog(t, pool)
	cat := loadFixture(t)

	report, err := scfimport.Import(context.Background(), pool, cat)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if !report.IsNewVersion {
		t.Fatal("first import should be a new version")
	}
	if report.Created != len(cat.Controls) {
		t.Fatalf("created = %d; want %d", report.Created, len(cat.Controls))
	}
	if report.Updated+report.Unchanged != 0 {
		t.Fatalf("first import has updates/unchanged: %+v", report)
	}
	if report.ReleaseVersion != cat.ReleaseVersion {
		t.Fatalf("release_version = %q; want %q", report.ReleaseVersion, cat.ReleaseVersion)
	}
}

func TestImport_SecondRunSameReleaseIsIdempotent(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	truncateCatalog(t, pool)
	cat := loadFixture(t)

	if _, err := scfimport.Import(context.Background(), pool, cat); err != nil {
		t.Fatalf("first Import: %v", err)
	}
	report, err := scfimport.Import(context.Background(), pool, cat)
	if err != nil {
		t.Fatalf("second Import: %v", err)
	}
	if report.IsNewVersion {
		t.Fatal("second import of same release should not be a new version")
	}
	if report.Created != 0 {
		t.Fatalf("created = %d; want 0 on idempotent re-import", report.Created)
	}
	if report.Unchanged != len(cat.Controls) {
		t.Fatalf("unchanged = %d; want %d", report.Unchanged, len(cat.Controls))
	}
}

func TestImport_NewReleaseCreatesNewFrameworkVersion(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	truncateCatalog(t, pool)

	v1 := loadFixture(t)
	if _, err := scfimport.Import(context.Background(), pool, v1); err != nil {
		t.Fatalf("v1 Import: %v", err)
	}

	v2 := loadFixture(t)
	v2.ReleaseVersion = "test-2026.2"
	v2.Controls = v2.Controls[:5] // smaller payload for v2
	report, err := scfimport.Import(context.Background(), pool, v2)
	if err != nil {
		t.Fatalf("v2 Import: %v", err)
	}
	if !report.IsNewVersion {
		t.Fatal("new release should be a new version")
	}
	if report.Created != len(v2.Controls) {
		t.Fatalf("created = %d; want %d", report.Created, len(v2.Controls))
	}

	// Old version still queryable: count its anchors.
	var count int
	err = pool.QueryRow(
		context.Background(),
		`SELECT count(*) FROM scf_anchors a
		 JOIN framework_versions fv ON fv.id = a.framework_version_id
		 WHERE fv.version = $1`,
		v1.ReleaseVersion,
	).Scan(&count)
	if err != nil {
		t.Fatalf("query v1 anchor count: %v", err)
	}
	if count != len(v1.Controls) {
		t.Fatalf("v1 anchors lost on v2 import: %d remain, expected %d", count, len(v1.Controls))
	}

	// Old "current" → "legacy"; new is "current".
	var v1Status, v2Status string
	if err := pool.QueryRow(context.Background(),
		`SELECT status::text FROM framework_versions WHERE version = $1`, v1.ReleaseVersion).Scan(&v1Status); err != nil {
		t.Fatalf("query v1 status: %v", err)
	}
	if err := pool.QueryRow(context.Background(),
		`SELECT status::text FROM framework_versions WHERE version = $1`, v2.ReleaseVersion).Scan(&v2Status); err != nil {
		t.Fatalf("query v2 status: %v", err)
	}
	if v1Status != "legacy" {
		t.Fatalf("v1 status = %q; want legacy", v1Status)
	}
	if v2Status != "current" {
		t.Fatalf("v2 status = %q; want current", v2Status)
	}
}

// anchorSnapshot captures every scf_anchors row (content + xmin) and
// the SCF framework_versions rows, so a test can assert a re-import wrote
// nothing.
func anchorSnapshot(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var snap string
	err := pool.QueryRow(context.Background(), `
		SELECT coalesce((SELECT string_agg(concat_ws('|', a.id, a.scf_id, a.family, a.title,
		                    a.description, a.subtopics::text, a.xmin::text), E'\n' ORDER BY a.id)
		                 FROM scf_anchors a), '')
		    || E'\n--\n' ||
		    coalesce((SELECT string_agg(concat_ws('|', fv.id, fv.version, fv.status::text,
		                    fv.xmin::text), E'\n' ORDER BY fv.id)
		                 FROM framework_versions fv WHERE fv.tenant_id IS NULL), '')
		    || E'\n--\n' ||
		    coalesce((SELECT string_agg(concat_ws('|', f.id, f.latest_version_id, f.xmin::text), E'\n' ORDER BY f.id)
		                 FROM frameworks f WHERE f.slug = 'scf'), '')`).Scan(&snap)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	return snap
}

func TestImport_SameReleaseIdenticalContentChangesNothing(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	truncateCatalog(t, pool)
	cat := loadFixture(t)
	if _, err := scfimport.Import(context.Background(), pool, cat); err != nil {
		t.Fatalf("first Import: %v", err)
	}
	before := anchorSnapshot(t, pool)

	report, err := scfimport.Import(context.Background(), pool, loadFixture(t))
	if err != nil {
		t.Fatalf("identical re-import: %v", err)
	}
	if report.Created != 0 || report.Updated != 0 || report.Unchanged != len(cat.Controls) {
		t.Fatalf("report = %+v; want all %d unchanged", report, len(cat.Controls))
	}
	if after := anchorSnapshot(t, pool); after != before {
		t.Fatalf("identical re-import changed the database:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestImport_SameReleaseChangedTitleIsRefused(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	truncateCatalog(t, pool)
	cat := loadFixture(t)
	if _, err := scfimport.Import(context.Background(), pool, cat); err != nil {
		t.Fatalf("first Import: %v", err)
	}
	before := anchorSnapshot(t, pool)

	changed := loadFixture(t)
	changed.Controls[0].Title = "MUTATED: title rewritten for the test"
	target := changed.Controls[0].SCFID
	_, err := scfimport.Import(context.Background(), pool, changed)
	if err == nil {
		t.Fatal("re-import with a changed title succeeded; want refusal")
	}
	var conflict *scfimport.ReleaseConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("error = %v (%T); want *scfimport.ReleaseConflictError", err, err)
	}
	if len(conflict.Changed) != 1 || conflict.Changed[0] != target ||
		len(conflict.Added) != 0 || len(conflict.Missing) != 0 {
		t.Fatalf("conflict = %+v; want only %s changed", conflict, target)
	}
	if !strings.Contains(err.Error(), target) {
		t.Fatalf("error %q does not name anchor %s", err, target)
	}
	if after := anchorSnapshot(t, pool); after != before {
		t.Fatalf("refused re-import wrote to the database:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestImport_SameReleaseAddedOrMissingAnchorIsRefused(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	truncateCatalog(t, pool)
	cat := loadFixture(t)
	if _, err := scfimport.Import(context.Background(), pool, cat); err != nil {
		t.Fatalf("first Import: %v", err)
	}

	shorter := loadFixture(t)
	dropped := shorter.Controls[len(shorter.Controls)-1].SCFID
	shorter.Controls = shorter.Controls[:len(shorter.Controls)-1]
	shorter.Controls = append(shorter.Controls, scfimport.Control{SCFID: "ZZZ-99", Family: "ZZZ", Title: "Added"})
	_, err := scfimport.Import(context.Background(), pool, shorter)
	var conflict *scfimport.ReleaseConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("error = %v; want *scfimport.ReleaseConflictError", err)
	}
	if len(conflict.Added) != 1 || conflict.Added[0] != "ZZZ-99" ||
		len(conflict.Missing) != 1 || conflict.Missing[0] != dropped {
		t.Fatalf("conflict = %+v; want added ZZZ-99, missing %s", conflict, dropped)
	}
}

// TestSCFAnchor_DirectUpdateRejected proves the trigger: an UPDATE of an
// imported anchor's title is refused for the migrate role (the trigger) and
// for the app role (which holds only SELECT, so the GRANT refuses it before
// the trigger runs). A no-content UPDATE still passes for the migrate role.
func TestSCFAnchor_DirectUpdateRejected(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	truncateCatalog(t, migrate)
	cat := loadFixture(t)
	if _, err := scfimport.Import(context.Background(), migrate, cat); err != nil {
		t.Fatalf("Import: %v", err)
	}
	scfID := cat.Controls[0].SCFID
	const update = `UPDATE scf_anchors SET title = 'rewritten' WHERE scf_id = $1`

	_, err := migrate.Exec(context.Background(), update, scfID)
	if err == nil || !strings.Contains(err.Error(), "imported anchors are frozen") {
		t.Fatalf("migrate-role UPDATE: err = %v; want the immutability trigger's rejection", err)
	}

	_, err = app.Exec(context.Background(), update, scfID)
	if err == nil {
		t.Fatal("app-role UPDATE of an anchor title succeeded; want rejection")
	}

	if _, err := migrate.Exec(context.Background(),
		`UPDATE scf_anchors SET updated_at = now() WHERE scf_id = $1`, scfID); err != nil {
		t.Fatalf("non-content UPDATE rejected: %v", err)
	}

	var title string
	if err := migrate.QueryRow(context.Background(),
		`SELECT title FROM scf_anchors WHERE scf_id = $1`, scfID).Scan(&title); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if title != cat.Controls[0].Title {
		t.Fatalf("title = %q; want unchanged %q", title, cat.Controls[0].Title)
	}
}

func TestLoad_RejectsBadSchemaVersion(t *testing.T) {
	t.Parallel()
	if _, err := scfimport.Load("../../../migrations/fixtures/scf-sample.json"); err != nil {
		t.Fatalf("loading the fixture should succeed: %v", err)
	}

	// Write a temp file with a bad schema_version.
	tmp, err := os.CreateTemp(t.TempDir(), "bad-*.json")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	if _, err := tmp.WriteString(`{"schema_version":"99.0","release_version":"x","controls":[{"scf_id":"X-1","family":"X","title":"X"}]}`); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = tmp.Close()
	if _, err := scfimport.Load(tmp.Name()); err == nil {
		t.Fatal("expected error for unsupported schema_version")
	}
}
