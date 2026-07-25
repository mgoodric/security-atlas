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

// Slice 438 integration suite — the FRAMEWORK-AGNOSTIC crosswalk importer
// proven against a real Postgres with a SECOND framework (ISO 27001:2022).
//
// This file is the reason the slice exists: it demonstrates UCF invariant
// #1 ("one control, N framework satisfactions") empirically. Until now the
// graph carried exactly one framework (SOC 2), so the invariant was
// asserted but unproven — a single framework cannot show that one SCF
// anchor satisfies two frameworks at once. Here we import BOTH crosswalks
// and prove a single shared SCF anchor resolves to a SOC 2 requirement AND
// an ISO requirement through that one anchor.

func loadISOCrosswalk(t *testing.T) *soc2import.Crosswalk {
	t.Helper()
	cw, err := soc2import.Load(filepath.Join("..", "..", "..", "data", "crosswalks", "iso27001-2022.yaml"))
	if err != nil {
		t.Fatalf("soc2import.Load(iso27001-2022.yaml): %v", err)
	}
	return cw
}

// resetISO wipes the ISO framework rows (mirrors resetCatalog's SOC 2 wipe)
// so each test starts clean. Catalog tables are not tenant-scoped, so this
// is a plain DELETE; fw_to_scf_edges + framework_requirements cascade off
// framework_versions.
func resetISO(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
		`DELETE FROM fw_to_scf_edges WHERE framework_requirement_id IN (
			SELECT fr.id FROM framework_requirements fr
			JOIN framework_versions fv ON fv.id = fr.framework_version_id
			JOIN frameworks f ON f.id = fv.framework_id
			WHERE f.slug = 'iso27001')`,
		`DELETE FROM framework_requirements WHERE framework_version_id IN (
			SELECT fv.id FROM framework_versions fv
			JOIN frameworks f ON f.id = fv.framework_id
			WHERE f.slug = 'iso27001')`,
		`DELETE FROM framework_versions WHERE framework_id IN (
			SELECT id FROM frameworks WHERE slug = 'iso27001' AND tenant_id IS NULL)`,
		`DELETE FROM frameworks WHERE slug = 'iso27001' AND tenant_id IS NULL`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("resetISO %q: %v", stmt, err)
		}
	}
}

// AC-4 + AC-5 — importing the ISO crosswalk creates framework_requirements
// + fw_to_scf_edges rows for ISO 27001:2022, and re-importing is idempotent.
func TestISOImport_CreatesRowsAndIsIdempotent(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	resetISO(t, pool)
	ensureSCFLoaded(t, pool)
	cw := loadISOCrosswalk(t)

	report, err := soc2import.Import(context.Background(), pool, cw)
	if err != nil {
		t.Fatalf("ISO Import: %v", err)
	}
	if !report.IsNewVersion {
		t.Fatal("first ISO import should be a new version")
	}
	if report.FrameworkSlug != "iso27001" || report.FrameworkVersion != "2022" {
		t.Fatalf("report framework = %s:%s; want iso27001:2022", report.FrameworkSlug, report.FrameworkVersion)
	}
	if report.RequirementsCreated != len(cw.Requirements) {
		t.Fatalf("ISO requirements created = %d; want %d", report.RequirementsCreated, len(cw.Requirements))
	}
	if report.EdgesCreated != len(cw.Mappings) {
		t.Fatalf("ISO edges created = %d; want %d", report.EdgesCreated, len(cw.Mappings))
	}

	// Re-import is a no-op.
	report2, err := soc2import.Import(context.Background(), pool, cw)
	if err != nil {
		t.Fatalf("second ISO Import: %v", err)
	}
	if report2.RequirementsCreated != 0 || report2.EdgesCreated != 0 {
		t.Fatalf("idempotent re-import created rows: %+v", report2)
	}
	if report2.EdgesUnchanged != len(cw.Mappings) {
		t.Fatalf("re-import edges unchanged = %d; want %d", report2.EdgesUnchanged, len(cw.Mappings))
	}
}

// AC-3 (slice 467) — the FULL Annex A crosswalk (all 93 controls) imports
// cleanly into a real Postgres: every requirement row and every STRM edge is
// created, and the import is idempotent. This is the completion proof for the
// slice — slice 438 shipped 36 controls; this asserts the graph now carries
// all 93. The earlier TestISOImport_CreatesRowsAndIsIdempotent already ties
// the created-row counts to len(cw.Requirements)/len(cw.Mappings); this test
// pins the ABSOLUTE Annex A count so a silently-shrunk crosswalk is caught.
func TestISOImport_FullAnnexA_ImportsAll93Controls(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	resetISO(t, pool)
	ensureSCFLoaded(t, pool)
	cw := loadISOCrosswalk(t)

	const fullAnnexACount = 93
	if len(cw.Requirements) != fullAnnexACount {
		t.Fatalf("crosswalk carries %d requirements; want full Annex A %d", len(cw.Requirements), fullAnnexACount)
	}

	if _, err := soc2import.Import(context.Background(), pool, cw); err != nil {
		t.Fatalf("ISO Import: %v", err)
	}

	// Count the ISO requirement rows actually persisted under iso27001:2022.
	var reqRows int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM framework_requirements fr
		JOIN framework_versions fv ON fv.id = fr.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = 'iso27001' AND fv.version = '2022'`).Scan(&reqRows); err != nil {
		t.Fatalf("count ISO requirement rows: %v", err)
	}
	if reqRows != fullAnnexACount {
		t.Fatalf("persisted ISO requirement rows = %d; want %d", reqRows, fullAnnexACount)
	}

	// Every persisted requirement resolves to at least one SCF anchor edge —
	// no orphan requirement (invariant #1: every framework requirement is
	// satisfied THROUGH an SCF anchor, never directly).
	var orphans int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM framework_requirements fr
		JOIN framework_versions fv ON fv.id = fr.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = 'iso27001' AND fv.version = '2022'
		  AND NOT EXISTS (
			SELECT 1 FROM fw_to_scf_edges e WHERE e.framework_requirement_id = fr.id)`).Scan(&orphans); err != nil {
		t.Fatalf("orphan-requirement query: %v", err)
	}
	if orphans != 0 {
		t.Fatalf("%d ISO requirements have no SCF anchor edge — invariant #1 violated", orphans)
	}

	// Idempotent re-import of the full set.
	report2, err := soc2import.Import(context.Background(), pool, cw)
	if err != nil {
		t.Fatalf("second full ISO Import: %v", err)
	}
	if report2.RequirementsCreated != 0 || report2.EdgesCreated != 0 {
		t.Fatalf("idempotent re-import of full Annex A created rows: %+v", report2)
	}
}

// AC-3 (slice 467) — the slice-438 curated subset is preserved verbatim: the
// 36 original controls (and their anchor mappings) still resolve after the
// full-coverage extension. This is the no-regression-of-the-subset guard.
func TestISOImport_FullAnnexA_PreservesSlice438Subset(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	resetISO(t, pool)
	ensureSCFLoaded(t, pool)
	if _, err := soc2import.Import(context.Background(), pool, loadISOCrosswalk(t)); err != nil {
		t.Fatalf("ISO Import: %v", err)
	}

	// A representative sample of the slice-438 subset, each with the anchor it
	// was originally mapped to. If the extension silently re-anchored or
	// dropped any of these, this fails.
	subset := map[string]string{
		"A.5.1":  "GOV-01",
		"A.5.15": "IAC-01", // the invariant-#1 shared anchor
		"A.5.18": "IAC-07",
		"A.5.24": "IRO-04",
		"A.6.3":  "HRS-04",
		"A.7.2":  "PES-04",
		"A.8.2":  "IAC-21",
		"A.8.8":  "VPM-01",
		"A.8.13": "BCD-09",
		"A.8.15": "AAA-01",
		"A.8.25": "SEA-05",
	}
	for code, wantAnchor := range subset {
		var found int
		if err := pool.QueryRow(context.Background(), `
			SELECT count(*) FROM framework_requirements fr
			JOIN framework_versions fv ON fv.id = fr.framework_version_id
			JOIN frameworks f ON f.id = fv.framework_id
			JOIN fw_to_scf_edges e ON e.framework_requirement_id = fr.id
			JOIN scf_anchors a ON a.id = e.scf_anchor_id
			WHERE f.slug = 'iso27001' AND fv.version = '2022'
			  AND fr.code = $1 AND a.scf_id = $2`, code, wantAnchor).Scan(&found); err != nil {
			t.Fatalf("subset-preservation query (%s -> %s): %v", code, wantAnchor, err)
		}
		if found == 0 {
			t.Errorf("slice-438 subset control %s no longer maps to %s after full-coverage extension", code, wantAnchor)
		}
	}
}

// AC-5 — importing ISO does NOT disturb the SOC 2 rows; both frameworks
// coexist in the same graph with distinct framework_version_ids.
// AC-3 — an ISO `A.x` code and a SOC 2 `CCx` code coexist without collision.
func TestISOImport_DoesNotDisturbSOC2(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	resetCatalog(t, pool)
	resetISO(t, pool)
	ensureSCFLoaded(t, pool)

	soc2 := loadCrosswalk(t)
	iso := loadISOCrosswalk(t)

	if _, err := soc2import.Import(context.Background(), pool, soc2); err != nil {
		t.Fatalf("SOC 2 Import: %v", err)
	}
	// Snapshot SOC 2 edge count before ISO import.
	var soc2EdgesBefore int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM fw_to_scf_edges e
		JOIN framework_requirements fr ON fr.id = e.framework_requirement_id
		JOIN framework_versions fv ON fv.id = fr.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = 'soc2'`).Scan(&soc2EdgesBefore); err != nil {
		t.Fatalf("count SOC 2 edges before: %v", err)
	}

	if _, err := soc2import.Import(context.Background(), pool, iso); err != nil {
		t.Fatalf("ISO Import: %v", err)
	}

	// SOC 2 edge count is unchanged after ISO import.
	var soc2EdgesAfter int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM fw_to_scf_edges e
		JOIN framework_requirements fr ON fr.id = e.framework_requirement_id
		JOIN framework_versions fv ON fv.id = fr.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = 'soc2'`).Scan(&soc2EdgesAfter); err != nil {
		t.Fatalf("count SOC 2 edges after: %v", err)
	}
	if soc2EdgesAfter != soc2EdgesBefore {
		t.Fatalf("ISO import disturbed SOC 2 edges: before=%d after=%d", soc2EdgesBefore, soc2EdgesAfter)
	}

	// AC-3 — the two frameworks are distinct framework_version rows.
	var distinctVersions int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(DISTINCT fv.id) FROM framework_versions fv
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug IN ('soc2', 'iso27001')`).Scan(&distinctVersions); err != nil {
		t.Fatalf("count distinct versions: %v", err)
	}
	if distinctVersions < 2 {
		t.Fatalf("expected >=2 distinct framework_versions (soc2 + iso27001); got %d", distinctVersions)
	}
}

// AC-6 — for an ISO requirement (resolved by the `slug:version:code`
// natural key the read endpoint accepts, e.g. `iso27001:2022:A.5.15`), the
// requirement resolves to its SCF anchor(s) with the STRM edge type. This
// mirrors the query the GET /v1/requirements/{id}/coverage handler runs
// (its `anchors[]` field) — the read path already exists and is exercised
// here at the SQL layer.
func TestISOImport_RequirementResolvesToAnchorsWithSTRMType(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	resetISO(t, pool)
	ensureSCFLoaded(t, pool)
	if _, err := soc2import.Import(context.Background(), pool, loadISOCrosswalk(t)); err != nil {
		t.Fatalf("ISO Import: %v", err)
	}

	rows, err := pool.Query(context.Background(), `
		SELECT a.scf_id, e.relationship_type::text
		FROM framework_requirements fr
		JOIN framework_versions fv ON fv.id = fr.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		JOIN fw_to_scf_edges e ON e.framework_requirement_id = fr.id
		JOIN scf_anchors a ON a.id = e.scf_anchor_id
		WHERE f.slug = 'iso27001' AND fv.version = '2022' AND fr.code = 'A.5.15'`)
	if err != nil {
		t.Fatalf("anchors-for-ISO-requirement query: %v", err)
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
		t.Fatal("ISO requirement A.5.15 resolved to ZERO anchors — read path broken")
	}
	if anchors[0] != "IAC-01" {
		t.Fatalf("A.5.15 should map to IAC-01; got %v", anchors)
	}
	if strm == "" {
		t.Fatal("edge resolved with empty STRM relationship_type")
	}
}

// AC-7 — THE LOAD-BEARING TEST. One SCF anchor (IAC-01) is shared between a
// SOC 2 criterion (CC6.1) and an ISO Annex A control (A.5.15). The anchor
// resolves to BOTH framework satisfactions through the single shared anchor.
// This is invariant #1 demonstrated: one control, two frameworks, NO
// per-framework duplicated control and NO requirement->requirement edge.
func TestISOImport_SharedAnchorSatisfiesBothFrameworks_Invariant1(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	resetCatalog(t, pool)
	resetISO(t, pool)
	ensureSCFLoaded(t, pool)

	if _, err := soc2import.Import(context.Background(), pool, loadCrosswalk(t)); err != nil {
		t.Fatalf("SOC 2 Import: %v", err)
	}
	if _, err := soc2import.Import(context.Background(), pool, loadISOCrosswalk(t)); err != nil {
		t.Fatalf("ISO Import: %v", err)
	}

	const sharedAnchor = "IAC-01"

	// Reverse traversal from the single shared anchor: which frameworks does
	// it satisfy? Group the requirement codes by framework slug.
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
	// requirement — through the SAME anchor row.
	if len(byFramework["soc2"]) == 0 {
		t.Fatalf("invariant #1 unproven: anchor %s satisfies no SOC 2 requirement", sharedAnchor)
	}
	if len(byFramework["iso27001"]) == 0 {
		t.Fatalf("invariant #1 unproven: anchor %s satisfies no ISO requirement", sharedAnchor)
	}
	// Concretely: SOC 2 CC6.1 and ISO A.5.15 both map to IAC-01.
	if !contains(byFramework["soc2"], "CC6.1") {
		t.Fatalf("expected SOC 2 CC6.1 among %s's SOC 2 satisfactions; got %v", sharedAnchor, byFramework["soc2"])
	}
	if !contains(byFramework["iso27001"], "A.5.15") {
		t.Fatalf("expected ISO A.5.15 among %s's ISO satisfactions; got %v", sharedAnchor, byFramework["iso27001"])
	}

	// Belt-and-suspenders for invariant #1 / P0-438-2: there is exactly ONE
	// scf_anchors row for IAC-01 (no per-framework duplication of the
	// control). Both framework satisfactions traverse that single row.
	var anchorRowCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM scf_anchors WHERE scf_id = $1`, sharedAnchor).Scan(&anchorRowCount); err != nil {
		t.Fatalf("count anchor rows: %v", err)
	}
	if anchorRowCount != 1 {
		t.Fatalf("invariant #1 violated: %d scf_anchors rows for %s; expected exactly 1 (one control, N frameworks)", anchorRowCount, sharedAnchor)
	}

	t.Logf("invariant #1 proven: single anchor %s satisfies SOC 2 %v AND ISO %v",
		sharedAnchor, byFramework["soc2"], byFramework["iso27001"])
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// AC-2 + P0-438-4 — an edge whose scf_anchor does not resolve to a real
// anchor is a clear loader error, not a panic and not a dangling edge.
func TestISOImport_RejectsEdgeToNonexistentAnchor(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	resetISO(t, pool)
	ensureSCFLoaded(t, pool)

	bad := &soc2import.Crosswalk{
		SchemaVersion:     "1.0",
		FrameworkSlug:     "iso27001",
		FrameworkName:     "ISO/IEC 27001:2022",
		FrameworkIssuer:   "ISO/IEC",
		FrameworkVersion:  "2022",
		ReleaseDate:       "2022-10-25",
		SourceAttribution: "community_draft",
		Requirements: []soc2import.Requirement{
			{Code: "A.5.15", Title: "Access control", Body: "body"},
		},
		Mappings: []soc2import.Mapping{
			{RequirementCode: "A.5.15", SCFAnchor: "ZZZ-99", RelationshipType: "equal", Strength: 0.9, Rationale: "no such anchor"},
		},
	}
	_, err := soc2import.Import(context.Background(), pool, bad)
	if err == nil {
		t.Fatal("expected error for edge to nonexistent SCF anchor ZZZ-99")
	}
	// Clear, named error — not a panic; the whole import rolled back.
	var leaked int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM framework_requirements fr
		JOIN framework_versions fv ON fv.id = fr.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = 'iso27001' AND fr.code = 'A.5.15'`).Scan(&leaked); err != nil {
		t.Fatalf("leak-check query: %v", err)
	}
	if leaked != 0 {
		t.Fatalf("failed import left %d ISO requirement rows behind — transaction did not roll back", leaked)
	}
}
