//go:build integration

// Slice 461 regression suite. These tests are the load-bearing proof that
// the SCF-catalog seed guard is order-independent: a partial-DELETE that
// leaves the current SCF framework version incomplete (the original failure
// shape) must trigger a full reseed rather than being mistaken for
// "already seeded."
package scfseed_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/scfseed"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

func adminDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	return dsn
}

func openAdminPool(t *testing.T) *pgxpool.Pool {
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

// sentinelResolves mirrors the exact query the SOC 2 importer uses, so the
// assertion tracks production resolution semantics, not a looser row count.
func sentinelResolves(t *testing.T, pool *pgxpool.Pool) bool {
	t.Helper()
	_, err := dbx.New(pool).GetSCFAnchorBySCFID(context.Background(), scfseed.SentinelAnchor)
	if err == nil {
		return true
	}
	if err == pgx.ErrNoRows {
		return false
	}
	t.Fatalf("probe sentinel: %v", err)
	return false
}

// TestEnsureFullCatalog_SeedsFromEmpty verifies the cold-start path: an empty
// catalog gets fully seeded and the sentinel resolves afterwards.
func TestEnsureFullCatalog_SeedsFromEmpty(t *testing.T) {
	pool := openAdminPool(t)
	ctx := context.Background()

	// Wipe to a clean platform-layer state.
	wipeCatalog(t, pool)
	if sentinelResolves(t, pool) {
		t.Fatal("precondition: sentinel must not resolve on an empty catalog")
	}

	if err := scfseed.EnsureFullCatalog(ctx, pool); err != nil {
		t.Fatalf("EnsureFullCatalog from empty: %v", err)
	}
	if !sentinelResolves(t, pool) {
		t.Fatal("sentinel must resolve after seeding from empty")
	}
}

// TestEnsureFullCatalog_SelfCorrectsAfterPartialDelete is the core slice 461
// regression. It reproduces the failure shape: a non-empty catalog whose
// CURRENT SCF version is missing the sentinel anchor (a partial-DELETE
// leftover). The old `if anchorCount == 0` guard would see rows present and
// skip reseed; the completeness-aware guard must detect the gap and reseed.
func TestEnsureFullCatalog_SelfCorrectsAfterPartialDelete(t *testing.T) {
	pool := openAdminPool(t)
	ctx := context.Background()

	// Start fully seeded.
	wipeCatalog(t, pool)
	if err := scfseed.EnsureFullCatalog(ctx, pool); err != nil {
		t.Fatalf("initial seed: %v", err)
	}
	if !sentinelResolves(t, pool) {
		t.Fatal("precondition: sentinel must resolve after initial seed")
	}

	// Simulate the partial-DELETE leftover: remove ONLY the sentinel anchor
	// from the current SCF version, leaving a non-empty subset behind. This
	// is exactly the state that produced `scf_anchor "GOV-01" not found`.
	if _, err := pool.Exec(ctx, `
		DELETE FROM scf_anchors
		WHERE scf_id = $1
		  AND framework_version_id IN (
			SELECT fv.id FROM framework_versions fv
			JOIN frameworks f ON f.id = fv.framework_id
			WHERE f.slug = 'scf' AND fv.status = 'current' AND f.tenant_id IS NULL
		)
	`, scfseed.SentinelAnchor); err != nil {
		t.Fatalf("simulate partial delete: %v", err)
	}

	// Confirm we are in the broken state: rows exist, but the sentinel is
	// gone — precisely the condition the old guard mishandled.
	var remaining int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM scf_anchors`).Scan(&remaining); err != nil {
		t.Fatalf("count remaining: %v", err)
	}
	if remaining == 0 {
		t.Fatal("precondition: catalog must be non-empty (partial), not empty")
	}
	if sentinelResolves(t, pool) {
		t.Fatal("precondition: sentinel must NOT resolve after partial delete")
	}

	// The fix: EnsureFullCatalog must self-correct.
	if err := scfseed.EnsureFullCatalog(ctx, pool); err != nil {
		t.Fatalf("EnsureFullCatalog after partial delete: %v", err)
	}
	if !sentinelResolves(t, pool) {
		t.Fatal("sentinel must resolve after self-correcting reseed (slice 461 regression)")
	}
}

// TestEnsureFullCatalog_IdempotentOnFullCatalog verifies the hot path is a
// safe no-op: calling twice on a fully-seeded catalog does not error and the
// sentinel still resolves.
func TestEnsureFullCatalog_IdempotentOnFullCatalog(t *testing.T) {
	pool := openAdminPool(t)
	ctx := context.Background()

	wipeCatalog(t, pool)
	if err := scfseed.EnsureFullCatalog(ctx, pool); err != nil {
		t.Fatalf("first EnsureFullCatalog: %v", err)
	}
	if err := scfseed.EnsureFullCatalog(ctx, pool); err != nil {
		t.Fatalf("second EnsureFullCatalog (idempotent path): %v", err)
	}
	if !sentinelResolves(t, pool) {
		t.Fatal("sentinel must resolve after idempotent re-seed")
	}
}

// TestIsCatalogComplete_TracksCurrentVersionOnly verifies the completeness
// probe keys on the CURRENT SCF version specifically — a sentinel that lives
// only in a legacy version must read as incomplete (this is the demotion
// scenario that TestImport_NewReleaseCreatesNewFrameworkVersion produces).
func TestIsCatalogComplete_TracksCurrentVersionOnly(t *testing.T) {
	pool := openAdminPool(t)
	ctx := context.Background()

	wipeCatalog(t, pool)
	if err := scfseed.EnsureFullCatalog(ctx, pool); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ok, err := scfseed.IsCatalogComplete(ctx, pool)
	if err != nil {
		t.Fatalf("IsCatalogComplete on full catalog: %v", err)
	}
	if !ok {
		t.Fatal("full catalog must read as complete")
	}

	// Demote the current SCF version to legacy without touching anchors.
	// The sentinel still exists in the table, but no longer in any current
	// version — so the completeness probe must read false.
	if _, err := pool.Exec(ctx, `
		UPDATE framework_versions SET status = 'legacy'
		WHERE framework_id IN (
			SELECT id FROM frameworks WHERE slug = 'scf' AND tenant_id IS NULL
		) AND status = 'current'
	`); err != nil {
		t.Fatalf("demote current version: %v", err)
	}
	ok, err = scfseed.IsCatalogComplete(ctx, pool)
	if err != nil {
		t.Fatalf("IsCatalogComplete after demotion: %v", err)
	}
	if ok {
		t.Fatal("catalog with no current version must read as incomplete")
	}
}

// TestEnsureFullCatalog_ReseedAuditsOverlayEdges is the #2481 regression: a
// reseed deletes every overlay edge with the catalog, and each one, active
// or retired, leaves a cascade_removed audit row in its own tenant under the
// reseed actor.
func TestEnsureFullCatalog_ReseedAuditsOverlayEdges(t *testing.T) {
	pool := openAdminPool(t)
	ctx := context.Background()

	wipeCatalog(t, pool)
	if err := scfseed.EnsureFullCatalog(ctx, pool); err != nil {
		t.Fatalf("initial seed: %v", err)
	}

	tenant := dbtest.SeedTenant(t, pool, "fw_to_scf_overlay_edges")
	var reqID, anchorID string
	if err := pool.QueryRow(ctx, `
		SELECT fr.id::text, a.id::text
		FROM framework_requirements fr
		JOIN framework_versions fv ON fv.id = fr.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id AND f.slug = 'soc2' AND f.tenant_id IS NULL
		CROSS JOIN LATERAL (
			SELECT a.id FROM scf_anchors a
			JOIN framework_versions av ON av.id = a.framework_version_id
			JOIN frameworks sf ON sf.id = av.framework_id AND sf.slug = 'scf' AND sf.tenant_id IS NULL
			WHERE av.status = 'current' AND a.scf_id <> $1
			ORDER BY a.scf_id LIMIT 1
		) a
		ORDER BY fr.code LIMIT 1`, scfseed.SentinelAnchor).Scan(&reqID, &anchorID); err != nil {
		t.Fatalf("pick requirement and anchor: %v", err)
	}
	edges := map[string]string{} // edge id -> status
	for _, stmt := range []string{
		`INSERT INTO fw_to_scf_overlay_edges (tenant_id, framework_requirement_id, scf_anchor_id,
			relationship_type, strength, rationale, source_ref, created_by)
		VALUES ($1, $2, $3, 'equal', 1.0, 'reseed test', 'reseed test', 'test')
		RETURNING id::text, status`,
		`INSERT INTO fw_to_scf_overlay_edges (tenant_id, framework_requirement_id, scf_anchor_id,
			relationship_type, strength, rationale, source_ref, created_by,
			status, retirement_reason, retired_by, retired_at)
		VALUES ($1, $2, $3, 'subset_of', 0.5, 'reseed test', 'reseed test', 'test',
			'retired', 'superseded', 'test', now())
		RETURNING id::text, status`,
	} {
		var id, status string
		if err := pool.QueryRow(ctx, stmt, tenant, reqID, anchorID).Scan(&id, &status); err != nil {
			t.Fatalf("seed overlay edge: %v", err)
		}
		edges[id] = status
	}

	// Leave the catalog partial so EnsureFullCatalog takes the reseed path.
	if _, err := pool.Exec(ctx, `
		DELETE FROM scf_anchors
		WHERE scf_id = $1
		  AND framework_version_id IN (
			SELECT fv.id FROM framework_versions fv
			JOIN frameworks f ON f.id = fv.framework_id
			WHERE f.slug = 'scf' AND fv.status = 'current' AND f.tenant_id IS NULL
		)
	`, scfseed.SentinelAnchor); err != nil {
		t.Fatalf("simulate partial delete: %v", err)
	}
	if err := scfseed.EnsureFullCatalog(ctx, pool); err != nil {
		t.Fatalf("EnsureFullCatalog reseed with overlay edges present: %v", err)
	}

	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM fw_to_scf_overlay_edges WHERE tenant_id = $1`, tenant).Scan(&left); err != nil || left != 0 {
		t.Fatalf("overlay edges after reseed: %d, err %v; want 0", left, err)
	}

	rows, err := pool.Query(ctx, `
		SELECT overlay_edge_id::text, actor, before->>'status', before->>'anchor_code', after->>'removed_with'
		FROM fw_to_scf_overlay_edge_audit_log
		WHERE tenant_id = $1 AND action = 'cascade_removed'`, tenant)
	if err != nil {
		t.Fatalf("read audit rows: %v", err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var id, actor, status, anchor, removedWith string
		if err := rows.Scan(&id, &actor, &status, &anchor, &removedWith); err != nil {
			t.Fatalf("scan audit row: %v", err)
		}
		want, ok := edges[id]
		if !ok {
			t.Errorf("cascade_removed row for unknown edge %s", id)
			continue
		}
		seen++
		if actor != scfseed.ReseedOverlayActor || status != want || anchor == "" || removedWith != "catalog_reseed" {
			t.Errorf("edge %s audit: actor %q, before status %q (want %q), anchor %q, removed_with %q",
				id, actor, status, want, anchor, removedWith)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("audit rows: %v", err)
	}
	if seen != len(edges) {
		t.Errorf("cascade_removed rows for the tenant: %d; want %d (one per edge)", seen, len(edges))
	}
}

// wipeCatalog clears the SCF + SOC 2 platform-layer rows so each test starts
// from a known clean state. It TRUNCATEs controls CASCADE first because under
// the full alphabetical wildcard run an earlier internal/api/controls* package
// leaves controls rows whose `scf_anchor_id` FK (`ON DELETE RESTRICT`) would
// block the `DELETE FROM scf_anchors`. Same reason + hammer as scfseed's
// fullReseedSCF and ucfcoverage's wipeTenantControls.
func wipeCatalog(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
		`TRUNCATE controls RESTART IDENTITY CASCADE`,
		`DELETE FROM fw_to_scf_edges`,
		`DELETE FROM framework_requirements`,
		`DELETE FROM scf_anchors`,
		`DELETE FROM framework_versions WHERE framework_id IN (
			SELECT id FROM frameworks WHERE slug IN ('scf', 'soc2') AND tenant_id IS NULL
		)`,
		`DELETE FROM frameworks WHERE slug IN ('scf', 'soc2') AND tenant_id IS NULL`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("wipe %q: %v", stmt, err)
		}
	}
}
