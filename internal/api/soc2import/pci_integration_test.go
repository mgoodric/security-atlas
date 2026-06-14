//go:build integration

package soc2import_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/soc2import"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// Slice 447 integration suite — the FRAMEWORK-AGNOSTIC crosswalk importer
// (slice 438) proven against a real Postgres with a THIRD framework
// (PCI DSS v4.0). This extends the slice-438 invariant-#1 proof from two
// frameworks to three: a single shared SCF anchor (IAC-01) resolves to a
// SOC 2 criterion, an ISO Annex A control, AND a PCI requirement through that
// ONE anchor row — one control, N framework satisfactions, no per-framework
// duplication and no requirement -> requirement edge (invariants #1 + #7).

func loadPCICrosswalk(t *testing.T) *soc2import.Crosswalk {
	t.Helper()
	cw, err := soc2import.Load(filepath.Join("..", "..", "..", "data", "crosswalks", "pci-dss-4.0.yaml"))
	if err != nil {
		t.Fatalf("soc2import.Load(pci-dss-4.0.yaml): %v", err)
	}
	return cw
}

// resetPCI wipes the PCI framework rows (mirrors resetISO's ISO wipe) so each
// test starts clean. Catalog tables are not tenant-scoped, so this is a plain
// DELETE; fw_to_scf_edges + framework_requirements cascade off
// framework_versions.
func resetPCI(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
		`DELETE FROM fw_to_scf_edges WHERE framework_requirement_id IN (
			SELECT fr.id FROM framework_requirements fr
			JOIN framework_versions fv ON fv.id = fr.framework_version_id
			JOIN frameworks f ON f.id = fv.framework_id
			WHERE f.slug = 'pcidss')`,
		`DELETE FROM framework_requirements WHERE framework_version_id IN (
			SELECT fv.id FROM framework_versions fv
			JOIN frameworks f ON f.id = fv.framework_id
			WHERE f.slug = 'pcidss')`,
		`DELETE FROM framework_versions WHERE framework_id IN (
			SELECT id FROM frameworks WHERE slug = 'pcidss' AND tenant_id IS NULL)`,
		`DELETE FROM frameworks WHERE slug = 'pcidss' AND tenant_id IS NULL`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("resetPCI %q: %v", stmt, err)
		}
	}
}

// AC-2 + AC-7 — importing the PCI crosswalk creates framework_requirements +
// fw_to_scf_edges rows for PCI DSS v4.0, and re-importing is idempotent.
func TestPCIImport_CreatesRowsAndIsIdempotent(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	resetPCI(t, pool)
	ensureSCFLoaded(t, pool)
	cw := loadPCICrosswalk(t)

	report, err := soc2import.Import(context.Background(), pool, cw)
	if err != nil {
		t.Fatalf("PCI Import: %v", err)
	}
	if !report.IsNewVersion {
		t.Fatal("first PCI import should be a new version")
	}
	if report.FrameworkSlug != "pcidss" || report.FrameworkVersion != "4.0" {
		t.Fatalf("report framework = %s:%s; want pcidss:4.0", report.FrameworkSlug, report.FrameworkVersion)
	}
	if report.RequirementsCreated != len(cw.Requirements) {
		t.Fatalf("PCI requirements created = %d; want %d", report.RequirementsCreated, len(cw.Requirements))
	}
	if report.EdgesCreated != len(cw.Mappings) {
		t.Fatalf("PCI edges created = %d; want %d", report.EdgesCreated, len(cw.Mappings))
	}

	// Re-import is a no-op (idempotent).
	report2, err := soc2import.Import(context.Background(), pool, cw)
	if err != nil {
		t.Fatalf("second PCI Import: %v", err)
	}
	if report2.RequirementsCreated != 0 || report2.EdgesCreated != 0 {
		t.Fatalf("idempotent re-import created rows: %+v", report2)
	}
	if report2.EdgesUnchanged != len(cw.Mappings) {
		t.Fatalf("re-import edges unchanged = %d; want %d", report2.EdgesUnchanged, len(cw.Mappings))
	}
}

// AC-2 — importing PCI does NOT disturb the SOC 2 or ISO rows; all three
// frameworks coexist in the same graph with distinct framework_version_ids.
// AC-3 — a PCI "8.x" code, an ISO "A.x" code, and a SOC 2 "CCx" code coexist
// without collision (three distinct framework_versions).
func TestPCIImport_DoesNotDisturbSOC2orISO(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	resetCatalog(t, pool)
	resetISO(t, pool)
	resetPCI(t, pool)
	ensureSCFLoaded(t, pool)

	soc2 := loadCrosswalk(t)
	iso := loadISOCrosswalk(t)
	pci := loadPCICrosswalk(t)

	if _, err := soc2import.Import(context.Background(), pool, soc2); err != nil {
		t.Fatalf("SOC 2 Import: %v", err)
	}
	if _, err := soc2import.Import(context.Background(), pool, iso); err != nil {
		t.Fatalf("ISO Import: %v", err)
	}

	// Snapshot SOC 2 + ISO edge counts before the PCI import.
	countEdges := func(slug string) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(context.Background(), `
			SELECT count(*) FROM fw_to_scf_edges e
			JOIN framework_requirements fr ON fr.id = e.framework_requirement_id
			JOIN framework_versions fv ON fv.id = fr.framework_version_id
			JOIN frameworks f ON f.id = fv.framework_id
			WHERE f.slug = $1`, slug).Scan(&n); err != nil {
			t.Fatalf("count %s edges: %v", slug, err)
		}
		return n
	}
	soc2Before := countEdges("soc2")
	isoBefore := countEdges("iso27001")

	if _, err := soc2import.Import(context.Background(), pool, pci); err != nil {
		t.Fatalf("PCI Import: %v", err)
	}

	// SOC 2 + ISO edge counts are unchanged after the PCI import (P0-447-6).
	if got := countEdges("soc2"); got != soc2Before {
		t.Fatalf("PCI import disturbed SOC 2 edges: before=%d after=%d", soc2Before, got)
	}
	if got := countEdges("iso27001"); got != isoBefore {
		t.Fatalf("PCI import disturbed ISO edges: before=%d after=%d", isoBefore, got)
	}

	// AC-3 — three distinct framework_version rows now exist.
	var distinctVersions int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(DISTINCT fv.id) FROM framework_versions fv
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug IN ('soc2', 'iso27001', 'pcidss')`).Scan(&distinctVersions); err != nil {
		t.Fatalf("count distinct versions: %v", err)
	}
	if distinctVersions < 3 {
		t.Fatalf("expected >=3 distinct framework_versions (soc2 + iso27001 + pcidss); got %d", distinctVersions)
	}
}

// AC-3 — for a PCI requirement, the requirement resolves to its SCF anchor(s)
// with the STRM edge type. Mirrors the query the GET
// /v1/requirements/{slug}/anchors read path runs. PCI 8.2.1 -> IAC-01.
func TestPCIImport_RequirementResolvesToAnchorsWithSTRMType(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	resetPCI(t, pool)
	ensureSCFLoaded(t, pool)
	if _, err := soc2import.Import(context.Background(), pool, loadPCICrosswalk(t)); err != nil {
		t.Fatalf("PCI Import: %v", err)
	}

	rows, err := pool.Query(context.Background(), `
		SELECT a.scf_id, e.relationship_type::text
		FROM framework_requirements fr
		JOIN framework_versions fv ON fv.id = fr.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		JOIN fw_to_scf_edges e ON e.framework_requirement_id = fr.id
		JOIN scf_anchors a ON a.id = e.scf_anchor_id
		WHERE f.slug = 'pcidss' AND fv.version = '4.0' AND fr.code = '8.2.1'`)
	if err != nil {
		t.Fatalf("anchors-for-PCI-requirement query: %v", err)
	}
	defer rows.Close()

	var anchors []string
	var strm string
	for rows.Next() {
		var scfID, rt string
		if err := rows.Scan(&scfID, &rt); err != nil {
			t.Fatalf("scan: %v", err)
		}
		anchors = append(anchors, scfID)
		strm = rt
	}
	if len(anchors) == 0 {
		t.Fatal("PCI requirement 8.2.1 resolved to ZERO anchors — read path broken")
	}
	if anchors[0] != "IAC-01" {
		t.Fatalf("8.2.1 should map to IAC-01; got %v", anchors)
	}
	if strm == "" {
		t.Fatal("edge resolved with empty STRM relationship_type")
	}
}

// AC-6 — THE LOAD-BEARING TEST (three-framework extension of slice 438's
// invariant-#1 proof). One SCF anchor (IAC-01) is shared between a SOC 2
// criterion (CC6.1), an ISO Annex A control (A.5.15), AND a PCI requirement
// (8.2.1). The anchor resolves to ALL THREE framework satisfactions through
// the single shared anchor row. This is invariant #1 demonstrated at three
// frameworks: one control, N frameworks, NO per-framework duplicated control
// and NO requirement -> requirement edge.
func TestPCIImport_SharedAnchorSatisfiesThreeFrameworks_Invariant1(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	resetCatalog(t, pool)
	resetISO(t, pool)
	resetPCI(t, pool)
	ensureSCFLoaded(t, pool)

	if _, err := soc2import.Import(context.Background(), pool, loadCrosswalk(t)); err != nil {
		t.Fatalf("SOC 2 Import: %v", err)
	}
	if _, err := soc2import.Import(context.Background(), pool, loadISOCrosswalk(t)); err != nil {
		t.Fatalf("ISO Import: %v", err)
	}
	if _, err := soc2import.Import(context.Background(), pool, loadPCICrosswalk(t)); err != nil {
		t.Fatalf("PCI Import: %v", err)
	}

	const sharedAnchor = "IAC-01"

	// Reverse traversal from the single shared anchor: which frameworks does
	// it satisfy? Group requirement codes by framework slug.
	rows, err := pool.Query(context.Background(), `
		SELECT f.slug, fr.code
		FROM scf_anchors a
		JOIN fw_to_scf_edges e ON e.scf_anchor_id = a.id
		JOIN framework_requirements fr ON fr.id = e.framework_requirement_id
		JOIN framework_versions fv ON fv.id = fr.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE a.scf_id = $1
		ORDER BY f.slug, fr.code`, sharedAnchor)
	if err != nil {
		t.Fatalf("shared-anchor traversal query: %v", err)
	}
	defer rows.Close()

	byFramework := map[string][]string{}
	for rows.Next() {
		var slug, code string
		if err := rows.Scan(&slug, &code); err != nil {
			t.Fatalf("scan: %v", err)
		}
		byFramework[slug] = append(byFramework[slug], code)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}

	// The single anchor must satisfy a SOC 2 requirement AND an ISO
	// requirement AND a PCI requirement — through the SAME anchor row.
	if len(byFramework["soc2"]) == 0 {
		t.Fatalf("invariant #1 unproven: anchor %s satisfies no SOC 2 requirement", sharedAnchor)
	}
	if len(byFramework["iso27001"]) == 0 {
		t.Fatalf("invariant #1 unproven: anchor %s satisfies no ISO requirement", sharedAnchor)
	}
	if len(byFramework["pcidss"]) == 0 {
		t.Fatalf("invariant #1 unproven: anchor %s satisfies no PCI requirement", sharedAnchor)
	}
	// Concretely: SOC 2 CC6.1, ISO A.5.15, and PCI 8.2.1 all map to IAC-01.
	if !contains(byFramework["soc2"], "CC6.1") {
		t.Fatalf("expected SOC 2 CC6.1 among %s's SOC 2 satisfactions; got %v", sharedAnchor, byFramework["soc2"])
	}
	if !contains(byFramework["iso27001"], "A.5.15") {
		t.Fatalf("expected ISO A.5.15 among %s's ISO satisfactions; got %v", sharedAnchor, byFramework["iso27001"])
	}
	if !contains(byFramework["pcidss"], "8.2.1") {
		t.Fatalf("expected PCI 8.2.1 among %s's PCI satisfactions; got %v", sharedAnchor, byFramework["pcidss"])
	}

	// Belt-and-suspenders for invariant #1 / P0-447-3: there is exactly ONE
	// scf_anchors row for IAC-01 (no per-framework duplication of the
	// control). All three framework satisfactions traverse that single row.
	var anchorRowCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM scf_anchors WHERE scf_id = $1`, sharedAnchor).Scan(&anchorRowCount); err != nil {
		t.Fatalf("count anchor rows: %v", err)
	}
	if anchorRowCount != 1 {
		t.Fatalf("invariant #1 violated: %d scf_anchors rows for %s; expected exactly 1 (one control, N frameworks)", anchorRowCount, sharedAnchor)
	}

	t.Logf("invariant #1 proven at THREE frameworks: single anchor %s satisfies SOC 2 %v AND ISO %v AND PCI %v",
		sharedAnchor, byFramework["soc2"], byFramework["iso27001"], byFramework["pcidss"])
}

// AC-2 + P0-447-3 — an edge whose scf_anchor does not resolve to a real
// anchor is a clear loader error, not a panic and not a dangling edge. The
// whole import rolls back, leaving no PCI requirement rows behind.
func TestPCIImport_RejectsEdgeToNonexistentAnchor(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	resetPCI(t, pool)
	ensureSCFLoaded(t, pool)

	bad := &soc2import.Crosswalk{
		SchemaVersion:     "1.0",
		FrameworkSlug:     "pcidss",
		FrameworkName:     "PCI DSS v4.0",
		FrameworkIssuer:   "PCI Security Standards Council",
		FrameworkVersion:  "4.0",
		ReleaseDate:       "2022-03-31",
		SourceAttribution: "community_draft",
		Requirements: []soc2import.Requirement{
			{Code: "8.2.1", Title: "Unique IDs", Body: "body"},
		},
		Mappings: []soc2import.Mapping{
			{RequirementCode: "8.2.1", SCFAnchor: "ZZZ-99", RelationshipType: "equal", Strength: 0.9, Rationale: "no such anchor"},
		},
	}
	_, err := soc2import.Import(context.Background(), pool, bad)
	if err == nil {
		t.Fatal("expected error for edge to nonexistent SCF anchor ZZZ-99")
	}
	var leaked int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM framework_requirements fr
		JOIN framework_versions fv ON fv.id = fr.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = 'pcidss' AND fr.code = '8.2.1'`).Scan(&leaked); err != nil {
		t.Fatalf("leak-check query: %v", err)
	}
	if leaked != 0 {
		t.Fatalf("failed import left %d PCI requirement rows behind — transaction did not roll back", leaked)
	}
}
