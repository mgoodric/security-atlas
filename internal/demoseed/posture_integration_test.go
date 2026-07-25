//go:build integration

// Integration tests for the slice 682 framework-posture spine.
//
// Requires Postgres reachable via DATABASE_URL (BYPASSRLS migrate-role
// DSN — the same adminPool the slice-205 harness opens in TestMain).
//
// Run with:
//
//	go test -tags=integration -p 1 ./internal/demoseed/...
//
// The load-bearing test (TestApply_FrameworkPostureRendersCoverage)
// REPRODUCES the reported symptom: before slice 682, a seeded demo tenant's
// dashboard "Framework posture" tiles render "No active framework versions
// yet" because the FrameworkPosture query returns ZERO rows — the SCF-anchor
// coverage spine (constitutional invariant #1) is broken in the seed:
//
//	(1) demo controls never set scf_anchor_id (the FK the posture query joins
//	    on), so the covering_control CTE finds zero controls; and
//	(2) the demo framework version has no framework_requirements and no
//	    fw_to_scf_edges (STRM edges), so version_reqs / req_anchor are empty.
//
// This test asserts the spine is whole: FrameworkPosture returns >=1 row with
// coverage_pct > 0 for the seeded tenant, and an idempotent re-seed does not
// duplicate the STRM edges (AC-5).
//
// Test fixtures use only neutral `demo-*` slugs (P0-A7).

package demoseed_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/demoseed"
)

// pgUUID boxes a uuid.UUID into the pgtype.UUID the generated FrameworkPosture
// params expect.
func pgUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

// frameworkPosture runs the production FrameworkPosture read query through the
// BYPASSRLS admin pool. The query's covering_control CTE carries an explicit
// c.tenant_id = $1 predicate, so it scopes correctly without a GUC.
func frameworkPosture(t *testing.T, tenantID uuid.UUID) []dbx.FrameworkPostureRow {
	t.Helper()
	q := dbx.New(adminPool)
	rows, err := q.FrameworkPosture(context.Background(), dbx.FrameworkPostureParams{
		TenantID:    pgUUID(tenantID),
		EvaluatedAt: pgtype.Timestamptz{Time: time.Now().AddDate(0, 0, -90), Valid: true},
	})
	if err != nil {
		t.Fatalf("FrameworkPosture: %v", err)
	}
	return rows
}

// edgeCount returns the number of fw_to_scf_edges rows reachable from the
// tenant's current framework_versions (the demo spine). Catalog tables carry
// no tenant_id, so we scope through the tenant's own framework_versions rows.
func edgeCount(t *testing.T, tenantID uuid.UUID) int {
	t.Helper()
	var n int
	err := adminPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM fw_to_scf_edges e
		JOIN framework_requirements fr ON fr.id = e.framework_requirement_id
		JOIN framework_versions fv ON fv.id = fr.framework_version_id
		WHERE fv.tenant_id = $1`,
		tenantID,
	).Scan(&n)
	if err != nil {
		t.Fatalf("edgeCount: %v", err)
	}
	return n
}

// controlsAnchored returns the count of the tenant's controls carrying a
// non-NULL scf_anchor_id (the FK the posture query's covering_control CTE
// joins on). Before slice 682 this is 0.
func controlsAnchored(t *testing.T, tenantID uuid.UUID) int {
	t.Helper()
	var n int
	err := adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM controls WHERE tenant_id = $1 AND scf_anchor_id IS NOT NULL`,
		tenantID,
	).Scan(&n)
	if err != nil {
		t.Fatalf("controlsAnchored: %v", err)
	}
	return n
}

// TestApply_FrameworkPostureRendersCoverage is the slice 682 load-bearing
// test. AC-1, AC-2, AC-3: a seeded demo tenant's controls carry resolvable
// scf_anchor_id values, the demo framework version carries requirements +
// STRM edges, and FrameworkPosture returns >=1 row with coverage_pct > 0.
func TestApply_FrameworkPostureRendersCoverage(t *testing.T) {
	const slug = "demo-it-posture"
	cleanupTenant(t, slug)

	seeder, err := demoseed.NewSeeder(adminPool, demoseed.DefaultScale)
	if err != nil {
		t.Fatalf("NewSeeder: %v", err)
	}
	res, err := seeder.Apply(context.Background(), demoseed.ApplyInput{Slug: slug})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Idempotent {
		t.Fatal("expected first apply to write rows; got idempotent")
	}

	// AC-1: demo controls are anchored to real scf_anchors rows.
	if got := controlsAnchored(t, res.TenantID); got == 0 {
		t.Fatalf("AC-1: 0 controls carry scf_anchor_id; the covering_control CTE will find nothing")
	}

	// AC-2: the demo framework version carries STRM edges.
	if got := edgeCount(t, res.TenantID); got == 0 {
		t.Fatalf("AC-2: 0 fw_to_scf_edges for the demo framework; version_reqs/req_anchor are empty")
	}

	// AC-3: the production FrameworkPosture query renders >=1 coverage tile
	// with a real, non-zero coverage_pct for the seeded tenant.
	rows := frameworkPosture(t, res.TenantID)
	if len(rows) == 0 {
		t.Fatalf("AC-3: FrameworkPosture returned 0 rows; dashboard shows 'No active framework versions yet'")
	}
	// coverage_pct is reported on a 0..100 scale (dashboard.sql ROUND(100.0 *
	// covered / total, 2)).
	var anyCovered bool
	for _, r := range rows {
		if r.CoveragePct < 0 || r.CoveragePct > 100 {
			t.Errorf("coverage_pct out of [0,100]: %v", r.CoveragePct)
		}
		if r.CoveragePct > 0 {
			anyCovered = true
		}
	}
	if !anyCovered {
		t.Fatalf("AC-3: every posture row has coverage_pct = 0; coverage must be REAL (>0)")
	}

	// The demo seed deliberately leaves a couple of requirements uncovered so
	// the tile is honest (coverage < 100%, not a flat green wall). Assert at
	// least one demo posture row is strictly between 0 and 100.
	var realistic bool
	for _, r := range rows {
		if r.CoveragePct > 0 && r.CoveragePct < 100 {
			realistic = true
		}
	}
	if !realistic {
		t.Errorf("AC-3: no posture row has a realistic partial coverage (0 < pct < 100); got %+v", rows)
	}
}

// TestApply_PostureSpineIdempotent is AC-5: an idempotent re-seed (same slug,
// after teardown + re-apply) does not duplicate STRM edges. A second apply on
// a fresh teardown re-creates the same spine cardinality; the
// (framework_requirement_id, scf_anchor_id) UNIQUE on fw_to_scf_edges and the
// (framework_version_id, scf_id) UNIQUE on scf_anchors are the invariants the
// writer must respect.
func TestApply_PostureSpineIdempotent(t *testing.T) {
	const slug = "demo-it-posture-idem"
	cleanupTenant(t, slug)

	seeder, err := demoseed.NewSeeder(adminPool, demoseed.DefaultScale)
	if err != nil {
		t.Fatalf("NewSeeder: %v", err)
	}
	ctx := context.Background()

	res1, err := seeder.Apply(ctx, demoseed.ApplyInput{Slug: slug})
	if err != nil {
		t.Fatalf("Apply #1: %v", err)
	}
	edges1 := edgeCount(t, res1.TenantID)
	if edges1 == 0 {
		t.Fatalf("first apply produced 0 STRM edges")
	}

	// Teardown sweeps the tenant-scoped demo spine; a re-apply re-creates it
	// from scratch with the same cardinality (no duplication).
	if err := seeder.Teardown(ctx, slug, uuid.Nil, uuid.Nil); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	res2, err := seeder.Apply(ctx, demoseed.ApplyInput{Slug: slug})
	if err != nil {
		t.Fatalf("Apply #2: %v", err)
	}
	edges2 := edgeCount(t, res2.TenantID)
	if edges2 != edges1 {
		t.Errorf("AC-5: re-seed changed STRM edge count %d -> %d; spine is not idempotent", edges1, edges2)
	}
}
