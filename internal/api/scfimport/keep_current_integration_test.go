//go:build integration

package scfimport_test

// #3061: Options.KeepCurrent (`atlas-cli catalog import-scf --keep-current`)
// loads a new release as legacy beside the current one, so the docker
// bootstrap can load the bundled sample next to an operator's own release.

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// scfVersions returns "version:status" for every SCF release, and the
// release frameworks.latest_version_id points at.
func scfVersions(t *testing.T, pool *pgxpool.Pool) (map[string]string, string) {
	t.Helper()
	ctx := context.Background()
	rows, err := pool.Query(ctx, `
		SELECT fv.version, fv.status::text FROM framework_versions fv
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = 'scf' AND f.tenant_id IS NULL`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var v, s string
		if err := rows.Scan(&v, &s); err != nil {
			t.Fatal(err)
		}
		out[v] = s
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	var latest string
	if err := pool.QueryRow(ctx, `
		SELECT fv.version FROM frameworks f JOIN framework_versions fv ON fv.id = f.latest_version_id
		WHERE f.slug = 'scf' AND f.tenant_id IS NULL`).Scan(&latest); err != nil {
		t.Fatalf("read latest_version_id: %v", err)
	}
	return out, latest
}

func operatorRelease(t *testing.T) *scfimport.Catalog {
	t.Helper()
	cat := loadFixture(t)
	cat.ReleaseVersion = "test-operator-3061"
	return cat
}

func TestImportKeepCurrent_LoadsANewReleaseAsLegacy(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	truncateCatalog(t, pool)
	ctx := context.Background()

	operator := operatorRelease(t)
	if _, err := scfimport.Import(ctx, pool, operator); err != nil {
		t.Fatalf("import operator release: %v", err)
	}
	sample := loadFixture(t)
	rep, err := scfimport.ImportWithOptions(ctx, pool, sample, scfimport.Options{KeepCurrent: true})
	if err != nil {
		t.Fatalf("import sample with KeepCurrent: %v", err)
	}
	if !rep.IsNewVersion || rep.Status != "legacy" || rep.Created != len(sample.Controls) {
		t.Fatalf("report = %+v; want a new legacy version with %d anchors", rep, len(sample.Controls))
	}
	versions, latest := scfVersions(t, pool)
	if versions[operator.ReleaseVersion] != "current" || versions[sample.ReleaseVersion] != "legacy" || latest != operator.ReleaseVersion {
		t.Fatalf("versions = %v, latest = %s; want %s current and latest, %s legacy",
			versions, latest, operator.ReleaseVersion, sample.ReleaseVersion)
	}

	// Re-running either import changes no status: the sample is frozen and
	// stays legacy, the operator's release stays current.
	if rep, err := scfimport.ImportWithOptions(ctx, pool, sample, scfimport.Options{KeepCurrent: true}); err != nil || rep.IsNewVersion || rep.Status != "legacy" {
		t.Fatalf("re-import sample: report %+v, err %v; want an unchanged legacy release", rep, err)
	}
	if rep, err := scfimport.Import(ctx, pool, operator); err != nil || rep.IsNewVersion || rep.Status != "current" {
		t.Fatalf("re-import operator release: report %+v, err %v; want an unchanged current release", rep, err)
	}
	if again, latest := scfVersions(t, pool); again[operator.ReleaseVersion] != "current" || again[sample.ReleaseVersion] != "legacy" || latest != operator.ReleaseVersion {
		t.Fatalf("after re-imports: versions = %v, latest = %s", again, latest)
	}
}

func TestImportKeepCurrent_WithNoCurrentReleaseTheNewOneIsCurrent(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	truncateCatalog(t, pool)

	sample := loadFixture(t)
	rep, err := scfimport.ImportWithOptions(context.Background(), pool, sample, scfimport.Options{KeepCurrent: true})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if rep.Status != "current" {
		t.Fatalf("status = %q; want current", rep.Status)
	}
	if versions, latest := scfVersions(t, pool); versions[sample.ReleaseVersion] != "current" || latest != sample.ReleaseVersion {
		t.Fatalf("versions = %v, latest = %s", versions, latest)
	}
}

// Without KeepCurrent a new release still demotes the current one.
func TestImport_NewReleaseStillDemotesTheCurrentOne(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	truncateCatalog(t, pool)
	ctx := context.Background()

	operator := operatorRelease(t)
	if _, err := scfimport.Import(ctx, pool, operator); err != nil {
		t.Fatal(err)
	}
	sample := loadFixture(t)
	rep, err := scfimport.Import(ctx, pool, sample)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != "current" {
		t.Fatalf("status = %q; want current", rep.Status)
	}
	if versions, latest := scfVersions(t, pool); versions[operator.ReleaseVersion] != "legacy" || latest != sample.ReleaseVersion {
		t.Fatalf("versions = %v, latest = %s", versions, latest)
	}
}
