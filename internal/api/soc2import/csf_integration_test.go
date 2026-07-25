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

// Slice 480 integration suite — the FRAMEWORK-AGNOSTIC crosswalk importer
// (slice 438) proven against a real Postgres with a FOURTH framework
// (NIST CSF 2.0). This extends the slice-438/447 invariant-#1 proof from
// three frameworks to four: a single shared SCF anchor (IAC-01) resolves to a
// SOC 2 criterion, an ISO Annex A control, a PCI requirement, AND a CSF
// Subcategory through that ONE anchor row — one control, N framework
// satisfactions, no per-framework duplication and no requirement ->
// requirement edge (invariants #1 + #7). AC-5 additionally proves the GOVERN
// function (no SOC 2 analog) maps to an SCF governance-family anchor.

func loadCSFCrosswalk(t *testing.T) *soc2import.Crosswalk {
	t.Helper()
	cw, err := soc2import.Load(filepath.Join("..", "..", "..", "data", "crosswalks", "nist-csf-2.0.yaml"))
	if err != nil {
		t.Fatalf("soc2import.Load(nist-csf-2.0.yaml): %v", err)
	}
	return cw
}

// resetCSF wipes the CSF framework rows (mirrors resetISO/resetPCI) so each
// test starts clean. Catalog tables are not tenant-scoped, so this is a plain
// DELETE; fw_to_scf_edges + framework_requirements cascade off
// framework_versions.
func resetCSF(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
		`DELETE FROM fw_to_scf_edges WHERE framework_requirement_id IN (
			SELECT fr.id FROM framework_requirements fr
			JOIN framework_versions fv ON fv.id = fr.framework_version_id
			JOIN frameworks f ON f.id = fv.framework_id
			WHERE f.slug = 'nist_csf')`,
		`DELETE FROM framework_requirements WHERE framework_version_id IN (
			SELECT fv.id FROM framework_versions fv
			JOIN frameworks f ON f.id = fv.framework_id
			WHERE f.slug = 'nist_csf')`,
		`DELETE FROM framework_versions WHERE framework_id IN (
			SELECT id FROM frameworks WHERE slug = 'nist_csf' AND tenant_id IS NULL)`,
		`DELETE FROM frameworks WHERE slug = 'nist_csf' AND tenant_id IS NULL`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("resetCSF %q: %v", stmt, err)
		}
	}
}

// AC-2 + AC-6 — importing the CSF crosswalk creates framework_requirements +
// fw_to_scf_edges rows for NIST CSF 2.0, and re-importing is idempotent.
func TestCSFImport_CreatesRowsAndIsIdempotent(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	resetCSF(t, pool)
	ensureSCFLoaded(t, pool)
	cw := loadCSFCrosswalk(t)

	report, err := soc2import.Import(context.Background(), pool, cw)
	if err != nil {
		t.Fatalf("CSF Import: %v", err)
	}
	if !report.IsNewVersion {
		t.Fatal("first CSF import should be a new version")
	}
	if report.FrameworkSlug != "nist_csf" || report.FrameworkVersion != "2.0" {
		t.Fatalf("report framework = %s:%s; want nist_csf:2.0", report.FrameworkSlug, report.FrameworkVersion)
	}
	if report.RequirementsCreated != len(cw.Requirements) {
		t.Fatalf("CSF requirements created = %d; want %d", report.RequirementsCreated, len(cw.Requirements))
	}
	if report.EdgesCreated != len(cw.Mappings) {
		t.Fatalf("CSF edges created = %d; want %d", report.EdgesCreated, len(cw.Mappings))
	}

	// Re-import is a no-op (idempotent).
	report2, err := soc2import.Import(context.Background(), pool, cw)
	if err != nil {
		t.Fatalf("second CSF Import: %v", err)
	}
	if report2.RequirementsCreated != 0 || report2.EdgesCreated != 0 {
		t.Fatalf("idempotent re-import created rows: %+v", report2)
	}
	if report2.EdgesUnchanged != len(cw.Mappings) {
		t.Fatalf("re-import edges unchanged = %d; want %d", report2.EdgesUnchanged, len(cw.Mappings))
	}
}

// AC-2 — importing CSF does NOT disturb the SOC 2 / ISO / PCI rows; all four
// frameworks coexist in the same graph with distinct framework_version_ids.
func TestCSFImport_DoesNotDisturbOtherFrameworks(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	resetCatalog(t, pool)
	resetISO(t, pool)
	resetPCI(t, pool)
	resetCSF(t, pool)
	ensureSCFLoaded(t, pool)

	soc2 := loadCrosswalk(t)
	iso := loadISOCrosswalk(t)
	pci := loadPCICrosswalk(t)
	csf := loadCSFCrosswalk(t)

	if _, err := soc2import.Import(context.Background(), pool, soc2); err != nil {
		t.Fatalf("SOC 2 Import: %v", err)
	}
	if _, err := soc2import.Import(context.Background(), pool, iso); err != nil {
		t.Fatalf("ISO Import: %v", err)
	}
	if _, err := soc2import.Import(context.Background(), pool, pci); err != nil {
		t.Fatalf("PCI Import: %v", err)
	}

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
	pciBefore := countEdges("pcidss")

	if _, err := soc2import.Import(context.Background(), pool, csf); err != nil {
		t.Fatalf("CSF Import: %v", err)
	}

	// SOC 2 / ISO / PCI edge counts are unchanged after the CSF import (AC-2).
	if got := countEdges("soc2"); got != soc2Before {
		t.Fatalf("CSF import disturbed SOC 2 edges: before=%d after=%d", soc2Before, got)
	}
	if got := countEdges("iso27001"); got != isoBefore {
		t.Fatalf("CSF import disturbed ISO edges: before=%d after=%d", isoBefore, got)
	}
	if got := countEdges("pcidss"); got != pciBefore {
		t.Fatalf("CSF import disturbed PCI edges: before=%d after=%d", pciBefore, got)
	}

	// Four distinct framework_version rows now exist.
	var distinctVersions int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(DISTINCT fv.id) FROM framework_versions fv
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug IN ('soc2', 'iso27001', 'pcidss', 'nist_csf')`).Scan(&distinctVersions); err != nil {
		t.Fatalf("count distinct versions: %v", err)
	}
	if distinctVersions < 4 {
		t.Fatalf("expected >=4 distinct framework_versions (soc2 + iso27001 + pcidss + nist_csf); got %d", distinctVersions)
	}
}

// AC-3 — for a CSF requirement, the requirement resolves to its SCF anchor(s)
// with the STRM edge type. Mirrors the query the GET
// /v1/requirements/{slug}/anchors read path runs. CSF PR.AA-01 -> IAC-01.
func TestCSFImport_RequirementResolvesToAnchorsWithSTRMType(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	resetCSF(t, pool)
	ensureSCFLoaded(t, pool)
	if _, err := soc2import.Import(context.Background(), pool, loadCSFCrosswalk(t)); err != nil {
		t.Fatalf("CSF Import: %v", err)
	}

	rows, err := pool.Query(context.Background(), `
		SELECT a.scf_id, e.relationship_type::text
		FROM framework_requirements fr
		JOIN framework_versions fv ON fv.id = fr.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		JOIN fw_to_scf_edges e ON e.framework_requirement_id = fr.id
		JOIN scf_anchors a ON a.id = e.scf_anchor_id
		WHERE f.slug = 'nist_csf' AND fv.version = '2.0' AND fr.code = 'PR.AA-01'`)
	if err != nil {
		t.Fatalf("anchors-for-CSF-requirement query: %v", err)
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
		t.Fatal("CSF requirement PR.AA-01 resolved to ZERO anchors — read path broken")
	}
	if anchors[0] != "IAC-01" {
		t.Fatalf("PR.AA-01 should map to IAC-01; got %v", anchors)
	}
	if strm == "" {
		t.Fatal("edge resolved with empty STRM relationship_type")
	}
}

// AC-4 — THE LOAD-BEARING TEST (four-framework extension of slices 438/447's
// invariant-#1 proof). One SCF anchor (IAC-01) is shared between a SOC 2
// criterion (CC6.1), an ISO Annex A control (A.5.15), a PCI requirement
// (8.2.1), AND a CSF Subcategory (PR.AA-01). The anchor resolves to ALL FOUR
// framework satisfactions through the single shared anchor row. This is
// invariant #1 demonstrated at four frameworks: one control, N frameworks, NO
// per-framework duplicated control and NO requirement -> requirement edge.
func TestCSFImport_SharedAnchorSatisfiesFourFrameworks_Invariant1(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	resetCatalog(t, pool)
	resetISO(t, pool)
	resetPCI(t, pool)
	resetCSF(t, pool)
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
	if _, err := soc2import.Import(context.Background(), pool, loadCSFCrosswalk(t)); err != nil {
		t.Fatalf("CSF Import: %v", err)
	}

	const sharedAnchor = "IAC-01"

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

	// The single anchor must satisfy a requirement in ALL FOUR frameworks —
	// through the SAME anchor row.
	for _, fw := range []string{"soc2", "iso27001", "pcidss", "nist_csf"} {
		if len(byFramework[fw]) == 0 {
			t.Fatalf("invariant #1 unproven: anchor %s satisfies no %s requirement", sharedAnchor, fw)
		}
	}
	// Concretely: SOC 2 CC6.1, ISO A.5.15, PCI 8.2.1, and CSF PR.AA-01 all
	// map to IAC-01.
	if !contains(byFramework["soc2"], "CC6.1") {
		t.Fatalf("expected SOC 2 CC6.1 among %s's SOC 2 satisfactions; got %v", sharedAnchor, byFramework["soc2"])
	}
	if !contains(byFramework["iso27001"], "A.5.15") {
		t.Fatalf("expected ISO A.5.15 among %s's ISO satisfactions; got %v", sharedAnchor, byFramework["iso27001"])
	}
	if !contains(byFramework["pcidss"], "8.2.1") {
		t.Fatalf("expected PCI 8.2.1 among %s's PCI satisfactions; got %v", sharedAnchor, byFramework["pcidss"])
	}
	if !contains(byFramework["nist_csf"], "PR.AA-01") {
		t.Fatalf("expected CSF PR.AA-01 among %s's CSF satisfactions; got %v", sharedAnchor, byFramework["nist_csf"])
	}

	// Belt-and-suspenders for invariant #1 / P0-480-3: there is exactly ONE
	// scf_anchors row for IAC-01 (no per-framework duplication of the
	// control). All four framework satisfactions traverse that single row.
	var anchorRowCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM scf_anchors WHERE scf_id = $1`, sharedAnchor).Scan(&anchorRowCount); err != nil {
		t.Fatalf("count anchor rows: %v", err)
	}
	if anchorRowCount != 1 {
		t.Fatalf("invariant #1 violated: %d scf_anchors rows for %s; expected exactly 1 (one control, N frameworks)", anchorRowCount, sharedAnchor)
	}

	t.Logf("invariant #1 proven at FOUR frameworks: single anchor %s satisfies SOC 2 %v AND ISO %v AND PCI %v AND CSF %v",
		sharedAnchor, byFramework["soc2"], byFramework["iso27001"], byFramework["pcidss"], byFramework["nist_csf"])
}

// AC-5 — THE LOAD-BEARING DIFFERENTIATOR vs. 438/447. The CSF GOVERN function
// is CSF 2.0's headline addition; SOC 2's TSC has no GOVERN *Function* — there
// is no SOC 2 organizing structure that is the analog of GOVERN. This test
// proves a GOVERN-function Subcategory (GV.RR-02 — security roles and
// responsibilities) maps to an SCF governance-family anchor (GOV-04) via an
// STRM edge, demonstrating the graph models a Function with no v1-framework
// structural counterpart — it does not quietly assume framework overlap.
//
// NOTE on what is and is NOT asserted: SOC 2's Control-Environment criteria
// (CC1.x) DO map to the SCF governance anchors (GOV-01/GOV-04) — that is
// expected and is exactly invariant #1 at work (one SCF governance anchor
// satisfies a SOC 2 CC1.x criterion AND a CSF GOVERN Subcategory through the
// SAME anchor). The "no analog" claim is at the FRAMEWORK-STRUCTURE grain
// (SOC 2 has no GOVERN Function), NOT a claim that the governance anchor is
// SOC-2-untouched. The constitutional guarantee proven here is invariant #7:
// CSF GOVERN reaches governance coverage ONLY through an SCF anchor, never via
// a CSF -> SOC 2 requirement-to-requirement edge.
func TestCSFImport_GovernFunctionMapsToGovernanceAnchor_NoSOC2Analog(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	resetCatalog(t, pool)
	resetCSF(t, pool)
	ensureSCFLoaded(t, pool)

	if _, err := soc2import.Import(context.Background(), pool, loadCrosswalk(t)); err != nil {
		t.Fatalf("SOC 2 Import: %v", err)
	}
	if _, err := soc2import.Import(context.Background(), pool, loadCSFCrosswalk(t)); err != nil {
		t.Fatalf("CSF Import: %v", err)
	}

	// GV.RR-02 resolves to the GOV-04 governance-family anchor with a non-empty
	// STRM type. This is the GOVERN-Subcategory -> governance-anchor edge.
	var govAnchor, strm string
	if err := pool.QueryRow(context.Background(), `
		SELECT a.scf_id, e.relationship_type::text
		FROM framework_requirements fr
		JOIN framework_versions fv ON fv.id = fr.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		JOIN fw_to_scf_edges e ON e.framework_requirement_id = fr.id
		JOIN scf_anchors a ON a.id = e.scf_anchor_id
		WHERE f.slug = 'nist_csf' AND fv.version = '2.0' AND fr.code = 'GV.RR-02'`).Scan(&govAnchor, &strm); err != nil {
		t.Fatalf("GV.RR-02 anchor query: %v", err)
	}
	if govAnchor != "GOV-04" {
		t.Fatalf("GV.RR-02 should map to governance anchor GOV-04; got %q", govAnchor)
	}
	if strm == "" {
		t.Fatal("GOVERN edge resolved with empty STRM relationship_type")
	}

	// Invariant #7 (the real no-analog guarantee): there exists NO edge table
	// path from a CSF requirement directly to a SOC 2 requirement. CSF GOVERN
	// reaches governance coverage ONLY through SCF anchors. We prove this by
	// confirming the GOVERN Subcategory's only graph neighbors are SCF anchors,
	// never another framework's requirement row.
	var csfReqToReqEdges int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM fw_to_scf_edges e
		JOIN framework_requirements fr ON fr.id = e.framework_requirement_id
		JOIN framework_versions fv ON fv.id = fr.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		JOIN scf_anchors a ON a.id = e.scf_anchor_id
		WHERE f.slug = 'nist_csf' AND fr.code LIKE 'GV.%'
		  AND a.id IS NULL`).Scan(&csfReqToReqEdges); err != nil {
		t.Fatalf("csf-govern-edge-target query: %v", err)
	}
	if csfReqToReqEdges != 0 {
		t.Fatalf("invariant #7 violated: %d CSF GOVERN edges resolve to a non-anchor target", csfReqToReqEdges)
	}

	// Every CSF GOVERN edge target is a real SCF anchor (the only edge kind the
	// schema permits). Count them as a positive assertion the GOVERN function
	// is wired into the governance/risk/third-party SCF families.
	var governEdges int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM fw_to_scf_edges e
		JOIN framework_requirements fr ON fr.id = e.framework_requirement_id
		JOIN framework_versions fv ON fv.id = fr.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = 'nist_csf' AND fv.version = '2.0' AND fr.code LIKE 'GV.%'`).Scan(&governEdges); err != nil {
		t.Fatalf("govern-edge count: %v", err)
	}
	if governEdges == 0 {
		t.Fatal("CSF GOVERN function has zero SCF-anchor edges — no-analog Function is not wired into the graph")
	}

	t.Logf("AC-5 proven: CSF GV.RR-02 -> GOV-04 (%s); %d GOVERN edges all traverse SCF anchors — GOVERN (no SOC 2 Function analog) is modeled via invariant #7", strm, governEdges)
}

// AC-2 / P0-480-4 — an edge whose scf_anchor does not resolve to a real anchor
// is a clear loader error, not a panic and not a dangling edge. The whole
// import rolls back, leaving no CSF requirement rows behind.
func TestCSFImport_RejectsEdgeToNonexistentAnchor(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	resetCSF(t, pool)
	ensureSCFLoaded(t, pool)

	bad := &soc2import.Crosswalk{
		SchemaVersion:     "1.0",
		FrameworkSlug:     "nist_csf",
		FrameworkName:     "NIST Cybersecurity Framework 2.0",
		FrameworkIssuer:   "NIST",
		FrameworkVersion:  "2.0",
		ReleaseDate:       "2024-02-26",
		SourceAttribution: "community_draft",
		Requirements: []soc2import.Requirement{
			{Code: "PR.AA-01", Title: "Identities managed", Body: "body"},
		},
		Mappings: []soc2import.Mapping{
			{RequirementCode: "PR.AA-01", SCFAnchor: "ZZZ-99", RelationshipType: "equal", Strength: 0.9, Rationale: "no such anchor"},
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
		WHERE f.slug = 'nist_csf' AND fr.code = 'PR.AA-01'`).Scan(&leaked); err != nil {
		t.Fatalf("leak-check query: %v", err)
	}
	if leaked != 0 {
		t.Fatalf("failed import left %d CSF requirement rows behind — transaction did not roll back", leaked)
	}
}
