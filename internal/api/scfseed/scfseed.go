// Package scfseed is the shared SCF-catalog seeding helper for integration
// tests. It exists to remove the seed-order coupling described in slice 461:
// before this package, every integration test that needed the SCF catalog
// carried its own inline guard of the shape `if anchorCount == 0 { reseed }`.
// That guard counts ALL rows in scf_anchors across ALL framework versions,
// so a non-zero count was treated as "fully seeded" even when an earlier
// package in a `-p 1` run had left the CURRENT SCF framework version holding
// only a partial subset (e.g. a 5-control "new release" import, or a scoped
// DELETE). The next package then skipped its reseed and its SOC 2 crosswalk
// import failed with `scf_anchor "GOV-01" not found`.
//
// The fix is to make the guard CATALOG-COMPLETENESS-aware rather than
// row-count-aware, using the exact resolution path the SOC 2 importer uses:
// "is the sentinel anchor GOV-01 resolvable in the CURRENT SCF framework
// version?" If yes, the catalog is fully seeded and seeding is a no-op. If
// no, the catalog is absent OR partial, so we do a full clean reseed
// (wipe the SCF + SOC 2 platform-layer rows, then reimport both fixtures).
// This makes every consumer self-correcting regardless of invocation order
// — the property the integration suite actually wants (slice 461 AC-2).
//
// This package is NOT build-tagged: it is an ordinary importable helper
// (mirroring internal/api/testjwt), so the build-tagged `_test.go` files
// that consume it can import it without a tag dance.
package scfseed

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/api/soc2import"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// SentinelAnchor is the SCF code the completeness guard probes for. GOV-01
// is the canonical sentinel because it is the anchor whose absence produced
// the original slice 461 failure signature
// (`crosswalk: scf_anchor "GOV-01" not found` — the loader error prefix was
// generalized from `soc2import:` to `crosswalk:` in slice 438), and it is
// present in the
// committed sample fixture (migrations/fixtures/scf-sample.json). If the
// sample fixture is ever reshaped to drop GOV-01, IsCatalogComplete will
// (correctly) report incomplete and force a reseed — so the worst failure
// mode of a stale sentinel is a redundant reseed, never a false "complete".
const SentinelAnchor = "GOV-01"

// fixturePaths are resolved once, lazily, relative to THIS source file via
// runtime.Caller, so callers at any directory depth resolve the same files
// independent of the test's working directory.
var (
	pathOnce      sync.Once
	scfSamplePath string
	soc2CWPath    string
	pathErr       error
)

func resolvePaths() {
	pathOnce.Do(func() {
		_, thisFile, _, ok := runtime.Caller(0)
		if !ok {
			pathErr = fmt.Errorf("scfseed: runtime.Caller failed; cannot locate fixtures")
			return
		}
		// Walk up from this file's directory until we find the repo root
		// (the directory that holds go.mod). The fixtures live at known
		// paths beneath it.
		dir := filepath.Dir(thisFile)
		for {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				pathErr = fmt.Errorf("scfseed: walked to filesystem root without finding go.mod")
				return
			}
			dir = parent
		}
		scfSamplePath = filepath.Join(dir, "migrations", "fixtures", "scf-sample.json")
		soc2CWPath = filepath.Join(dir, "data", "crosswalks", "soc2-tsc-2017.yaml")
	})
}

// IsCatalogComplete reports whether the CURRENT SCF framework version holds
// a fully-seeded catalog, probed by the same query path the SOC 2 importer
// uses (GetSCFAnchorBySCFID resolves against `slug='scf' AND status='current'`).
// A nil error with ok=true means the sentinel anchor resolves; ok=false means
// the catalog is absent or partial and the caller should reseed.
func IsCatalogComplete(ctx context.Context, pool *pgxpool.Pool) (ok bool, err error) {
	q := dbx.New(pool)
	_, err = q.GetSCFAnchorBySCFID(ctx, SentinelAnchor)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("scfseed: probe sentinel anchor %q: %w", SentinelAnchor, err)
	}
	return true, nil
}

// EnsureSCFCatalog guarantees the CURRENT SCF framework version holds the
// full sample catalog, in a way that is robust to any prior package having
// left the catalog partial. It does NOT touch the SOC 2 crosswalk — use it
// when the caller imports the crosswalk itself (e.g. the soc2import tests,
// where the crosswalk import is the unit under test). It is idempotent and
// order-independent:
//
//   - If the catalog is already complete (sentinel resolves), it is a no-op.
//   - If the catalog is absent or partial, it performs a full clean reseed:
//     wipe the SCF + SOC 2 platform-layer rows in FK order, then reimport
//     the SCF sample. (The SOC 2 rows are wiped because the SCF reseed
//     invalidates their anchor FKs; the caller is responsible for
//     re-importing the crosswalk it owns.)
//
// pool MUST be the admin pool (BYPASSRLS) — seeding writes platform-layer,
// tenant-NULL rows.
func EnsureSCFCatalog(ctx context.Context, pool *pgxpool.Pool) error {
	resolvePaths()
	if pathErr != nil {
		return pathErr
	}

	complete, err := IsCatalogComplete(ctx, pool)
	if err != nil {
		return err
	}
	if !complete {
		if err := fullReseedSCF(ctx, pool); err != nil {
			return err
		}
	}
	return nil
}

// EnsureFullCatalog guarantees the CURRENT SCF framework version holds the
// full sample catalog AND the SOC 2 crosswalk, in a way that is robust to
// any prior package having left the catalog partial. It is idempotent and
// order-independent:
//
//   - If the catalog is already complete (sentinel resolves), the SCF reseed
//     is a no-op and the SOC 2 crosswalk is re-imported (itself a
//     content-equality-aware no-op, and cheap). The crosswalk re-import also
//     restores the SOC 2 rows if an earlier package wiped only those.
//   - If the catalog is absent or partial, it performs a full clean reseed
//     of the SCF sample, then imports the SOC 2 crosswalk.
//
// pool MUST be the admin pool (BYPASSRLS) — seeding writes platform-layer,
// tenant-NULL rows. Returns an error rather than calling t.Fatal so callers
// keep control of failure reporting; the thin testing-aware wrapper lives in
// the consuming test files.
func EnsureFullCatalog(ctx context.Context, pool *pgxpool.Pool) error {
	if err := EnsureSCFCatalog(ctx, pool); err != nil {
		return err
	}

	// Always (re)import the SOC 2 crosswalk. After a fresh SCF reseed the
	// crosswalk must be (re)built against the new anchor IDs; on the
	// already-complete path the import is a content-equality no-op. This
	// also restores the crosswalk if an earlier package wiped only the
	// SOC 2 rows (the common cleanup other packages do).
	cw, err := soc2import.Load(soc2CWPath)
	if err != nil {
		return fmt.Errorf("scfseed: load SOC 2 crosswalk: %w", err)
	}
	if _, err := soc2import.Import(ctx, pool, cw); err != nil {
		return fmt.Errorf("scfseed: import SOC 2 crosswalk: %w", err)
	}
	return nil
}

// fullReseedSCF wipes the SCF + SOC 2 platform-layer rows and reimports the
// SCF sample. It only fires on the catalog-incomplete branch — i.e. when the
// catalog is absent OR a prior package left it partial. In that case the
// reseed assigns brand-new anchor IDs, so any controls.scf_anchor_id FK
// pointing at the old anchors is stale by definition. Because
// controls.scf_anchor_id is `ON DELETE RESTRICT`, a leftover controls row
// from an earlier package (the alphabetical full-wildcard run puts
// internal/api/controls* before scfimport/scfseed) would otherwise block the
// `DELETE FROM scf_anchors`. We therefore TRUNCATE controls CASCADE first —
// the same hammer ucfcoverage's wipeTenantControls uses, for the same reason.
// This is safe: every package that needs controls seeds its own per-test, and
// a reseed only happens when the shared catalog was already broken.
func fullReseedSCF(ctx context.Context, pool *pgxpool.Pool) error {
	// FK order: controls (CASCADE, clears the RESTRICT) -> edges ->
	// requirements -> anchors -> framework_versions -> frameworks.
	// fw_to_scf_edges references framework_requirements + scf_anchors;
	// framework_requirements + scf_anchors reference framework_versions;
	// framework_versions references frameworks.
	stmts := []string{
		`TRUNCATE controls RESTART IDENTITY CASCADE`,
		`DELETE FROM fw_to_scf_edges`,
		`DELETE FROM framework_requirements`,
		`DELETE FROM scf_anchors`,
		`DELETE FROM framework_versions WHERE framework_id IN (
			SELECT id FROM frameworks WHERE slug IN ('scf', 'soc2') AND tenant_id IS NULL
		)`,
		`DELETE FROM frameworks WHERE slug IN ('scf', 'soc2') AND tenant_id IS NULL`,
	}
	for _, stmt := range stmts {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("scfseed: reseed wipe %q: %w", stmt, err)
		}
	}

	cat, err := scfimport.Load(scfSamplePath)
	if err != nil {
		return fmt.Errorf("scfseed: load SCF sample: %w", err)
	}
	if _, err := scfimport.Import(ctx, pool, cat); err != nil {
		return fmt.Errorf("scfseed: import SCF sample: %w", err)
	}
	return nil
}
