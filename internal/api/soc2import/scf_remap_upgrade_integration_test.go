//go:build integration

package soc2import_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/api/soc2import"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// Issue #1971: the SCF 2026.3 IAC remap, applied to an install that already
// ran the pre-#1971 bootstrap. The sample moves to a new release label
// (test-2026.1 -> test-2026.3), so scfimport mints a new framework version
// with new anchor rows and leaves the old one legacy (#1768). The crosswalk
// import then retires every edge it wrote to the legacy rows and writes the
// current mapping to the new ones. Without the retire pass, HIPAA Password
// Management would keep its edge to the legacy Authenticator Management row.

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

const (
	passwordManagement = "164.308(a)(5)(ii)(D)"
	preRemapRelease    = "test-2026.1"
	hipaaSource        = "yaml:hipaa-security-rule.yaml"
)

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
		t.Fatalf("scfimport.Import(%s): %v", cat.ReleaseVersion, err)
	}
}

// wipeSCFReleases removes every SCF release and the frameworks this test
// imports, so the test can start from the pre-#1971 release alone. Same
// statements and FK order as scfseed's full reseed, plus HIPAA.
func wipeSCFReleases(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	for _, stmt := range []string{
		`TRUNCATE controls RESTART IDENTITY CASCADE`,
		`DELETE FROM fw_to_scf_edges`,
		`DELETE FROM framework_requirements`,
		`DELETE FROM scf_anchors`,
		`DELETE FROM framework_versions WHERE framework_id IN (
			SELECT id FROM frameworks WHERE slug IN ('scf', 'soc2', 'hipaa_security_rule') AND tenant_id IS NULL)`,
		`DELETE FROM frameworks WHERE slug IN ('scf', 'soc2', 'hipaa_security_rule') AND tenant_id IS NULL`,
	} {
		if _, err := pool.Exec(context.Background(), stmt); err != nil {
			t.Fatalf("wipe %q: %v", stmt, err)
		}
	}
}

// hipaaAnchors returns "scf_id@release" for the live edges of one HIPAA
// requirement.
func hipaaAnchors(t *testing.T, pool *pgxpool.Pool, code string) []string {
	t.Helper()
	return queryStrings(t, pool, `
		SELECT a.scf_id || '@' || afv.version
		FROM effective_fw_to_scf_edges e
		JOIN framework_requirements fr ON fr.id = e.framework_requirement_id
		JOIN framework_versions fv ON fv.id = fr.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		JOIN scf_anchors a ON a.id = e.scf_anchor_id
		JOIN framework_versions afv ON afv.id = a.framework_version_id
		WHERE f.slug = 'hipaa_security_rule' AND f.tenant_id IS NULL AND fr.code = $1
		ORDER BY 1`, code)
}

func queryStrings(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), sql, args...)
	if err != nil {
		t.Fatalf("query: %v", err)
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

func countRows(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

const hipaaEdgesWhere = `
	FROM fw_to_scf_edges e
	JOIN framework_requirements fr ON fr.id = e.framework_requirement_id
	JOIN framework_versions fv ON fv.id = fr.framework_version_id
	JOIN frameworks f ON f.id = fv.framework_id
	JOIN scf_anchors a ON a.id = e.scf_anchor_id
	JOIN framework_versions afv ON afv.id = a.framework_version_id
	WHERE f.slug = 'hipaa_security_rule' AND f.tenant_id IS NULL`

func TestSCFRemapUpgrade_RetiresEdgesLeftOnTheOldRelease(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	ctx := context.Background()
	wipeSCFReleases(t, pool)
	t.Cleanup(func() {
		wipeSCFReleases(t, pool)
		ensureSCFLoaded(t, pool) // the shared catalog the other tests expect
	})

	// Pre-#1971 install: the old sample IDs and titles under the old label,
	// and the HIPAA crosswalk anchored on them, with no crosswalk_source.
	old := loadSCFSample(t)
	old.ReleaseVersion = preRemapRelease
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
	if _, err := pool.Exec(ctx, `UPDATE fw_to_scf_edges SET crosswalk_source = NULL`); err != nil {
		t.Fatalf("clear crosswalk_source: %v", err)
	}
	if got := hipaaAnchors(t, pool, passwordManagement); !slices.Equal(got, []string{"IAC-10@" + preRemapRelease}) {
		t.Fatalf("pre-#1971 Password Management anchors = %v; want [IAC-10@%s]", got, preRemapRelease)
	}

	// A tenant's controls on the pre-#1971 release: IAC-01 keeps its code
	// and title, IAC-10 keeps its code but changes meaning (Authenticator
	// Management -> PAM), IAC-21 is gone from the new release.
	tenant := dbtest.SeedTenant(t, pool, "controls")
	tenantControls := map[string]uuid.UUID{}
	for _, code := range []string{"IAC-01", "IAC-10", "IAC-21"} {
		tenantControls[code] = insertTenantControl(t, pool, tenant, code)
	}

	// The upgraded bootstrap: the current sample (a new release label, so a
	// new framework version), then the current crosswalk.
	cur := loadSCFSample(t)
	if cur.ReleaseVersion == preRemapRelease {
		t.Fatalf("scf-sample.json is still labelled %s; its content changed, so it needs a new release_version", preRemapRelease)
	}
	importSCF(t, pool, cur)
	if got := countRows(t, pool, `
		SELECT count(*) FROM framework_versions fv JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = 'scf' AND f.tenant_id IS NULL AND fv.version = $1 AND fv.status = 'legacy'`, preRemapRelease); got != 1 {
		t.Fatalf("the %s release is not legacy after the upgrade import", preRemapRelease)
	}
	assertTenantControlsAfterUpgrade(t, pool, tenant, tenantControls, cur.ReleaseVersion)
	cw := loadHIPAACrosswalk(t)
	report, err := soc2import.Import(ctx, pool, cw)
	if err != nil {
		t.Fatalf("post-#1971 HIPAA Import: %v", err)
	}

	if got := hipaaAnchors(t, pool, passwordManagement); !slices.Equal(got, []string{"IAC-14@" + cur.ReleaseVersion}) {
		t.Fatalf("Password Management anchors after upgrade = %v; want only [IAC-14@%s]", got, cur.ReleaseVersion)
	}

	// Every edge to the legacy release is retired, not deleted, and its
	// retirement is on the edge's tier history.
	if len(report.EdgesRetired) != len(oldCW.Mappings) {
		t.Fatalf("upgrade retired %d edges; want all %d edges to the legacy release", len(report.EdgesRetired), len(oldCW.Mappings))
	}
	for _, r := range report.EdgesRetired {
		if r.Source != soc2import.LegacySource || r.Attribution != "community_draft" {
			t.Errorf("retired %s->%s (source %s, attribution %s); want only the legacy community_draft edges", r.RequirementCode, r.SCFAnchor, r.Source, r.Attribution)
		}
	}
	if got := countRows(t, pool, `SELECT count(*)`+hipaaEdgesWhere+` AND e.retired_at IS NOT NULL AND afv.version = $1`, preRemapRelease); got != len(oldCW.Mappings) {
		t.Fatalf("retired legacy edges kept = %d; want %d", got, len(oldCW.Mappings))
	}
	if got := countRows(t, pool, `
		SELECT count(*) FROM fw_to_scf_edge_tier_transitions tt
		JOIN fw_to_scf_edges e ON e.id = tt.edge_id
		WHERE e.retired_at IS NOT NULL AND tt.reviewer_id = $1 AND tt.note = $2`,
		soc2import.SystemReviewerID, "retired: dropped by crosswalk "+hipaaSource); got != len(oldCW.Mappings) {
		t.Fatalf("retirement transition rows = %d; want one per retired edge (%d)", got, len(oldCW.Mappings))
	}

	// The live edge set is exactly the crosswalk, all on the current release.
	if got := countRows(t, pool, `SELECT count(*)`+hipaaEdgesWhere+` AND e.retired_at IS NULL`); got != len(cw.Mappings) {
		t.Fatalf("live HIPAA edges = %d; want the crosswalk's %d", got, len(cw.Mappings))
	}
	if got := countRows(t, pool, `SELECT count(*)`+hipaaEdgesWhere+` AND e.retired_at IS NULL AND afv.status <> 'current'`); got != 0 {
		t.Fatalf("%d live HIPAA edges point at a non-current SCF release", got)
	}

	// Idempotent: a second boot retires nothing more.
	again, err := soc2import.Import(ctx, pool, cw)
	if err != nil {
		t.Fatalf("second post-#1971 HIPAA Import: %v", err)
	}
	if len(again.EdgesRetired) != 0 || len(again.EdgesRestored) != 0 || again.EdgesUnchanged != len(cw.Mappings) {
		t.Fatalf("re-import retired=%d restored=%d unchanged=%d; want 0, 0 and %d",
			len(again.EdgesRetired), len(again.EdgesRestored), again.EdgesUnchanged, len(cw.Mappings))
	}
}

// insertTenantControl binds an active control for tenant to the current SCF
// release's anchor for code, and returns its id.
func insertTenantControl(t *testing.T, pool *pgxpool.Pool, tenant, code string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO controls
			(id, tenant_id, bundle_id, version, superseded_by, scf_id, scf_anchor_id,
			 title, description, control_family, implementation_type, owner_role,
			 lifecycle_state, applicability_expr, evidence_queries,
			 manual_evidence_schema, linked_policy_ids, freshness_class,
			 bundle_manifest_yaml, bundle_manifest_hash, bundle_uploaded_at, bundle_uploaded_by)
		SELECT $1, $2, 'tenant-' || lower($3), 1, NULL, a.scf_id, a.id,
			'Tenant control on ' || $3, '', 'IAC', 'automated', '',
			'active', '{"true": true}', '{}'::jsonb,
			'{}'::jsonb, '{}', NULL,
			'', 'hash-' || $3, now(), 'test-1971'
		FROM scf_anchors a
		JOIN framework_versions fv ON fv.id = a.framework_version_id AND fv.status = 'current'
		JOIN frameworks f ON f.id = fv.framework_id AND f.slug = 'scf' AND f.tenant_id IS NULL
		WHERE a.scf_id = $3`, id, tenant, code); err != nil {
		t.Fatalf("insert tenant control on %s: %v", code, err)
	}
	return id
}

// assertTenantControlsAfterUpgrade checks Matt's #1971 D3 rule after the new
// SCF release is imported: the control whose anchor kept its code and title
// moves to the new release's anchor; the others stay put and are listed as
// needing re-anchoring, with the remap note from data/scfremap.
func assertTenantControlsAfterUpgrade(t *testing.T, pool *pgxpool.Pool, tenant string, controls map[string]uuid.UUID, release string) {
	t.Helper()
	ctx := context.Background()
	rr, err := scfimport.RebindControls(ctx, pool)
	if err != nil {
		t.Fatalf("RebindControls: %v", err)
	}
	if len(rr.Rebound) != 1 || rr.Rebound[0].ControlID != controls["IAC-01"] {
		t.Fatalf("Rebound = %+v; want only the IAC-01 control", rr.Rebound)
	}
	notes := map[uuid.UUID]string{}
	for _, f := range rr.NeedsReanchoring {
		notes[f.ControlID] = f.Note
	}
	wantNotes := map[uuid.UUID]string{
		controls["IAC-10"]: "IAC-10 -> IAC-14 in " + release + " (legacy)",
		controls["IAC-21"]: "IAC-21 -> IAC-10 in " + release + " (concept)",
	}
	if len(notes) != len(wantNotes) {
		t.Fatalf("NeedsReanchoring = %+v; want the IAC-10 and IAC-21 controls", rr.NeedsReanchoring)
	}
	for id, want := range wantNotes {
		if notes[id] != want {
			t.Errorf("note for control %s = %q; want %q", id, notes[id], want)
		}
	}
	bound := func(id uuid.UUID) string {
		t.Helper()
		return queryStrings(t, pool, `
			SELECT a.scf_id || '@' || fv.version FROM controls c
			JOIN scf_anchors a ON a.id = c.scf_anchor_id
			JOIN framework_versions fv ON fv.id = a.framework_version_id
			WHERE c.id = $1`, id)[0]
	}
	if got := bound(controls["IAC-01"]); got != "IAC-01@"+release {
		t.Errorf("IAC-01 control bound to %s; want IAC-01@%s", got, release)
	}
	if got := bound(controls["IAC-10"]); got != "IAC-10@"+preRemapRelease {
		t.Errorf("IAC-10 control bound to %s; want it left on IAC-10@%s, not moved to the new IAC-10 (PAM)", got, preRemapRelease)
	}

	// The per-tenant list the warning surface reads holds exactly the two.
	rows, err := dbx.New(pool).ListControlsOnLegacySCFAnchors(ctx, pgtype.UUID{Bytes: uuid.MustParse(tenant), Valid: true})
	if err != nil {
		t.Fatalf("ListControlsOnLegacySCFAnchors: %v", err)
	}
	var listed []string
	for _, r := range rows {
		listed = append(listed, r.LegacyScfID)
	}
	slices.Sort(listed)
	if !slices.Equal(listed, []string{"IAC-10", "IAC-21"}) {
		t.Fatalf("controls needing re-anchoring for the tenant = %v; want [IAC-10 IAC-21]", listed)
	}

	// Idempotent: a second boot moves nothing more and still flags both.
	again, err := scfimport.RebindControls(ctx, pool)
	if err != nil {
		t.Fatalf("second RebindControls: %v", err)
	}
	if len(again.Rebound) != 0 || len(again.NeedsReanchoring) != 2 {
		t.Fatalf("second RebindControls = %d rebound, %d flagged; want 0 and 2", len(again.Rebound), len(again.NeedsReanchoring))
	}
}

// readProvenanceEdge returns the attribution and retired state of the
// provenance fixture's P-1 edge to one anchor, or ok=false when there is none.
func readProvenanceEdgeTo(t *testing.T, pool *pgxpool.Pool, scfID string) (attr string, retired bool, ok bool) {
	t.Helper()
	err := pool.QueryRow(context.Background(), `
		SELECT e.source_attribution::text, e.retired_at IS NOT NULL
		FROM fw_to_scf_edges e
		JOIN framework_requirements fr ON fr.id = e.framework_requirement_id
		JOIN framework_versions fv ON fv.id = fr.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		JOIN scf_anchors a ON a.id = e.scf_anchor_id
		WHERE f.slug = $1 AND f.tenant_id IS NULL AND fr.code = 'P-1' AND a.scf_id = $2`,
		provenanceSlug, scfID).Scan(&attr, &retired)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, false
	}
	if err != nil {
		t.Fatalf("read P-1->%s: %v", scfID, err)
	}
	return attr, retired, true
}

// An edge another source wrote is never retired, even when the crosswalk
// does not list it: the importer owns only the provenance it writes.
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
	if len(report.EdgesRetired) != 0 {
		t.Fatalf("community import retired %+v; the scf_official edge is not its to retire", report.EdgesRetired)
	}
	if len(report.EdgesUnlisted) != 1 || report.EdgesUnlisted[0].SCFAnchor != "GOV-01" || report.EdgesUnlisted[0].Attribution != "scf_official" {
		t.Fatalf("EdgesUnlisted = %+v; want the scf_official P-1->GOV-01 edge", report.EdgesUnlisted)
	}
	if attr, retired, _ := readProvenanceEdgeTo(t, pool, "GOV-01"); attr != "scf_official" || retired {
		t.Fatalf("P-1->GOV-01 = %q retired=%v; want the scf_official edge kept live", attr, retired)
	}

	// A same-source crosswalk that drops the mapping does retire it.
	moved := provenanceCrosswalk("scf_official", "official rationale")
	moved.Mappings[0].SCFAnchor = "IAC-01"
	report, err = soc2import.Import(ctx, pool, moved)
	if err != nil {
		t.Fatalf("moved official Import: %v", err)
	}
	if len(report.EdgesRetired) != 1 || report.EdgesRetired[0].SCFAnchor != "GOV-01" {
		t.Fatalf("EdgesRetired = %+v; want the dropped P-1->GOV-01 edge", report.EdgesRetired)
	}
	if _, retired, ok := readProvenanceEdgeTo(t, pool, "GOV-01"); !ok || !retired {
		t.Fatalf("P-1->GOV-01 ok=%v retired=%v; want the row kept and retired", ok, retired)
	}
}

// Retiring an edge keeps its row and its append-only tier history, takes it
// out of coverage, and a crosswalk that lists it again restores it (#1971).
func TestImport_RetiredEdgeKeepsTierHistoryAndComesBack(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	ctx := context.Background()
	resetFrameworkCatalog(t, pool, provenanceSlug)
	t.Cleanup(func() { resetFrameworkCatalog(t, pool, provenanceSlug) })
	ensureSCFLoaded(t, pool)

	const src = "yaml:provenance.yaml"
	cw := func(anchors ...string) *soc2import.Crosswalk {
		c := provenanceCrosswalk("community_draft", "rationale")
		c.Source = src
		c.Mappings = nil
		for _, a := range anchors {
			c.Mappings = append(c.Mappings, soc2import.Mapping{
				RequirementCode: "P-1", SCFAnchor: a, RelationshipType: "equal", Strength: 0.9, Rationale: "rationale",
			})
		}
		return c
	}
	if _, err := soc2import.Import(ctx, pool, cw("GOV-01")); err != nil {
		t.Fatalf("first Import: %v", err)
	}
	var edgeID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT e.id FROM fw_to_scf_edges e
		JOIN framework_requirements fr ON fr.id = e.framework_requirement_id
		JOIN framework_versions fv ON fv.id = fr.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		JOIN scf_anchors a ON a.id = e.scf_anchor_id
		WHERE f.slug = $1 AND f.tenant_id IS NULL AND a.scf_id = 'GOV-01'`, provenanceSlug).Scan(&edgeID); err != nil {
		t.Fatalf("read edge id: %v", err)
	}
	// A reviewer verified the mapping.
	reviewer := uuid.New()
	if _, err := pool.Exec(ctx, `UPDATE fw_to_scf_edges SET mapping_tier = 'verified' WHERE id = $1`, edgeID); err != nil {
		t.Fatalf("verify edge: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO fw_to_scf_edge_tier_transitions (edge_id, reviewer_id, from_tier, to_tier, note)
		VALUES ($1, $2, 'draft', 'verified', 'reviewed')`, edgeID, reviewer); err != nil {
		t.Fatalf("record review: %v", err)
	}
	history := func() []string {
		t.Helper()
		return queryStrings(t, pool, `
			SELECT from_tier::text || '>' || to_tier::text || ' ' || note
			FROM fw_to_scf_edge_tier_transitions WHERE edge_id = $1 ORDER BY created_at, id`, edgeID)
	}
	live := func() int {
		t.Helper()
		return countRows(t, pool, `SELECT count(*) FROM effective_fw_to_scf_edges WHERE id = $1`, edgeID)
	}

	// The crosswalk drops GOV-01: retired, history intact, out of coverage.
	r, err := soc2import.Import(ctx, pool, cw("IAC-01"))
	if err != nil {
		t.Fatalf("dropping Import: %v", err)
	}
	if len(r.EdgesRetired) != 1 || r.EdgesRetired[0].SCFAnchor != "GOV-01" || r.EdgesRetired[0].Tier != "verified" {
		t.Fatalf("EdgesRetired = %+v; want the verified P-1->GOV-01 edge", r.EdgesRetired)
	}
	want := []string{"draft>verified reviewed", "verified>verified retired: dropped by crosswalk " + src}
	if got := history(); !slices.Equal(got, want) {
		t.Fatalf("history after retire = %v; want %v", got, want)
	}
	if live() != 0 {
		t.Fatal("a retired edge is still in effective_fw_to_scf_edges")
	}

	// The crosswalk lists GOV-01 again: restored, same row, tier kept.
	r, err = soc2import.Import(ctx, pool, cw("GOV-01", "IAC-01"))
	if err != nil {
		t.Fatalf("re-listing Import: %v", err)
	}
	if len(r.EdgesRestored) != 1 || r.EdgesRestored[0].SCFAnchor != "GOV-01" {
		t.Fatalf("EdgesRestored = %+v; want P-1->GOV-01", r.EdgesRestored)
	}
	if live() != 1 {
		t.Fatal("the re-listed edge is not back in effective_fw_to_scf_edges")
	}
	got := history()
	if len(got) != 3 || got[2] != "verified>verified restored: listed again by crosswalk "+src {
		t.Fatalf("history after restore = %v; want the review, the retirement and a restore row", got)
	}
	if attr, retired, _ := readProvenanceEdgeTo(t, pool, "GOV-01"); retired || attr != "community_draft" {
		t.Fatalf("restored edge attribution=%q retired=%v", attr, retired)
	}
	if tier := queryStrings(t, pool, `SELECT mapping_tier::text FROM fw_to_scf_edges WHERE id = $1`, edgeID); !slices.Equal(tier, []string{"verified"}) {
		t.Fatalf("restored edge tier = %v; want verified kept", tier)
	}
}

// A YAML crosswalk and a workbook import can write into the same framework
// version, both as community_draft (docs/operator/scf-crosswalk-import.md,
// "Combining columns"). Each re-import retires only the edges its own source
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

// coexistEdges returns "code->scf_id@source" for every live edge of the
// fixture.
func coexistEdges(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	return queryStrings(t, pool, `
		SELECT fr.code || '->' || a.scf_id || '@' || coalesce(e.crosswalk_source, '')
		FROM fw_to_scf_edges e
		JOIN framework_requirements fr ON fr.id = e.framework_requirement_id
		JOIN framework_versions fv ON fv.id = fr.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		JOIN scf_anchors a ON a.id = e.scf_anchor_id
		WHERE f.slug = $1 AND f.tenant_id IS NULL AND e.retired_at IS NULL
		ORDER BY 1`, coexistSlug)
}

func TestImport_YAMLAndWorkbookRetireOnlyTheirOwnEdges(t *testing.T) {
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

	// A YAML re-import that moves R-2 to IAC-14 retires its own old edge and
	// leaves the workbook's edges untouched.
	r := importCW(coexistCrosswalk(yamlSrc, [2]string{"R-1", "GOV-01"}, [2]string{"R-2", "IAC-14"}))
	if len(r.EdgesRetired) != 1 || r.EdgesRetired[0].SCFAnchor != "IAC-01" || r.EdgesRetired[0].Source != yamlSrc {
		t.Fatalf("YAML re-import retired %+v; want only its own R-2->IAC-01", r.EdgesRetired)
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

	// A workbook re-import that drops R-2 retires its own edge and leaves the
	// YAML's edges untouched.
	r = importCW(coexistCrosswalk(wbSrc, [2]string{"R-1", "IAC-37"}))
	if len(r.EdgesRetired) != 1 || r.EdgesRetired[0].SCFAnchor != "IAC-04" || r.EdgesRetired[0].Source != wbSrc {
		t.Fatalf("workbook re-import retired %+v; want only its own R-2->IAC-04", r.EdgesRetired)
	}
	want = []string{"R-1->GOV-01@" + yamlSrc, "R-1->IAC-37@" + wbSrc, "R-2->IAC-14@" + yamlSrc}
	if got := coexistEdges(t, pool); !slices.Equal(got, want) {
		t.Fatalf("after workbook re-import edges = %v; want %v", got, want)
	}

	// A workbook import never retires an edge with no recorded source: those
	// predate #1971 and belong to the YAML crosswalk.
	if _, err := pool.Exec(ctx, `
		UPDATE fw_to_scf_edges SET crosswalk_source = NULL
		WHERE crosswalk_source = $1`, yamlSrc); err != nil {
		t.Fatalf("clear YAML sources: %v", err)
	}
	r = importCW(coexistCrosswalk(wbSrc, [2]string{"R-1", "IAC-37"}))
	if len(r.EdgesRetired) != 0 {
		t.Fatalf("workbook import retired legacy edges %+v", r.EdgesRetired)
	}
	r = importCW(coexistCrosswalk(yamlSrc, [2]string{"R-1", "GOV-01"}))
	if len(r.EdgesRetired) != 1 || r.EdgesRetired[0].SCFAnchor != "IAC-14" || r.EdgesRetired[0].Source != soc2import.LegacySource {
		t.Fatalf("YAML import retired %+v; want the legacy R-2->IAC-14", r.EdgesRetired)
	}
	if !strings.HasPrefix(r.EdgesRetired[0].RequirementCode, "R-2") {
		t.Fatalf("retired edge requirement = %q; want R-2", r.EdgesRetired[0].RequirementCode)
	}
}
