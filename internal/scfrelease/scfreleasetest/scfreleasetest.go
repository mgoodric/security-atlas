// Package scfreleasetest loads the #1770 two-release SCF fixtures
// (internal/scfrelease/testdata) into a real Postgres for integration tests.
package scfreleasetest

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/scfimport"
)

// Release labels of the two fixture catalogs. B is imported second, so it
// is the current release once LoadAB returns and A is legacy.
const (
	ReleaseA = "test-1770-a"
	ReleaseB = "test-1770-b"
)

// CatalogPath returns the absolute path of a fixture catalog file
// ("scf-release-a.json" or "scf-release-b.json").
func CatalogPath(name string) string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "testdata", name)
}

// LoadAB imports release A then release B through the real importer, so B
// is the only current SCF release while the test runs. admin must be the
// migrate (BYPASSRLS) pool.
//
// It deletes nothing it did not create. Any SCF release that was current
// beforehand is demoted to legacy for the test and restored by the
// registered cleanup, which also removes both fixture releases and any
// control bound to their anchors. The SCF catalog is global, so this is
// not safe beside a parallel test that reads it; the integration tier runs
// packages with -p 1 and these tests are not t.Parallel.
func LoadAB(t *testing.T, admin *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	// A run that died before its cleanup leaves the fixture releases
	// behind; a re-import of a loaded label is a no-op that would not make
	// B current, so clear them first.
	removeAB(t, admin)

	type latest struct{ framework, version uuid.UUID }
	var wasCurrent []uuid.UUID
	var latests []latest
	rows, err := admin.Query(ctx, `
		SELECT fv.id FROM framework_versions fv
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = 'scf' AND f.tenant_id IS NULL AND fv.status = 'current'`)
	if err != nil {
		t.Fatalf("read current SCF releases: %v", err)
	}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan current SCF release: %v", err)
		}
		wasCurrent = append(wasCurrent, id)
	}
	rows.Close()
	rows, err = admin.Query(ctx, `
		SELECT id, latest_version_id FROM frameworks
		WHERE slug = 'scf' AND tenant_id IS NULL AND latest_version_id IS NOT NULL`)
	if err != nil {
		t.Fatalf("read SCF latest versions: %v", err)
	}
	for rows.Next() {
		var l latest
		if err := rows.Scan(&l.framework, &l.version); err != nil {
			t.Fatalf("scan SCF latest version: %v", err)
		}
		latests = append(latests, l)
	}
	rows.Close()

	t.Cleanup(func() {
		removeAB(t, admin)
		for _, id := range wasCurrent {
			if _, err := admin.Exec(ctx, `UPDATE framework_versions SET status = 'current' WHERE id = $1`, id); err != nil {
				t.Errorf("restore current SCF release %s: %v", id, err)
			}
		}
		for _, l := range latests {
			if _, err := admin.Exec(ctx, `UPDATE frameworks SET latest_version_id = $2 WHERE id = $1`, l.framework, l.version); err != nil {
				t.Errorf("restore SCF latest version: %v", err)
			}
		}
	})

	if _, err := admin.Exec(ctx, `UPDATE framework_versions SET status = 'legacy' WHERE id = ANY($1)`, wasCurrent); err != nil {
		t.Fatalf("demote current SCF releases: %v", err)
	}
	for _, name := range []string{"scf-release-a.json", "scf-release-b.json"} {
		cat, err := scfimport.Load(CatalogPath(name))
		if err != nil {
			t.Fatalf("load %s: %v", name, err)
		}
		if _, err := scfimport.Import(ctx, admin, cat); err != nil {
			t.Fatalf("import %s: %v", name, err)
		}
	}
}

func removeAB(t *testing.T, admin *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
		`DELETE FROM controls WHERE scf_anchor_id IN (
			SELECT a.id FROM scf_anchors a
			JOIN framework_versions fv ON fv.id = a.framework_version_id
			WHERE fv.tenant_id IS NULL AND fv.version IN ($1, $2))`,
		`UPDATE frameworks SET latest_version_id = NULL
			WHERE tenant_id IS NULL AND slug = 'scf' AND latest_version_id IN (
				SELECT id FROM framework_versions WHERE tenant_id IS NULL AND version IN ($1, $2))`,
		`DELETE FROM framework_versions WHERE tenant_id IS NULL AND version IN ($1, $2)`,
	} {
		if _, err := admin.Exec(ctx, stmt, ReleaseA, ReleaseB); err != nil {
			t.Errorf("remove fixture releases: %v", err)
		}
	}
}
