//go:build integration

package soc2import_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/scfseed"
	"github.com/mgoodric/security-atlas/internal/api/soc2import"
)

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

// resetCatalog wipes every slice-007-owned catalog table so each test
// starts with a clean SOC 2 mapping graph. We DO NOT wipe scf_anchors or
// frameworks/framework_versions: those are owned by slice 006's test
// helper, and other packages in the full integration suite leave
// dependent rows (e.g., controls with controls.scf_anchor_id FK) that
// would block a global wipe. Wiping just the slice-007 tables is
// sufficient because the slice-007 importer re-creates its own framework
// + framework_version rows idempotently.
func resetCatalog(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
		"DELETE FROM fw_to_scf_edges",
		"DELETE FROM framework_requirements",
		// Slice-007 framework_versions row (slug='soc2') — re-imports
		// upsert it, but stale `current` status from a prior test run
		// would break the at-most-one-current invariant if a different
		// version landed in between. Deleting just the SOC2 framework
		// avoids touching the SCF catalog rows.
		"DELETE FROM framework_versions WHERE framework_id IN (SELECT id FROM frameworks WHERE slug = 'soc2' AND tenant_id IS NULL)",
		"DELETE FROM frameworks WHERE slug = 'soc2' AND tenant_id IS NULL",
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("cleanup %q: %v", stmt, err)
		}
	}
}

// ensureSCFLoaded ensures the CURRENT SCF framework version holds the full
// sample catalog before each crosswalk-import test. It delegates to the
// shared slice 461 helper, which is order-independent: a prior package that
// left the SCF catalog PARTIAL (e.g. current version missing GOV-01) would
// have tripped the old `if n > 0 { return }` guard into skipping the reseed,
// and the crosswalk import under test would then fail resolving GOV-01. The
// completeness-aware helper reseeds the SCF catalog when it is absent OR
// partial. It deliberately does NOT import the SOC 2 crosswalk — that is the
// unit under test here, so the test owns it via loadCrosswalk + Import.
func ensureSCFLoaded(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if err := scfseed.EnsureSCFCatalog(context.Background(), pool); err != nil {
		t.Fatalf("scfseed.EnsureSCFCatalog: %v", err)
	}
}

func loadCrosswalk(t *testing.T) *soc2import.Crosswalk {
	t.Helper()
	cw, err := soc2import.Load(filepath.Join("..", "..", "..", "data", "crosswalks", "soc2-tsc-2017.yaml"))
	if err != nil {
		t.Fatalf("soc2import.Load: %v", err)
	}
	return cw
}

// AC-1 + ISC-15 — first import creates rows and reports them; second
// import with the same crosswalk is a no-op (idempotent).
func TestImport_FirstRunCreatesRequirementsAndEdges(t *testing.T) {
	pool := openPool(t)
	resetCatalog(t, pool)
	ensureSCFLoaded(t, pool)
	cw := loadCrosswalk(t)

	report, err := soc2import.Import(context.Background(), pool, cw)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if !report.IsNewVersion {
		t.Fatal("first import should be a new version")
	}
	if report.RequirementsCreated != len(cw.Requirements) {
		t.Fatalf("requirements created = %d; want %d", report.RequirementsCreated, len(cw.Requirements))
	}
	if report.EdgesCreated != len(cw.Mappings) {
		t.Fatalf("edges created = %d; want %d", report.EdgesCreated, len(cw.Mappings))
	}
	if report.EdgesUpdated+report.EdgesUnchanged != 0 {
		t.Fatalf("first import has updates/unchanged: %+v", report)
	}
}

func TestImport_SecondRunSameCrosswalkIsIdempotent(t *testing.T) {
	pool := openPool(t)
	resetCatalog(t, pool)
	ensureSCFLoaded(t, pool)
	cw := loadCrosswalk(t)

	if _, err := soc2import.Import(context.Background(), pool, cw); err != nil {
		t.Fatalf("first Import: %v", err)
	}
	report, err := soc2import.Import(context.Background(), pool, cw)
	if err != nil {
		t.Fatalf("second Import: %v", err)
	}
	if report.RequirementsCreated != 0 || report.EdgesCreated != 0 {
		t.Fatalf("idempotent re-import created rows: %+v", report)
	}
	if report.RequirementsUnchanged != len(cw.Requirements) {
		t.Fatalf("requirements unchanged = %d; want %d", report.RequirementsUnchanged, len(cw.Requirements))
	}
	if report.EdgesUnchanged != len(cw.Mappings) {
		t.Fatalf("edges unchanged = %d; want %d", report.EdgesUnchanged, len(cw.Mappings))
	}
}

// AC-6 + ISC-19 + ISC-29 — every drafted row carries source_attribution,
// and the agent-authored crosswalk uses 'community_draft' on every row
// pending HITL approval.
func TestImport_EveryDraftedEdgeIsCommunityDraft(t *testing.T) {
	pool := openPool(t)
	resetCatalog(t, pool)
	ensureSCFLoaded(t, pool)
	cw := loadCrosswalk(t)

	if _, err := soc2import.Import(context.Background(), pool, cw); err != nil {
		t.Fatalf("Import: %v", err)
	}

	var totalCount, communityDraftCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM fw_to_scf_edges`).Scan(&totalCount); err != nil {
		t.Fatalf("total count: %v", err)
	}
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM fw_to_scf_edges WHERE source_attribution = 'community_draft'`).Scan(&communityDraftCount); err != nil {
		t.Fatalf("community_draft count: %v", err)
	}
	if totalCount != len(cw.Mappings) {
		t.Fatalf("total edges = %d; want %d", totalCount, len(cw.Mappings))
	}
	if communityDraftCount != totalCount {
		t.Fatalf("community_draft edges = %d; want %d (every drafted row is community_draft pending HITL)",
			communityDraftCount, totalCount)
	}
}

// AC-2 — every edge has a non-empty relationship_type ∈ {equal, subset_of,
// superset_of, intersects_with, no_relationship} and strength ∈ [0.0, 1.0].
// The DB CHECK and ENUM enforce this; the test confirms the importer
// faithfully writes the values from the crosswalk.
func TestImport_EveryEdgeHasValidSTRMTypeAndStrength(t *testing.T) {
	pool := openPool(t)
	resetCatalog(t, pool)
	ensureSCFLoaded(t, pool)
	cw := loadCrosswalk(t)

	if _, err := soc2import.Import(context.Background(), pool, cw); err != nil {
		t.Fatalf("Import: %v", err)
	}

	var outOfRange int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM fw_to_scf_edges WHERE strength < 0.0 OR strength > 1.0`).Scan(&outOfRange); err != nil {
		t.Fatalf("strength range query: %v", err)
	}
	if outOfRange != 0 {
		t.Fatalf("%d edges have out-of-range strength — DB CHECK should have rejected them", outOfRange)
	}

	rows, err := pool.Query(context.Background(),
		`SELECT relationship_type::text, count(*) FROM fw_to_scf_edges GROUP BY relationship_type`)
	if err != nil {
		t.Fatalf("strm distribution query: %v", err)
	}
	defer rows.Close()
	allowed := map[string]bool{
		"equal": true, "subset_of": true, "superset_of": true,
		"intersects_with": true, "no_relationship": true,
	}
	for rows.Next() {
		var rt string
		var n int
		if err := rows.Scan(&rt, &n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if !allowed[rt] {
			t.Fatalf("unexpected relationship_type %q (%d rows)", rt, n)
		}
	}
}

// Invariant 1 — the DB has NO requirement-to-requirement edge table.
// information_schema is the source of truth for "what tables exist."
// If a future schema slice adds a fw_to_fw_edges table, this test fails
// and the constitutional violation is caught at CI time.
func TestImport_NoDirectRequirementToRequirementTableExists(t *testing.T) {
	pool := openPool(t)
	resetCatalog(t, pool)

	var n int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM information_schema.tables
		WHERE table_schema = 'public'
		  AND table_name LIKE '%req%req%'`).Scan(&n); err != nil {
		t.Fatalf("information_schema query: %v", err)
	}
	if n > 0 {
		t.Fatalf("invariant 1 violated: %d req-to-req table(s) exist; all framework-to-framework relationships must traverse SCF anchors", n)
	}

	// And no second CROSSWALK-style bridge that references
	// framework_requirements. The invariant the guard protects is "no
	// framework-to-framework MAPPING that bypasses SCF anchors" — i.e. no
	// second catalog/crosswalk edge into framework_requirements beyond the
	// one legitimate fw_to_scf_edges.framework_requirement_id.
	//
	// Refinement (slice 515, decisions-log D7): the original heuristic
	// counted EVERY FK into framework_requirements and allowed exactly 1.
	// That was correct only while the crosswalk edge was the sole
	// referencer. It is over-broad: a TENANT-SCOPED assessment-state table
	// may legitimately reference the shared Subcategory row for referential
	// integrity WITHOUT being a crosswalk (slice 515's
	// csf_profile_selections records a tenant's per-Subcategory target
	// outcome — assessment-state -> reference-data, not requirement ->
	// requirement mapping, and it never bypasses the crosswalk). The
	// distinguishing property: a crosswalk/edge table is CATALOG data (no
	// tenant_id — shared reference data, like fw_to_scf_edges), whereas
	// assessment-state is TENANT-SCOPED (carries tenant_id). So the count is
	// restricted to FKs ORIGINATING from catalog tables (no tenant_id
	// column). A genuine fw_to_fw_edges crosswalk would be catalog data (no
	// tenant_id, exactly like fw_to_scf_edges), so it is still counted and
	// still caught here — AND independently by the %req%req%-name check
	// above. A tenant-scoped table cannot launder a crosswalk through this
	// exclusion because a crosswalk is shared (tenant-agnostic) catalog data
	// by construction (invariant #1/#3.5).
	var refCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM information_schema.key_column_usage kcu
		JOIN information_schema.referential_constraints rc ON rc.constraint_name = kcu.constraint_name
		JOIN information_schema.constraint_column_usage ccu ON ccu.constraint_name = kcu.constraint_name
		WHERE kcu.table_schema = 'public'
		  AND ccu.table_name = 'framework_requirements'
		  AND NOT EXISTS (
		      SELECT 1
		      FROM information_schema.columns c
		      WHERE c.table_schema = 'public'
		        AND c.table_name = kcu.table_name
		        AND c.column_name = 'tenant_id'
		  )`).Scan(&refCount); err != nil {
		t.Fatalf("fk inspection query: %v", err)
	}
	if refCount > 1 {
		// Allowed exactly one CATALOG referencer:
		// fw_to_scf_edges.framework_requirement_id. A second catalog-table
		// FK into framework_requirements is a framework-to-framework
		// crosswalk that bypasses SCF anchors — the constitutional
		// violation. (Tenant-scoped assessment-state FKs are excluded by the
		// tenant_id filter and are NOT crosswalks; see the comment above.)
		t.Fatalf("invariant 1 violated: %d catalog FKs point at framework_requirements; expected exactly 1 (fw_to_scf_edges.framework_requirement_id) — a second crosswalk bridge bypasses SCF anchors", refCount)
	}

	// Positive guard: prove the refinement still catches a real
	// requirement->requirement crosswalk. We create a CATALOG (no tenant_id)
	// edge table that references framework_requirements a second time, in a
	// rolled-back transaction, and assert the count query trips to > 1. This
	// keeps the guard honest — it must fail on a genuine second crosswalk,
	// not merely tolerate the one we know about. The tx is always rolled
	// back so no schema change persists.
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(context.Background(), `
		CREATE TABLE fw_to_fw_edges_probe (
			id UUID PRIMARY KEY,
			src_requirement_id UUID NOT NULL REFERENCES framework_requirements(id),
			dst_requirement_id UUID NOT NULL REFERENCES framework_requirements(id)
		)`); err != nil {
		t.Fatalf("create probe crosswalk table: %v", err)
	}
	var probeCount int
	if err := tx.QueryRow(context.Background(), `
		SELECT count(*)
		FROM information_schema.key_column_usage kcu
		JOIN information_schema.referential_constraints rc ON rc.constraint_name = kcu.constraint_name
		JOIN information_schema.constraint_column_usage ccu ON ccu.constraint_name = kcu.constraint_name
		WHERE kcu.table_schema = 'public'
		  AND ccu.table_name = 'framework_requirements'
		  AND NOT EXISTS (
		      SELECT 1
		      FROM information_schema.columns c
		      WHERE c.table_schema = 'public'
		        AND c.table_name = kcu.table_name
		        AND c.column_name = 'tenant_id'
		  )`).Scan(&probeCount); err != nil {
		t.Fatalf("probe fk inspection query: %v", err)
	}
	if probeCount <= 1 {
		t.Fatalf("refined guard is broken: a genuine catalog req->req crosswalk probe yielded count=%d, want >1 (the guard would NOT catch a real crosswalk)", probeCount)
	}
}

// AC-2 — strength=1.4 violates the DB CHECK constraint. Belt-and-
// suspenders proof that even if the loader is bypassed, Postgres
// refuses to store an out-of-range edge.
func TestSchema_StrengthCheckConstraintRejectsOutOfRange(t *testing.T) {
	pool := openPool(t)
	resetCatalog(t, pool)
	ensureSCFLoaded(t, pool)
	cw := loadCrosswalk(t)
	if _, err := soc2import.Import(context.Background(), pool, cw); err != nil {
		t.Fatalf("Import: %v", err)
	}

	// Pick an existing (requirement, anchor) pair to force a STRENGTH
	// violation on. The UNIQUE constraint would fire first if we tried
	// to INSERT, so we UPDATE.
	_, err := pool.Exec(context.Background(),
		`UPDATE fw_to_scf_edges SET strength = 1.4 WHERE strength = (SELECT max(strength) FROM fw_to_scf_edges)`)
	if err == nil {
		t.Fatal("expected CHECK constraint to reject strength = 1.4")
	}
}
