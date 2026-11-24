//go:build integration

package soc2import_test

// Issue #1773: stock content is pinned to the SCF release the bootstrap
// imports. A fresh bootstrap (deploy/docker/bootstrap/bootstrap.sh) imports
// migrations/fixtures/scf-sample.json, the bundled crosswalks and the 65
// stock control bundles; every one binds in the sample's release, with no
// "declares no scf_release" deprecation warning. An install bootstrapped
// before #1773 stores the unpinned stock manifests; its next boot updates
// each control in place, so owners and evidence stay attached.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/soc2import"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// stockBundleDirs returns every controls/soc2/<bundle> directory.
func stockBundleDirs(t *testing.T) []string {
	t.Helper()
	dirs, err := filepath.Glob(filepath.Join("..", "..", "..", "controls", "soc2", "*"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range dirs {
		if _, err := os.Stat(filepath.Join(d, "control.yaml")); err == nil {
			out = append(out, d)
		}
	}
	if len(out) == 0 {
		t.Fatal("no stock control bundles found; the test would pass vacuously")
	}
	return out
}

// controlRelease reads the SCF release label of the anchor a control is
// bound to, or "" when it is unanchored.
func controlRelease(t *testing.T, pool *pgxpool.Pool, controlID uuid.UUID) string {
	t.Helper()
	var release *string
	if err := pool.QueryRow(context.Background(), `
		SELECT fv.version
		FROM controls c
		LEFT JOIN scf_anchors a ON a.id = c.scf_anchor_id
		LEFT JOIN framework_versions fv ON fv.id = a.framework_version_id
		WHERE c.id = $1`, controlID).Scan(&release); err != nil {
		t.Fatalf("read control %s release: %v", controlID, err)
	}
	if release == nil {
		return ""
	}
	return *release
}

func tenantCtx(t *testing.T, tenant string) context.Context {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return ctx
}

func TestFreshBootstrap_StockContentBindsInTheSampleRelease(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	ctx := context.Background()
	wipeSCFReleases(t, pool)
	t.Cleanup(func() {
		wipeSCFReleases(t, pool)
		ensureSCFLoaded(t, pool) // the shared catalog the other tests expect
	})

	// Phase 3: the SCF sample.
	sample := loadSCFSample(t)
	importSCF(t, pool, sample)
	release := sample.ReleaseVersion

	// Phase 3: the bundled crosswalks (SOC 2 at every boot, the others
	// when an operator opts in).
	files, err := filepath.Glob(filepath.Join("..", "..", "..", "data", "crosswalks", "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("glob data/crosswalks: %v (%d files)", err, len(files))
	}
	for _, f := range files {
		cw, err := soc2import.Load(f)
		if err != nil {
			t.Fatalf("Load %s: %v", f, err)
		}
		rep, err := soc2import.Import(ctx, pool, cw)
		if err != nil {
			t.Fatalf("Import %s: %v", f, err)
		}
		if rep.SCFRelease != release {
			t.Errorf("%s: bound in SCF release %q; want %s", f, rep.SCFRelease, release)
		}
		var total, outside int
		if err := pool.QueryRow(ctx, `
			SELECT count(*), count(*) FILTER (WHERE av.version <> $3)
			FROM fw_to_scf_edges e
			JOIN framework_versions rv ON rv.id = (
				SELECT r.framework_version_id FROM framework_requirements r WHERE r.id = e.framework_requirement_id)
			JOIN frameworks fw ON fw.id = rv.framework_id
			JOIN scf_anchors a ON a.id = e.scf_anchor_id
			JOIN framework_versions av ON av.id = a.framework_version_id
			WHERE fw.slug = $1 AND rv.version = $2`,
			cw.FrameworkSlug, cw.FrameworkVersion, release).Scan(&total, &outside); err != nil {
			t.Fatalf("%s: count edges outside %s: %v", f, release, err)
		}
		if total == 0 {
			t.Errorf("%s: no edges imported", f)
		}
		if outside != 0 {
			t.Errorf("%s: %d edges bound outside SCF release %s", f, outside, release)
		}
	}

	// Phase 5b: the stock control bundles.
	tenant := dbtest.SeedTenant(t, pool, "control_manifest_revisions", "controls")
	store := control.NewStore(app)
	uploaded := 0
	for _, dir := range stockBundleDirs(t) {
		b, err := control.ParseDirectory(dir)
		if err != nil {
			t.Fatalf("parse %s: %v", dir, err)
		}
		res, err := store.Upload(tenantCtx(t, tenant), b, "oauth_client:bootstrap")
		if err != nil {
			t.Errorf("%s: upload: %v", dir, err)
			continue
		}
		if b.Manifest.SCFAnchorID == "" {
			continue
		}
		if res.SCFRelease != release {
			t.Errorf("%s: bound in SCF release %q; want %s", dir, res.SCFRelease, release)
		}
		if res.SCFRelease != release {
			t.Errorf("%s: bound in SCF release %q; want %q", dir, res.SCFRelease, release)
		}
		if got := controlRelease(t, pool, res.ControlID); got != release {
			t.Errorf("%s: control is on an anchor in %q; want %q", dir, got, release)
		}
		uploaded++
	}
	if uploaded == 0 {
		t.Fatal("no anchored stock control was uploaded; the test would pass vacuously")
	}
}

// TestUpgradeFromUnpinnedStock_UpdatesInPlace plays the first boot after
// #1773 on an install whose stock controls were uploaded from the pre-#1773
// shipped bytes (no scf_release). Declaring scf_release is metadata
// (control.SupersedingFields), so every control keeps its id and version.
func TestUpgradeFromUnpinnedStock_UpdatesInPlace(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	wipeSCFReleases(t, pool)
	t.Cleanup(func() {
		wipeSCFReleases(t, pool)
		ensureSCFLoaded(t, pool)
	})
	sample := loadSCFSample(t)
	importSCF(t, pool, sample)
	pin := []byte("scf_release: " + sample.ReleaseVersion + "\n")

	tenant := dbtest.SeedTenant(t, pool, "control_manifest_revisions", "controls")
	store := control.NewStore(app)
	upgraded := 0
	for _, dir := range stockBundleDirs(t) {
		shipped, err := os.ReadFile(filepath.Join(dir, "control.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		before := bytes.Replace(shipped, pin, nil, 1)
		if bytes.Equal(before, shipped) {
			t.Fatalf("%s: control.yaml does not declare %s", dir, pin)
		}
		prior, err := control.FinalizeBundleForHTTP(before)
		if err != nil {
			t.Fatalf("%s: parse pre-#1773 manifest: %v", dir, err)
		}
		was, err := store.Upload(tenantCtx(t, tenant), prior, "oauth_client:bootstrap")
		if err != nil {
			t.Fatalf("%s: pre-#1773 upload: %v", dir, err)
		}
		next, err := control.FinalizeBundleForHTTP(shipped)
		if err != nil {
			t.Fatalf("%s: parse shipped manifest: %v", dir, err)
		}
		now, err := store.Upload(tenantCtx(t, tenant), next, "oauth_client:bootstrap")
		if err != nil {
			t.Fatalf("%s: upgrade upload: %v", dir, err)
		}
		if !now.UpdatedInPlace || now.ControlID != was.ControlID || now.Version != was.Version || now.SupersededID != uuid.Nil {
			t.Errorf("%s: upgrade should update in place; got %+v (prior id %s v%d)", dir, now, was.ControlID, was.Version)
			continue
		}
		if now.AnchorID != was.AnchorID {
			t.Errorf("%s: anchor moved %s -> %s; the code resolves to the same row", dir, was.AnchorID, now.AnchorID)
		}
		upgraded++
	}
	if upgraded == 0 {
		t.Fatal("no stock control was upgraded; the test would pass vacuously")
	}
}
