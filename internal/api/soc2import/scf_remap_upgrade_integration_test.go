//go:build integration

package soc2import_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/api/soc2import"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// Issue #1971: the SCF 2026.3 IAC remap, applied to an install that already
// ran the pre-#1971 bootstrap. scfimport updates anchors in place by scf_id,
// so the IAC-10 row changes meaning from Authenticator Management to PAM. If
// the crosswalk import only upserted, HIPAA Password Management would keep
// its old edge on that row and the PAM control would falsely cover it. The
// import prunes the edges the crosswalk no longer lists.

// preRemap is the inverse of the #1971 remap: 2026.3 ID -> the pre-#1971
// sample ID and title.
var preRemap = map[string]struct{ id, title string }{
	"IAC-37":   {"IAC-06", "Multi-Factor Authentication (MFA)"},
	"IAC-04":   {"IAC-07", "User Provisioning & Lifecycle"},
	"IAC-14":   {"IAC-10", "Authenticator Management"},
	"IAC-12":   {"IAC-15", "Account Review"},
	"IAC-10":   {"IAC-21", "Privileged Account Management"},
	"IAC-11":   {"IAC-24", "Emergency Access (Break-Glass) Accounts"},
	"CFG-04.5": {"IAC-25", "Session Termination"},
}

// preRemapOnly are the pre-#1971 IDs that no 2026.3 sample row reuses.
var preRemapOnly = []string{"IAC-06", "IAC-07", "IAC-15", "IAC-21", "IAC-24", "IAC-25"}

const passwordManagement = "164.308(a)(5)(ii)(D)"

func loadSCFSample(t *testing.T) *scfimport.Catalog {
	t.Helper()
	cat, err := scfimport.Load(filepath.Join("..", "..", "..", "migrations", "fixtures", "scf-sample.json"))
	if err != nil {
		t.Fatalf("scfimport.Load(scf-sample.json): %v", err)
	}
	return cat
}

func importSCF(t *testing.T, pool *pgxpool.Pool, cat *scfimport.Catalog) {
	t.Helper()
	if _, err := scfimport.Import(context.Background(), pool, cat); err != nil {
		t.Fatalf("scfimport.Import: %v", err)
	}
}

// hipaaAnchors returns the scf_ids one HIPAA requirement maps to.
func hipaaAnchors(t *testing.T, pool *pgxpool.Pool, code string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT a.scf_id
		FROM fw_to_scf_edges e
		JOIN framework_requirements fr ON fr.id = e.framework_requirement_id
		JOIN framework_versions fv ON fv.id = fr.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		JOIN scf_anchors a ON a.id = e.scf_anchor_id
		WHERE f.slug = 'hipaa_security_rule' AND f.tenant_id IS NULL AND fr.code = $1
		ORDER BY a.scf_id`, code)
	if err != nil {
		t.Fatalf("query HIPAA %s anchors: %v", code, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

func TestSCFRemapUpgrade_PrunesEdgesLeftOnOldAnchors(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	ctx := context.Background()
	resetHIPAA(t, pool)
	ensureSCFLoaded(t, pool)
	t.Cleanup(func() {
		resetHIPAA(t, pool)
		if _, err := pool.Exec(ctx, `DELETE FROM scf_anchors WHERE scf_id = ANY($1)`, preRemapOnly); err != nil {
			t.Errorf("cleanup pre-#1971 anchors: %v", err)
		}
		importSCF(t, pool, loadSCFSample(t)) // restores the IAC-10 title
	})

	// Pre-#1971 state: the old sample IDs and titles, and the HIPAA crosswalk
	// anchored on them.
	old := loadSCFSample(t)
	for i, c := range old.Controls {
		if p, ok := preRemap[c.SCFID]; ok {
			old.Controls[i].SCFID, old.Controls[i].Title, old.Controls[i].Family = p.id, p.title, "IAC"
		}
	}
	importSCF(t, pool, old)
	oldCW := loadHIPAACrosswalk(t)
	for i, m := range oldCW.Mappings {
		if p, ok := preRemap[m.SCFAnchor]; ok {
			oldCW.Mappings[i].SCFAnchor = p.id
		}
	}
	if _, err := soc2import.Import(ctx, pool, oldCW); err != nil {
		t.Fatalf("pre-#1971 HIPAA Import: %v", err)
	}
	// A pre-#1971 install has no crosswalk_source on its edges.
	if _, err := pool.Exec(ctx, `
		UPDATE fw_to_scf_edges SET crosswalk_source = NULL
		WHERE framework_requirement_id IN (
			SELECT fr.id FROM framework_requirements fr
			JOIN framework_versions fv ON fv.id = fr.framework_version_id
			JOIN frameworks f ON f.id = fv.framework_id
			WHERE f.slug = 'hipaa_security_rule' AND f.tenant_id IS NULL)`); err != nil {
		t.Fatalf("clear crosswalk_source: %v", err)
	}
	if got := hipaaAnchors(t, pool, passwordManagement); !slices.Equal(got, []string{"IAC-10"}) {
		t.Fatalf("pre-#1971 Password Management anchors = %v; want [IAC-10] (the old Authenticator Management row)", got)
	}

	// Re-run the bootstrap: the current sample, then the current crosswalk.
	importSCF(t, pool, loadSCFSample(t))
	cw := loadHIPAACrosswalk(t)
	report, err := soc2import.Import(ctx, pool, cw)
	if err != nil {
		t.Fatalf("post-#1971 HIPAA Import: %v", err)
	}

	if got := hipaaAnchors(t, pool, passwordManagement); !slices.Equal(got, []string{"IAC-14"}) {
		t.Fatalf("Password Management anchors after upgrade = %v; want only [IAC-14]", got)
	}
	if len(report.EdgesPruned) == 0 {
		t.Fatal("upgrade import pruned no edges; the edges left on the old anchors survived")
	}
	for _, p := range report.EdgesPruned {
		if p.Attribution != "community_draft" || p.Source != soc2import.LegacySource {
			t.Errorf("pruned %s->%s (attribution %s, source %s); only the legacy community_draft edges may go", p.RequirementCode, p.SCFAnchor, p.Attribution, p.Source)
		}
	}

	// Every HIPAA edge now matches the crosswalk exactly: nothing is left on
	// a pre-#1971-only anchor, and IAC-10 carries only what the YAML maps to PAM.
	var stored int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM fw_to_scf_edges e
		JOIN framework_requirements fr ON fr.id = e.framework_requirement_id
		JOIN framework_versions fv ON fv.id = fr.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = 'hipaa_security_rule' AND f.tenant_id IS NULL`).Scan(&stored); err != nil {
		t.Fatalf("count HIPAA edges: %v", err)
	}
	if stored != len(cw.Mappings) {
		t.Fatalf("HIPAA edges after upgrade = %d; want exactly the crosswalk's %d", stored, len(cw.Mappings))
	}
	var onOld int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM fw_to_scf_edges e
		JOIN scf_anchors a ON a.id = e.scf_anchor_id
		WHERE a.scf_id = ANY($1)`, preRemapOnly).Scan(&onOld); err != nil {
		t.Fatalf("count edges on pre-#1971 anchors: %v", err)
	}
	if onOld != 0 {
		t.Fatalf("%d edges still point at pre-#1971-only anchors %v", onOld, preRemapOnly)
	}

	// The prune is idempotent: a second bootstrap removes nothing more.
	again, err := soc2import.Import(ctx, pool, cw)
	if err != nil {
		t.Fatalf("second post-#1971 HIPAA Import: %v", err)
	}
	if len(again.EdgesPruned) != 0 || again.EdgesUnchanged != len(cw.Mappings) {
		t.Fatalf("re-import pruned=%v unchanged=%d; want no prunes and %d unchanged", again.EdgesPruned, again.EdgesUnchanged, len(cw.Mappings))
	}
}

// An edge another source wrote is never pruned, even when the crosswalk does
// not list it: the importer owns only the provenance it writes.
func TestImport_KeepsUnlistedEdgeFromAnotherSource(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	ctx := context.Background()
	resetFrameworkCatalog(t, pool, provenanceSlug)
	t.Cleanup(func() { resetFrameworkCatalog(t, pool, provenanceSlug) })
	ensureSCFLoaded(t, pool)

	official := provenanceCrosswalk("scf_official", "official rationale")
	if _, err := soc2import.Import(ctx, pool, official); err != nil {
		t.Fatalf("official Import: %v", err)
	}
	community := provenanceCrosswalk("community_draft", "community rationale")
	community.Mappings[0].SCFAnchor = "IAC-01"
	report, err := soc2import.Import(ctx, pool, community)
	if err != nil {
		t.Fatalf("community Import: %v", err)
	}
	if len(report.EdgesPruned) != 0 {
		t.Fatalf("community import pruned %+v; the scf_official edge is not its to remove", report.EdgesPruned)
	}
	if len(report.EdgesUnlisted) != 1 || report.EdgesUnlisted[0].SCFAnchor != "GOV-01" || report.EdgesUnlisted[0].Attribution != "scf_official" {
		t.Fatalf("EdgesUnlisted = %+v; want the scf_official P-1->GOV-01 edge", report.EdgesUnlisted)
	}
	if got := readProvenanceEdgeTo(t, pool, "GOV-01"); got != "scf_official" {
		t.Fatalf("P-1->GOV-01 attribution = %q; want scf_official kept", got)
	}

	// A same-source crosswalk that drops the mapping does prune it.
	moved := provenanceCrosswalk("scf_official", "official rationale")
	moved.Mappings[0].SCFAnchor = "IAC-01"
	report, err = soc2import.Import(ctx, pool, moved)
	if err != nil {
		t.Fatalf("moved official Import: %v", err)
	}
	if len(report.EdgesPruned) != 1 || report.EdgesPruned[0].SCFAnchor != "GOV-01" {
		t.Fatalf("EdgesPruned = %+v; want the dropped P-1->GOV-01 edge", report.EdgesPruned)
	}
	if got := readProvenanceEdgeTo(t, pool, "GOV-01"); got != "" {
		t.Fatalf("P-1->GOV-01 still stored with attribution %q", got)
	}
}

// readProvenanceEdgeTo returns the attribution of the provenance fixture's
// P-1 edge to one anchor, or "" when there is none.
func readProvenanceEdgeTo(t *testing.T, pool *pgxpool.Pool, scfID string) string {
	t.Helper()
	var attr string
	err := pool.QueryRow(context.Background(), `
		SELECT e.source_attribution::text
		FROM fw_to_scf_edges e
		JOIN framework_requirements fr ON fr.id = e.framework_requirement_id
		JOIN framework_versions fv ON fv.id = fr.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		JOIN scf_anchors a ON a.id = e.scf_anchor_id
		WHERE f.slug = $1 AND f.tenant_id IS NULL AND fr.code = 'P-1' AND a.scf_id = $2`,
		provenanceSlug, scfID).Scan(&attr)
	if errors.Is(err, pgx.ErrNoRows) {
		return ""
	}
	if err != nil {
		t.Fatalf("read P-1->%s: %v", scfID, err)
	}
	return attr
}

// A YAML crosswalk and a workbook import can write into the same framework
// version, both as community_draft (docs/operator/scf-crosswalk-import.md,
// "Combining columns"). Each re-import prunes only the edges its own source
// wrote (fw_to_scf_edges.crosswalk_source, #1971).
const coexistSlug = "coexist-1971"

func coexistCrosswalk(source string, mappings ...[2]string) *soc2import.Crosswalk {
	cw := &soc2import.Crosswalk{
		SchemaVersion:     "1",
		FrameworkSlug:     coexistSlug,
		FrameworkName:     "Crosswalk source coexistence fixture",
		FrameworkIssuer:   "security-atlas tests",
		FrameworkVersion:  "1.0",
		SourceAttribution: "community_draft",
		Source:            source,
		Requirements: []soc2import.Requirement{
			{Code: "R-1", Title: "First", Body: "Fixture requirement."},
			{Code: "R-2", Title: "Second", Body: "Fixture requirement."},
		},
	}
	for _, m := range mappings {
		cw.Mappings = append(cw.Mappings, soc2import.Mapping{
			RequirementCode:  m[0],
			SCFAnchor:        m[1],
			RelationshipType: "intersects_with",
			Strength:         0.7,
			Rationale:        source + " " + m[0] + "->" + m[1],
		})
	}
	return cw
}

// coexistEdges returns "code->scf_id@source" for every edge of the fixture.
func coexistEdges(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT fr.code || '->' || a.scf_id || '@' || coalesce(e.crosswalk_source, '')
		FROM fw_to_scf_edges e
		JOIN framework_requirements fr ON fr.id = e.framework_requirement_id
		JOIN framework_versions fv ON fv.id = fr.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		JOIN scf_anchors a ON a.id = e.scf_anchor_id
		WHERE f.slug = $1 AND f.tenant_id IS NULL
		ORDER BY 1`, coexistSlug)
	if err != nil {
		t.Fatalf("query fixture edges: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

func TestImport_YAMLAndWorkbookPruneOnlyTheirOwnEdges(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	ctx := context.Background()
	resetFrameworkCatalog(t, pool, coexistSlug)
	t.Cleanup(func() { resetFrameworkCatalog(t, pool, coexistSlug) })
	ensureSCFLoaded(t, pool)

	const yamlSrc, wbSrc = "yaml:coexist.yaml", "scf-workbook:Example 2022"
	importCW := func(cw *soc2import.Crosswalk) soc2import.Report {
		t.Helper()
		r, err := soc2import.Import(ctx, pool, cw)
		if err != nil {
			t.Fatalf("Import %s: %v", cw.Source, err)
		}
		return r
	}
	importCW(coexistCrosswalk(yamlSrc, [2]string{"R-1", "GOV-01"}, [2]string{"R-2", "IAC-01"}))
	importCW(coexistCrosswalk(wbSrc, [2]string{"R-1", "IAC-37"}, [2]string{"R-2", "IAC-04"}))
	want := []string{
		"R-1->GOV-01@" + yamlSrc, "R-1->IAC-37@" + wbSrc,
		"R-2->IAC-01@" + yamlSrc, "R-2->IAC-04@" + wbSrc,
	}
	if got := coexistEdges(t, pool); !slices.Equal(got, want) {
		t.Fatalf("after both imports edges = %v; want %v", got, want)
	}

	// A YAML re-import that moves R-2 to IAC-14 prunes its own old edge and
	// leaves the workbook's edges untouched.
	r := importCW(coexistCrosswalk(yamlSrc, [2]string{"R-1", "GOV-01"}, [2]string{"R-2", "IAC-14"}))
	if len(r.EdgesPruned) != 1 || r.EdgesPruned[0].SCFAnchor != "IAC-01" || r.EdgesPruned[0].Source != yamlSrc {
		t.Fatalf("YAML re-import pruned %+v; want only its own R-2->IAC-01", r.EdgesPruned)
	}
	if len(r.EdgesUnlisted) != 2 {
		t.Fatalf("YAML re-import reported %d unlisted edges; want the workbook's 2", len(r.EdgesUnlisted))
	}
	want = []string{
		"R-1->GOV-01@" + yamlSrc, "R-1->IAC-37@" + wbSrc,
		"R-2->IAC-04@" + wbSrc, "R-2->IAC-14@" + yamlSrc,
	}
	if got := coexistEdges(t, pool); !slices.Equal(got, want) {
		t.Fatalf("after YAML re-import edges = %v; want %v", got, want)
	}

	// A workbook re-import that drops R-2 prunes its own edge and leaves the
	// YAML's edges untouched.
	r = importCW(coexistCrosswalk(wbSrc, [2]string{"R-1", "IAC-37"}))
	if len(r.EdgesPruned) != 1 || r.EdgesPruned[0].SCFAnchor != "IAC-04" || r.EdgesPruned[0].Source != wbSrc {
		t.Fatalf("workbook re-import pruned %+v; want only its own R-2->IAC-04", r.EdgesPruned)
	}
	want = []string{"R-1->GOV-01@" + yamlSrc, "R-1->IAC-37@" + wbSrc, "R-2->IAC-14@" + yamlSrc}
	if got := coexistEdges(t, pool); !slices.Equal(got, want) {
		t.Fatalf("after workbook re-import edges = %v; want %v", got, want)
	}

	// A workbook import never prunes an edge with no recorded source: those
	// predate #1971 and belong to the YAML crosswalk.
	if _, err := pool.Exec(ctx, `
		UPDATE fw_to_scf_edges SET crosswalk_source = NULL
		WHERE crosswalk_source = $1`, yamlSrc); err != nil {
		t.Fatalf("clear YAML sources: %v", err)
	}
	r = importCW(coexistCrosswalk(wbSrc, [2]string{"R-1", "IAC-37"}))
	if len(r.EdgesPruned) != 0 {
		t.Fatalf("workbook import pruned legacy edges %+v", r.EdgesPruned)
	}
	r = importCW(coexistCrosswalk(yamlSrc, [2]string{"R-1", "GOV-01"}))
	if len(r.EdgesPruned) != 1 || r.EdgesPruned[0].SCFAnchor != "IAC-14" || r.EdgesPruned[0].Source != soc2import.LegacySource {
		t.Fatalf("YAML import pruned %+v; want the legacy R-2->IAC-14", r.EdgesPruned)
	}
}
