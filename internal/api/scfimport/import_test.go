//go:build integration

package scfimport_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/scfimport"
)

// adminDSN is the DSN with write access on scf_anchors. Tests skip when it
// is not set so the suite still runs locally without a DB.
func adminDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	return dsn
}

func openPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, adminDSN(t))
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// truncateCatalog wipes any prior SCF rows so each test starts clean. The
// fixture sample uses a deterministic release_version, so reruns would
// otherwise stack with previous test runs in the same DB.
func truncateCatalog(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
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
	pool := openPool(t)
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
	pool := openPool(t)
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
	pool := openPool(t)
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

func TestImport_DetectsContentUpdates(t *testing.T) {
	pool := openPool(t)
	truncateCatalog(t, pool)

	cat := loadFixture(t)
	if _, err := scfimport.Import(context.Background(), pool, cat); err != nil {
		t.Fatalf("first Import: %v", err)
	}

	// Mutate one anchor's description and re-import the same release.
	cat.Controls[0].Description = "MUTATED: description rewritten for the test"
	report, err := scfimport.Import(context.Background(), pool, cat)
	if err != nil {
		t.Fatalf("second Import: %v", err)
	}
	if report.Updated != 1 {
		t.Fatalf("updated = %d; want 1", report.Updated)
	}
	if report.Unchanged != len(cat.Controls)-1 {
		t.Fatalf("unchanged = %d; want %d", report.Unchanged, len(cat.Controls)-1)
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
