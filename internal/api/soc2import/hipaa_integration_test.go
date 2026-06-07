//go:build integration

package soc2import_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/soc2import"
)

// Slice 481 integration suite — the FRAMEWORK-AGNOSTIC crosswalk importer
// (slice 438) proven against a real Postgres with a FIFTH framework (HIPAA
// Security Rule). This extends the slice-438/447/480 invariant-#1 proof from
// four frameworks to five: a single shared SCF anchor (IAC-01) resolves to a
// SOC 2 criterion, an ISO Annex A control, a PCI requirement, a CSF
// Subcategory, AND a HIPAA Technical Safeguard standard through that ONE anchor
// row — one control, N framework satisfactions, no per-framework duplication
// and no requirement -> requirement edge (invariants #1 + #7).
//
// CATALOG, NOT WORKFLOW (P0-481-1): this suite proves only the catalog read
// path (requirement -> SCF anchor edges + STRM type). It does NOT exercise any
// covered-entity workflow, BAA tracking, required-vs-addressable decision flow,
// or breach risk-assessment — all deferred to the canvas §10.3 phase-3 slice.
//
// AC-5 (load-bearing, regulatory-weight confidentiality): the HIPAA anchors
// read returns catalog reference data ONLY — never any tenant-scoped field.
// HIPAA governs ePHI, so a leak here would be a confidentiality failure with
// regulatory weight; TestHIPAAImport_AnchorsReadCarriesNoTenantScopedData makes
// that property an explicit assertion (P0-481-5; threat-model I).

func loadHIPAACrosswalk(t *testing.T) *soc2import.Crosswalk {
	t.Helper()
	cw, err := soc2import.Load(filepath.Join("..", "..", "..", "data", "crosswalks", "hipaa-security-rule.yaml"))
	if err != nil {
		t.Fatalf("soc2import.Load(hipaa-security-rule.yaml): %v", err)
	}
	return cw
}

// resetHIPAA wipes the HIPAA framework rows (mirrors resetCSF/resetISO/resetPCI)
// so each test starts clean. Catalog tables are not tenant-scoped, so this is a
// plain DELETE; fw_to_scf_edges + framework_requirements cascade off
// framework_versions.
func resetHIPAA(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
		`DELETE FROM fw_to_scf_edges WHERE framework_requirement_id IN (
			SELECT fr.id FROM framework_requirements fr
			JOIN framework_versions fv ON fv.id = fr.framework_version_id
			JOIN frameworks f ON f.id = fv.framework_id
			WHERE f.slug = 'hipaa_security_rule')`,
		`DELETE FROM framework_requirements WHERE framework_version_id IN (
			SELECT fv.id FROM framework_versions fv
			JOIN frameworks f ON f.id = fv.framework_id
			WHERE f.slug = 'hipaa_security_rule')`,
		`DELETE FROM framework_versions WHERE framework_id IN (
			SELECT id FROM frameworks WHERE slug = 'hipaa_security_rule' AND tenant_id IS NULL)`,
		`DELETE FROM frameworks WHERE slug = 'hipaa_security_rule' AND tenant_id IS NULL`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("resetHIPAA %q: %v", stmt, err)
		}
	}
}

// AC-2 + AC-6 — importing the HIPAA crosswalk creates framework_requirements +
// fw_to_scf_edges rows for the HIPAA Security Rule, and re-importing is
// idempotent.
func TestHIPAAImport_CreatesRowsAndIsIdempotent(t *testing.T) {
	pool := openPool(t)
	resetHIPAA(t, pool)
	ensureSCFLoaded(t, pool)
	cw := loadHIPAACrosswalk(t)

	report, err := soc2import.Import(context.Background(), pool, cw)
	if err != nil {
		t.Fatalf("HIPAA Import: %v", err)
	}
	if !report.IsNewVersion {
		t.Fatal("first HIPAA import should be a new version")
	}
	if report.FrameworkSlug != "hipaa_security_rule" || report.FrameworkVersion != "2013-01-25" {
		t.Fatalf("report framework = %s:%s; want hipaa_security_rule:2013-01-25", report.FrameworkSlug, report.FrameworkVersion)
	}
	if report.RequirementsCreated != len(cw.Requirements) {
		t.Fatalf("HIPAA requirements created = %d; want %d", report.RequirementsCreated, len(cw.Requirements))
	}
	if report.EdgesCreated != len(cw.Mappings) {
		t.Fatalf("HIPAA edges created = %d; want %d", report.EdgesCreated, len(cw.Mappings))
	}

	// Re-import is a no-op (idempotent).
	report2, err := soc2import.Import(context.Background(), pool, cw)
	if err != nil {
		t.Fatalf("second HIPAA Import: %v", err)
	}
	if report2.RequirementsCreated != 0 || report2.EdgesCreated != 0 {
		t.Fatalf("idempotent re-import created rows: %+v", report2)
	}
	if report2.EdgesUnchanged != len(cw.Mappings) {
		t.Fatalf("re-import edges unchanged = %d; want %d", report2.EdgesUnchanged, len(cw.Mappings))
	}
}

// AC-2 / AC-7 — importing HIPAA does NOT disturb the SOC 2 / ISO / PCI / CSF
// rows; all five frameworks coexist in the same graph with distinct
// framework_version_ids.
func TestHIPAAImport_DoesNotDisturbOtherFrameworks(t *testing.T) {
	pool := openPool(t)
	resetCatalog(t, pool)
	resetISO(t, pool)
	resetPCI(t, pool)
	resetCSF(t, pool)
	resetHIPAA(t, pool)
	ensureSCFLoaded(t, pool)

	soc2 := loadCrosswalk(t)
	iso := loadISOCrosswalk(t)
	pci := loadPCICrosswalk(t)
	csf := loadCSFCrosswalk(t)
	hipaa := loadHIPAACrosswalk(t)

	if _, err := soc2import.Import(context.Background(), pool, soc2); err != nil {
		t.Fatalf("SOC 2 Import: %v", err)
	}
	if _, err := soc2import.Import(context.Background(), pool, iso); err != nil {
		t.Fatalf("ISO Import: %v", err)
	}
	if _, err := soc2import.Import(context.Background(), pool, pci); err != nil {
		t.Fatalf("PCI Import: %v", err)
	}
	if _, err := soc2import.Import(context.Background(), pool, csf); err != nil {
		t.Fatalf("CSF Import: %v", err)
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
	csfBefore := countEdges("nist_csf")

	if _, err := soc2import.Import(context.Background(), pool, hipaa); err != nil {
		t.Fatalf("HIPAA Import: %v", err)
	}

	// SOC 2 / ISO / PCI / CSF edge counts are unchanged after the HIPAA import (AC-2).
	if got := countEdges("soc2"); got != soc2Before {
		t.Fatalf("HIPAA import disturbed SOC 2 edges: before=%d after=%d", soc2Before, got)
	}
	if got := countEdges("iso27001"); got != isoBefore {
		t.Fatalf("HIPAA import disturbed ISO edges: before=%d after=%d", isoBefore, got)
	}
	if got := countEdges("pcidss"); got != pciBefore {
		t.Fatalf("HIPAA import disturbed PCI edges: before=%d after=%d", pciBefore, got)
	}
	if got := countEdges("nist_csf"); got != csfBefore {
		t.Fatalf("HIPAA import disturbed CSF edges: before=%d after=%d", csfBefore, got)
	}

	// Five distinct framework_version rows now exist.
	var distinctVersions int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(DISTINCT fv.id) FROM framework_versions fv
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug IN ('soc2', 'iso27001', 'pcidss', 'nist_csf', 'hipaa_security_rule')`).Scan(&distinctVersions); err != nil {
		t.Fatalf("count distinct versions: %v", err)
	}
	if distinctVersions < 5 {
		t.Fatalf("expected >=5 distinct framework_versions; got %d", distinctVersions)
	}
}

// AC-3 — for a HIPAA requirement, the requirement resolves to its SCF anchor(s)
// with the STRM edge type. Mirrors the query the GET
// /v1/requirements/{id}/anchors read path runs. HIPAA 164.312(a)(1) -> IAC-01.
func TestHIPAAImport_RequirementResolvesToAnchorsWithSTRMType(t *testing.T) {
	pool := openPool(t)
	resetHIPAA(t, pool)
	ensureSCFLoaded(t, pool)
	if _, err := soc2import.Import(context.Background(), pool, loadHIPAACrosswalk(t)); err != nil {
		t.Fatalf("HIPAA Import: %v", err)
	}

	rows, err := pool.Query(context.Background(), `
		SELECT a.scf_id, e.relationship_type::text
		FROM framework_requirements fr
		JOIN framework_versions fv ON fv.id = fr.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		JOIN fw_to_scf_edges e ON e.framework_requirement_id = fr.id
		JOIN scf_anchors a ON a.id = e.scf_anchor_id
		WHERE f.slug = 'hipaa_security_rule' AND fv.version = '2013-01-25' AND fr.code = '164.312(a)(1)'`)
	if err != nil {
		t.Fatalf("anchors-for-HIPAA-requirement query: %v", err)
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
		t.Fatal("HIPAA requirement 164.312(a)(1) resolved to ZERO anchors — read path broken")
	}
	if anchors[0] != "IAC-01" {
		t.Fatalf("164.312(a)(1) should map to IAC-01; got %v", anchors)
	}
	if strm == "" {
		t.Fatal("edge resolved with empty STRM relationship_type")
	}
}

// AC-4 — THE LOAD-BEARING TEST (five-framework extension of the slices
// 438/447/480 invariant-#1 proof). One SCF anchor (IAC-01) is shared between a
// SOC 2 criterion (CC6.1), an ISO Annex A control (A.5.15), a PCI requirement
// (8.2.1), a CSF Subcategory (PR.AA-01), AND a HIPAA Technical Safeguard
// standard (164.312(a)(1)). The anchor resolves to ALL FIVE framework
// satisfactions through the single shared anchor row. This is invariant #1
// demonstrated at five frameworks: one control, N frameworks, NO per-framework
// duplicated control and NO requirement -> requirement edge.
func TestHIPAAImport_SharedAnchorSatisfiesFiveFrameworks_Invariant1(t *testing.T) {
	pool := openPool(t)
	resetCatalog(t, pool)
	resetISO(t, pool)
	resetPCI(t, pool)
	resetCSF(t, pool)
	resetHIPAA(t, pool)
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
	if _, err := soc2import.Import(context.Background(), pool, loadHIPAACrosswalk(t)); err != nil {
		t.Fatalf("HIPAA Import: %v", err)
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

	// The single anchor must satisfy a requirement in ALL FIVE frameworks —
	// through the SAME anchor row.
	for _, fw := range []string{"soc2", "iso27001", "pcidss", "nist_csf", "hipaa_security_rule"} {
		if len(byFramework[fw]) == 0 {
			t.Fatalf("invariant #1 unproven: anchor %s satisfies no %s requirement", sharedAnchor, fw)
		}
	}
	// Concretely: SOC 2 CC6.1, ISO A.5.15, PCI 8.2.1, CSF PR.AA-01, and HIPAA
	// 164.312(a)(1) all map to IAC-01.
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
	if !contains(byFramework["hipaa_security_rule"], "164.312(a)(1)") {
		t.Fatalf("expected HIPAA 164.312(a)(1) among %s's HIPAA satisfactions; got %v", sharedAnchor, byFramework["hipaa_security_rule"])
	}

	// Belt-and-suspenders for invariant #1 / P0-481-4: there is exactly ONE
	// scf_anchors row for IAC-01 (no per-framework duplication of the control).
	// All five framework satisfactions traverse that single row.
	var anchorRowCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM scf_anchors WHERE scf_id = $1`, sharedAnchor).Scan(&anchorRowCount); err != nil {
		t.Fatalf("count anchor rows: %v", err)
	}
	if anchorRowCount != 1 {
		t.Fatalf("invariant #1 violated: %d scf_anchors rows for %s; expected exactly 1 (one control, N frameworks)", anchorRowCount, sharedAnchor)
	}

	t.Logf("invariant #1 proven at FIVE frameworks: single anchor %s satisfies SOC 2 %v AND ISO %v AND PCI %v AND CSF %v AND HIPAA %v",
		sharedAnchor, byFramework["soc2"], byFramework["iso27001"], byFramework["pcidss"], byFramework["nist_csf"], byFramework["hipaa_security_rule"])
}

// AC-5 — THE LOAD-BEARING CONFIDENTIALITY ASSERTION (P0-481-5; threat-model I).
// HIPAA governs ePHI, so the read-path payload for a HIPAA requirement's anchors
// MUST be catalog reference data ONLY — never any tenant-scoped field. A leak
// here would be a confidentiality failure with regulatory weight. This test
// runs the exact projection the GET /v1/requirements/{id}/anchors read path
// returns for the catalog (anchorWire) and asserts every projected column is
// catalog reference data: SCF anchor identity (scf_id, family, title,
// description), the STRM edge facts (relationship_type, strength,
// source_attribution, rationale). Tenant-scoped control-implementation state
// (lifecycle, ownership, freshness) is reachable ONLY via the explicit
// ?include=state opt-in (slice 104), which carries RLS — it is NOT in this
// catalog projection. The assertion is column-exhaustive: any new tenant-scoped
// column added to this projection would fail the test.
func TestHIPAAImport_AnchorsReadCarriesNoTenantScopedData(t *testing.T) {
	pool := openPool(t)
	resetHIPAA(t, pool)
	ensureSCFLoaded(t, pool)
	if _, err := soc2import.Import(context.Background(), pool, loadHIPAACrosswalk(t)); err != nil {
		t.Fatalf("HIPAA Import: %v", err)
	}

	// This SELECT is the catalog read-path projection (the anchorWire shape):
	// SCF anchor identity + STRM edge facts. It contains NO tenant column, no
	// join to any tenant-scoped table (controls, evidence, control state), and
	// no tenant_id predicate. The query deliberately does NOT reference
	// app.current_tenant — the catalog is shared, non-tenant reference data.
	rows, err := pool.Query(context.Background(), `
		SELECT a.scf_id, a.family, a.title, a.description,
		       e.relationship_type::text, e.strength, e.source_attribution::text,
		       coalesce(e.rationale, '')
		FROM framework_requirements fr
		JOIN framework_versions fv ON fv.id = fr.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		JOIN fw_to_scf_edges e ON e.framework_requirement_id = fr.id
		JOIN scf_anchors a ON a.id = e.scf_anchor_id
		WHERE f.slug = 'hipaa_security_rule' AND fr.code = '164.312(a)(1)'`)
	if err != nil {
		t.Fatalf("catalog read-path query: %v", err)
	}
	defer rows.Close()

	// Assert the projected column set is exactly the catalog reference columns —
	// NO tenant-scoped field is present. pgx exposes the result column
	// descriptions; we check them against an allow-list so a future projection
	// change that leaks a tenant column fails loudly.
	allowedCols := map[string]bool{
		"scf_id": true, "family": true, "title": true, "description": true,
		"relationship_type": true, "strength": true, "source_attribution": true,
		"coalesce": true, "rationale": true,
	}
	bannedSubstrings := []string{"tenant", "owner", "lifecycle", "freshness", "evidence", "implementation"}
	for _, fd := range rows.FieldDescriptions() {
		name := strings.ToLower(string(fd.Name))
		for _, banned := range bannedSubstrings {
			if strings.Contains(name, banned) {
				t.Fatalf("AC-5 CONFIDENTIALITY FAILURE: catalog anchors projection contains tenant-scoped column %q (matched banned token %q)", name, banned)
			}
		}
		if !allowedCols[name] {
			t.Fatalf("AC-5: unexpected column %q in HIPAA catalog anchors projection — confirm it is catalog reference data, not tenant-scoped, before allow-listing", name)
		}
	}

	var anchorCount int
	for rows.Next() {
		var scfID, family, title, desc, rt, src, rationale string
		var strength float64
		if err := rows.Scan(&scfID, &family, &title, &desc, &rt, &strength, &src, &rationale); err != nil {
			t.Fatalf("scan: %v", err)
		}
		anchorCount++
		if scfID == "" || rt == "" {
			t.Fatalf("catalog row missing reference data: scf_id=%q strm=%q", scfID, rt)
		}
	}
	if anchorCount == 0 {
		t.Fatal("HIPAA 164.312(a)(1) catalog read returned no anchors — cannot assert AC-5 on an empty payload")
	}
	t.Logf("AC-5 proven: HIPAA catalog anchors payload for 164.312(a)(1) carries only catalog reference data across %d row(s); no tenant-scoped field present", anchorCount)
}

// AC-2 / P0-481-4 — an edge whose scf_anchor does not resolve to a real anchor
// is a clear loader error, not a panic and not a dangling edge. The whole
// import rolls back, leaving no HIPAA requirement rows behind.
func TestHIPAAImport_RejectsEdgeToNonexistentAnchor(t *testing.T) {
	pool := openPool(t)
	resetHIPAA(t, pool)
	ensureSCFLoaded(t, pool)

	bad := &soc2import.Crosswalk{
		SchemaVersion:     "1.0",
		FrameworkSlug:     "hipaa_security_rule",
		FrameworkName:     "HIPAA Security Rule (45 CFR Part 164, Subpart C)",
		FrameworkIssuer:   "HHS",
		FrameworkVersion:  "2013-01-25",
		SourceAttribution: "community_draft",
		Requirements: []soc2import.Requirement{
			{Code: "164.312(a)(1)", Title: "Access Control", Body: "body"},
		},
		Mappings: []soc2import.Mapping{
			{RequirementCode: "164.312(a)(1)", SCFAnchor: "ZZZ-99", RelationshipType: "equal", Strength: 0.9, Rationale: "no such anchor"},
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
		WHERE f.slug = 'hipaa_security_rule' AND fr.code = '164.312(a)(1)'`).Scan(&leaked); err != nil {
		t.Fatalf("leak-check query: %v", err)
	}
	if leaked != 0 {
		t.Fatalf("failed import left %d HIPAA requirement rows behind — transaction did not roll back", leaked)
	}
}
