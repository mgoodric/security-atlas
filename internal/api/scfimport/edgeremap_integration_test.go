//go:build integration

package scfimport_test

// #1777: importing a new SCF release queues one remap proposal per catalog
// crosswalk edge left on an older release's anchor, and a super-admin
// decides them. The SCF fixtures are the #1775 pair,
// testdata/remap-release-{a,b}.json, which give every class against
// release A: TRM-01 unchanged, TRM-02 renamed (-> TRM-20), TRM-03 split
// (-> TRM-30, TRM-31), TRM-04 reused_id (the code now names another
// lineage; its concept moved to TRM-41) and TRM-05 withdrawn. The crosswalk
// maps requirement R-0n to TRM-0n in release A.
//
// TRM-01 keeps its code and title, so the import carries R-01's edge
// forward on its own (#2315); the other four wait for a person.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/liveroletest"
	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/api/soc2import"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/crosswalkremap"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

const (
	edgeReleaseA = "test-remap-a"
	edgeReleaseB = "test-remap-b"
	edgeFwSlug   = "edge1777"
)

// edgeMappings is the release-A crosswalk: one requirement per fixture
// anchor, each with its own relationship, strength and rationale so a copy
// that dropped or mixed any of them shows.
var edgeMappings = []soc2import.Mapping{
	{RequirementCode: "R-01", SCFAnchor: "TRM-01", RelationshipType: "equal", Strength: 1.0, Rationale: "asset inventory is the requirement"},
	{RequirementCode: "R-02", SCFAnchor: "TRM-02", RelationshipType: "intersects_with", Strength: 0.7, Rationale: "change review covers part of it"},
	{RequirementCode: "R-03", SCFAnchor: "TRM-03", RelationshipType: "subset_of", Strength: 0.6, Rationale: "logging is one part"},
	{RequirementCode: "R-04", SCFAnchor: "TRM-04", RelationshipType: "equal", Strength: 0.9, Rationale: "backups"},
	{RequirementCode: "R-05", SCFAnchor: "TRM-05", RelationshipType: "superset_of", Strength: 0.4, Rationale: "fax"},
}

func edgeCrosswalk() *soc2import.Crosswalk {
	cw := &soc2import.Crosswalk{
		SchemaVersion:     soc2import.SchemaVersion,
		FrameworkSlug:     edgeFwSlug,
		FrameworkName:     "1777 edge remap fw",
		FrameworkIssuer:   "test",
		FrameworkVersion:  "2026",
		SourceAttribution: "community_draft",
		SCFRelease:        edgeReleaseA,
		Mappings:          edgeMappings,
	}
	for _, m := range edgeMappings {
		cw.Requirements = append(cw.Requirements, soc2import.Requirement{Code: m.RequirementCode, Title: "requirement " + m.RequirementCode})
	}
	return cw
}

// edgeRow is every content column of one fw_to_scf_edges row.
type edgeRow struct {
	ID                uuid.UUID
	RequirementID     uuid.UUID
	AnchorID          uuid.UUID
	Relationship      string
	Strength          float64
	SourceAttribution string
	Rationale         string
	MappingTier       string
	CrosswalkSource   *string
	RetiredAt         *time.Time
	UpdatedAt         time.Time
}

func readEdge(t *testing.T, pool *pgxpool.Pool, where string, args ...any) (edgeRow, bool) {
	t.Helper()
	var e edgeRow
	err := pool.QueryRow(context.Background(), `
		SELECT id, framework_requirement_id, scf_anchor_id, relationship_type::text,
		       strength, source_attribution::text, rationale, mapping_tier::text,
		       crosswalk_source, retired_at, updated_at
		FROM fw_to_scf_edges WHERE `+where, args...).Scan(
		&e.ID, &e.RequirementID, &e.AnchorID, &e.Relationship, &e.Strength,
		&e.SourceAttribution, &e.Rationale, &e.MappingTier, &e.CrosswalkSource, &e.RetiredAt, &e.UpdatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return edgeRow{}, false
		}
		t.Fatalf("read edge: %v", err)
	}
	return e, true
}

func requirementID(t *testing.T, pool *pgxpool.Pool, code string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		SELECT r.id FROM framework_requirements r
		JOIN framework_versions fv ON fv.id = r.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = $1 AND r.code = $2`, edgeFwSlug, code).Scan(&id); err != nil {
		t.Fatalf("requirement %s: %v", code, err)
	}
	return id
}

// legacyEdge is the release-A edge of requirement code.
func legacyEdge(t *testing.T, pool *pgxpool.Pool, code string) edgeRow {
	t.Helper()
	m := edgeMappings[slices.IndexFunc(edgeMappings, func(m soc2import.Mapping) bool { return m.RequirementCode == code })]
	e, ok := readEdge(t, pool, "framework_requirement_id = $1 AND scf_anchor_id = $2",
		requirementID(t, pool, code), anchorID(t, pool, edgeReleaseA, m.SCFAnchor))
	if !ok {
		t.Fatalf("no release-A edge for %s", code)
	}
	return e
}

type edgeProposal struct {
	id, class, status string
	successors        []string
}

// edgeProposals reads this fixture's queue, keyed by requirement code.
func edgeProposals(t *testing.T, pool *pgxpool.Pool) map[string]edgeProposal {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT r.code, p.id::text, p.class, p.status, p.successor_scf_ids
		FROM scf_edge_remap_proposals p
		JOIN fw_to_scf_edges e ON e.id = p.edge_id
		JOIN framework_requirements r ON r.id = e.framework_requirement_id
		JOIN framework_versions fv ON fv.id = r.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = $1`, edgeFwSlug)
	if err != nil {
		t.Fatalf("read edge queue: %v", err)
	}
	defer rows.Close()
	out := map[string]edgeProposal{}
	for rows.Next() {
		var code string
		var p edgeProposal
		if err := rows.Scan(&code, &p.id, &p.class, &p.status, &p.successors); err != nil {
			t.Fatalf("scan edge proposal: %v", err)
		}
		if _, dup := out[code]; dup {
			t.Fatalf("requirement %s has more than one edge proposal", code)
		}
		out[code] = p
	}
	return out
}

// setupEdgeRemap loads release A and the crosswalk, runs before (with A
// current), then imports B and queues the edge proposals.
func setupEdgeRemap(t *testing.T, before func(admin *pgxpool.Pool)) (admin, app *pgxpool.Pool) {
	t.Helper()
	admin = dbtest.NewMigratePool(t)
	app = dbtest.NewAppPool(t)
	truncateCatalog(t, admin)
	ctx := context.Background()
	importRelease(t, admin, loadRemapRelease(t, "a"))
	if _, err := soc2import.Import(ctx, admin, edgeCrosswalk()); err != nil {
		t.Fatalf("import crosswalk in A: %v", err)
	}
	if before != nil {
		before(admin)
	}
	importRelease(t, admin, loadRemapRelease(t, "b"))
	if _, err := scfimport.RebindControls(ctx, admin); err != nil {
		t.Fatalf("RebindControls: %v", err)
	}
	if _, err := scfimport.QueueEdgeRemapProposals(ctx, admin); err != nil {
		t.Fatalf("QueueEdgeRemapProposals: %v", err)
	}
	return admin, app
}

func TestEdgeRemapQueue_ImportingReleaseBQueuesEachEdgeWithItsClass(t *testing.T) {
	admin, _ := setupEdgeRemap(t, nil)
	before := map[string]edgeRow{}
	for _, m := range edgeMappings {
		before[m.RequirementCode] = legacyEdge(t, admin, m.RequirementCode)
	}

	got := edgeProposals(t, admin)
	want := map[string]edgeProposal{
		"R-01": {class: "unchanged", status: "approved", successors: []string{"TRM-01"}},
		"R-02": {class: "renamed", status: "open", successors: []string{"TRM-20"}},
		"R-03": {class: "split", status: "open", successors: []string{"TRM-30", "TRM-31"}},
		"R-04": {class: "reused_id", status: "open", successors: []string{"TRM-41"}},
		"R-05": {class: "no_successor", status: "open", successors: []string{}},
	}
	if len(got) != len(want) {
		t.Fatalf("queued %d edge proposals; want %d: %+v", len(got), len(want), got)
	}
	for code, w := range want {
		g := got[code]
		if g.class != w.class || g.status != w.status || !slices.Equal(g.successors, w.successors) {
			t.Errorf("%s: class=%s status=%s successors=%v; want %s %s %v", code, g.class, g.status, g.successors, w.class, w.status, w.successors)
		}
	}

	// Queueing moves nothing: every legacy edge is exactly as it was.
	for code, b := range before {
		if a := legacyEdge(t, admin, code); a != b {
			t.Errorf("%s: legacy edge changed by queueing: %+v -> %+v", code, b, a)
		}
	}

	// #2315: R-01's edge was carried to TRM-01 in B, a copy of the legacy
	// edge recorded as the import's decision.
	r01 := legacyEdge(t, admin, "R-01")
	carried, ok := readEdge(t, admin, "framework_requirement_id = $1 AND scf_anchor_id = $2",
		r01.RequirementID, anchorID(t, admin, edgeReleaseB, "TRM-01"))
	if !ok {
		t.Fatalf("R-01 was not carried to TRM-01 in %s", edgeReleaseB)
	}
	if carried.Relationship != r01.Relationship || carried.Strength != r01.Strength ||
		carried.Rationale != r01.Rationale || carried.SourceAttribution != r01.SourceAttribution ||
		carried.MappingTier != r01.MappingTier || carried.RetiredAt != nil {
		t.Fatalf("carried edge %+v differs from legacy %+v", carried, r01)
	}
	var decidedBy string
	var bulk bool
	if err := admin.QueryRow(context.Background(), `
		SELECT decided_by, bulk FROM scf_edge_remap_decisions
		WHERE proposal_id = $1 AND decision = 'approved' AND new_edge_id = $2`,
		got["R-01"].id, carried.ID).Scan(&decidedBy, &bulk); err != nil {
		t.Fatalf("carry decision: %v", err)
	}
	if decidedBy != scfimport.CarryForwardDecider || !bulk {
		t.Fatalf("carry decision decided_by=%q bulk=%v; want %q true", decidedBy, bulk, scfimport.CarryForwardDecider)
	}

	// A re-run queues and carries nothing new.
	report, err := scfimport.QueueEdgeRemapProposals(context.Background(), admin)
	if err != nil {
		t.Fatalf("re-run: %v", err)
	}
	for class, n := range report.Queued {
		if n != 0 {
			t.Errorf("re-run queued %d %s proposals; want 0", n, class)
		}
	}
	if report.Carried != 0 {
		t.Errorf("re-run carried %d edges; want 0", report.Carried)
	}
	var n int
	if err := admin.QueryRow(context.Background(), `SELECT count(*) FROM fw_to_scf_edges WHERE framework_requirement_id = $1`,
		r01.RequirementID).Scan(&n); err != nil || n != 2 {
		t.Fatalf("R-01 has %d edges after the re-run (err %v); want the legacy one and one carried", n, err)
	}
}

func TestEdgeRemapDecide_ApproveCopiesTheEdgeAndLeavesTheLegacyOne(t *testing.T) {
	admin, app := setupEdgeRemap(t, func(admin *pgxpool.Pool) {
		// A non-default tier, so the copy has to carry it.
		if _, err := admin.Exec(context.Background(), `
			UPDATE fw_to_scf_edges SET mapping_tier = 'under_review'
			WHERE framework_requirement_id = $1`, requirementID(t, admin, "R-02")); err != nil {
			t.Fatalf("set tier: %v", err)
		}
	})
	ctx := context.Background()
	store := crosswalkremap.NewStore(app)
	props := edgeProposals(t, admin)
	legacy := legacyEdge(t, admin, "R-02")
	approver := uuid.NewString()

	d, err := store.Decide(ctx, crosswalkremap.DecideInput{
		ProposalID: props["R-02"].id, Decision: crosswalkremap.StatusApproved, DecidedBy: approver, Reason: "cosmetic rename",
	})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	newAnchor := anchorID(t, admin, edgeReleaseB, "TRM-20")
	copied, ok := readEdge(t, admin, "id = $1", uuid.MustParse(d.NewEdgeID))
	if !ok {
		t.Fatalf("approval named edge %s, which does not exist", d.NewEdgeID)
	}
	if copied.AnchorID != newAnchor || copied.RequirementID != legacy.RequirementID {
		t.Fatalf("new edge is %s -> %s; want %s -> %s", copied.RequirementID, copied.AnchorID, legacy.RequirementID, newAnchor)
	}
	if copied.Relationship != legacy.Relationship || copied.Strength != legacy.Strength ||
		copied.Rationale != legacy.Rationale || copied.SourceAttribution != legacy.SourceAttribution ||
		copied.MappingTier != legacy.MappingTier || copied.MappingTier != "under_review" {
		t.Fatalf("new edge content %+v differs from legacy %+v", copied, legacy)
	}
	if copied.CrosswalkSource == nil || *copied.CrosswalkSource != "scf-remap:"+props["R-02"].id || copied.RetiredAt != nil {
		t.Fatalf("new edge source/retired = %v/%v", copied.CrosswalkSource, copied.RetiredAt)
	}
	if after := legacyEdge(t, admin, "R-02"); after != legacy {
		t.Fatalf("legacy edge changed: %+v -> %+v", legacy, after)
	}

	// The proposal and the history record the outcome and who decided.
	var status string
	var target, newEdge uuid.UUID
	if err := admin.QueryRow(ctx, `SELECT status, target_anchor_id, new_edge_id FROM scf_edge_remap_proposals WHERE id = $1`,
		props["R-02"].id).Scan(&status, &target, &newEdge); err != nil {
		t.Fatalf("read proposal: %v", err)
	}
	if status != "approved" || target != newAnchor || newEdge != copied.ID {
		t.Fatalf("proposal = %s %s %s; want approved %s %s", status, target, newEdge, newAnchor, copied.ID)
	}
	if d.DecidedBy != approver || d.NewSCFID != "TRM-20" || d.NewRelease != edgeReleaseB ||
		d.LegacySCFID != "TRM-02" || d.LegacyRelease != edgeReleaseA || d.LegacyEdgeID != legacy.ID.String() ||
		d.Reason != "cosmetic rename" || d.DecidedAt == "" || d.Bulk {
		t.Fatalf("decision = %+v", d)
	}

	// Approved is final.
	if _, err := store.Decide(ctx, crosswalkremap.DecideInput{ProposalID: props["R-02"].id, Decision: crosswalkremap.StatusKeptLegacy, DecidedBy: approver}); !errors.Is(err, crosswalkremap.ErrDecided) {
		t.Fatalf("keep-legacy after approve: err=%v; want ErrDecided", err)
	}
}

func TestEdgeRemapDecide_SplitNeedsAnAnchorInTheTargetRelease(t *testing.T) {
	admin, app := setupEdgeRemap(t, nil)
	ctx := context.Background()
	store := crosswalkremap.NewStore(app)
	props := edgeProposals(t, admin)
	who := uuid.NewString()
	approve := func(code, scf string) (crosswalkremap.Decision, error) {
		return store.Decide(ctx, crosswalkremap.DecideInput{ProposalID: props[code].id, Decision: crosswalkremap.StatusApproved, SCFID: scf, DecidedBy: who})
	}

	if _, err := approve("R-03", ""); !errors.Is(err, crosswalkremap.ErrAnchorRequired) {
		t.Fatalf("split with no scf_id: err=%v; want ErrAnchorRequired", err)
	}
	if _, err := approve("R-05", ""); !errors.Is(err, crosswalkremap.ErrAnchorRequired) {
		t.Fatalf("no_successor with no scf_id: err=%v; want ErrAnchorRequired", err)
	}
	// TRM-02 exists only in A.
	if _, err := approve("R-03", "TRM-02"); !errors.Is(err, crosswalkremap.ErrAnchorNotInRelease) {
		t.Fatalf("code outside the target release: err=%v; want ErrAnchorNotInRelease", err)
	}
	d, err := approve("R-03", "TRM-31")
	if err != nil {
		t.Fatalf("split with TRM-31: %v", err)
	}
	if d.NewAnchorID != anchorID(t, admin, edgeReleaseB, "TRM-31").String() {
		t.Fatalf("split approved onto %s; want TRM-31", d.NewAnchorID)
	}
	// A withdrawn anchor can still be mapped to any anchor a person picks.
	if _, err := approve("R-05", "TRM-41"); err != nil {
		t.Fatalf("no_successor with TRM-41: %v", err)
	}

	// The requirement already maps to the chosen anchor: refused, not doubled.
	if _, err := admin.Exec(ctx, `
		INSERT INTO fw_to_scf_edges (id, framework_requirement_id, scf_anchor_id, relationship_type, strength, source_attribution, rationale)
		VALUES (gen_random_uuid(), $1, $2, 'equal', 1, 'community_draft', 'already carried')`,
		requirementID(t, admin, "R-04"), anchorID(t, admin, edgeReleaseB, "TRM-41")); err != nil {
		t.Fatalf("seed existing edge: %v", err)
	}
	if _, err := approve("R-04", ""); !errors.Is(err, crosswalkremap.ErrEdgeExists) {
		t.Fatalf("approve onto an existing edge: err=%v; want ErrEdgeExists", err)
	}
	if _, err := approve("R-04", "TRM-04"); err != nil {
		t.Fatalf("reused_id onto the reused code, chosen explicitly: %v", err)
	}
}

func TestEdgeRemapDecide_RejectIsRevisitableKeepLegacyIsFinal(t *testing.T) {
	admin, app := setupEdgeRemap(t, nil)
	ctx := context.Background()
	store := crosswalkremap.NewStore(app)
	id := edgeProposals(t, admin)["R-02"].id
	legacy := legacyEdge(t, admin, "R-02")
	who := uuid.NewString()
	decide := func(decision string) error {
		_, err := store.Decide(ctx, crosswalkremap.DecideInput{ProposalID: id, Decision: decision, DecidedBy: who, Reason: "not yet"})
		return err
	}
	if err := decide(crosswalkremap.StatusRejected); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if err := decide(crosswalkremap.StatusRejected); !errors.Is(err, crosswalkremap.ErrDecided) {
		t.Fatalf("second reject: err=%v; want ErrDecided", err)
	}
	if err := decide(crosswalkremap.StatusKeptLegacy); err != nil {
		t.Fatalf("keep legacy after reject: %v", err)
	}
	if err := decide(crosswalkremap.StatusApproved); !errors.Is(err, crosswalkremap.ErrDecided) {
		t.Fatalf("approve after keep legacy: err=%v; want ErrDecided", err)
	}
	if after := legacyEdge(t, admin, "R-02"); after != legacy {
		t.Fatalf("legacy edge changed: %+v -> %+v", legacy, after)
	}
	var n int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM fw_to_scf_edges WHERE framework_requirement_id = $1`, legacy.RequirementID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("requirement has %d edges (err %v); want only the legacy one", n, err)
	}
	if _, err := store.Decide(ctx, crosswalkremap.DecideInput{ProposalID: uuid.NewString(), Decision: crosswalkremap.StatusRejected, DecidedBy: who}); !errors.Is(err, crosswalkremap.ErrNotFound) {
		t.Fatalf("unknown proposal: err=%v; want ErrNotFound", err)
	}
	var decisions []string
	rows, err := admin.Query(ctx, `SELECT decision FROM scf_edge_remap_decisions WHERE proposal_id = $1 ORDER BY decided_at, id`, id)
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		decisions = append(decisions, s)
	}
	rows.Close()
	if !slices.Equal(decisions, []string{"rejected", "kept_legacy"}) {
		t.Fatalf("history = %v; want [rejected kept_legacy]", decisions)
	}
}

func TestEdgeRemapDecide_BulkApprovesUnchangedOnly(t *testing.T) {
	admin, app := setupEdgeRemap(t, nil)
	ctx := context.Background()
	store := crosswalkremap.NewStore(app)
	// The import already carried R-01 (#2315). An unchanged proposal it
	// leaves open is one whose successor has the same title under another
	// code; the fixture has none, so R-02's single-successor proposal
	// stands in for one.
	if _, err := admin.Exec(ctx, `UPDATE scf_edge_remap_proposals SET class = 'unchanged' WHERE id = $1`,
		edgeProposals(t, admin)["R-02"].id); err != nil {
		t.Fatalf("reclass R-02: %v", err)
	}
	res, err := store.BulkApproveUnchanged(ctx, uuid.NewString())
	if err != nil {
		t.Fatalf("bulk: %v", err)
	}
	props := edgeProposals(t, admin)
	if res.ApprovedCount != 1 || res.Approved[0].ProposalID != props["R-02"].id || !res.Approved[0].Bulk {
		t.Fatalf("bulk approved %+v; want only R-02's unchanged proposal", res)
	}
	for code, p := range props {
		want := "open"
		if code == "R-01" || code == "R-02" {
			want = "approved"
		}
		if p.status != want {
			t.Errorf("%s: status %s; want %s", code, p.status, want)
		}
	}
	if _, ok := readEdge(t, admin, "framework_requirement_id = $1 AND scf_anchor_id = $2",
		requirementID(t, admin, "R-02"), anchorID(t, admin, edgeReleaseB, "TRM-20")); !ok {
		t.Fatalf("bulk approval created no edge on TRM-20 in B")
	}
	again, err := store.BulkApproveUnchanged(ctx, uuid.NewString())
	if err != nil || again.ApprovedCount != 0 {
		t.Fatalf("re-run: %+v, %v; want nothing to do", again, err)
	}
}

// The app role reaches the queue and the catalog only through the decide
// function: it cannot write either table or fw_to_scf_edges directly.
func TestEdgeRemap_AppRoleCannotWriteAroundTheDecideFunction(t *testing.T) {
	admin, app := setupEdgeRemap(t, nil)
	ctx := context.Background()
	p := edgeProposals(t, admin)["R-02"]
	legacy := legacyEdge(t, admin, "R-02")
	for name, stmt := range map[string]string{
		"approve in place": `UPDATE scf_edge_remap_proposals SET status = 'kept_legacy' WHERE id = '` + p.id + `'`,
		"forge history": `INSERT INTO scf_edge_remap_decisions (proposal_id, decision, legacy_edge_id, legacy_anchor_id, legacy_scf_id, legacy_release, decided_by)
			VALUES ('` + p.id + `', 'rejected', '` + legacy.ID.String() + `', '` + legacy.AnchorID.String() + `', 'TRM-02', 'x', 'me')`,
		"delete a proposal": `DELETE FROM scf_edge_remap_proposals WHERE id = '` + p.id + `'`,
		"insert an edge": `INSERT INTO fw_to_scf_edges (id, framework_requirement_id, scf_anchor_id, relationship_type, strength, source_attribution)
			VALUES (gen_random_uuid(), '` + legacy.RequirementID.String() + `', '` + anchorID(t, admin, edgeReleaseB, "TRM-20").String() + `', 'equal', 1, 'community_draft')`,
	} {
		if _, err := app.Exec(ctx, stmt); err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("%s as atlas_app: err=%v; want permission denied", name, err)
		}
	}
	if got := edgeProposals(t, admin)["R-02"]; got.status != "open" {
		t.Fatalf("proposal status = %s; want open", got.status)
	}
}

// Both catalog functions refuse a caller in a tenant context (#2362's
// guard): a tenant-scoped transaction has no business deciding global
// edges, whatever role it runs as.
func TestEdgeRemap_FunctionsRefuseATenantContext(t *testing.T) {
	admin, app := setupEdgeRemap(t, nil)
	ctx := context.Background()
	id := edgeProposals(t, admin)["R-02"].id
	for name, call := range map[string]string{
		"scf_edge_remap_decide":              `SELECT 1 FROM scf_edge_remap_decide($1::uuid, 'approved', '', '', 'someone', false)`,
		"scf_edge_remap_lock_open_unchanged": `SELECT 1 FROM scf_edge_remap_lock_open_unchanged() WHERE $1::uuid IS NOT NULL`,
	} {
		tx, err := app.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.current_tenant', $1, true)`, uuid.NewString()); err != nil {
			t.Fatalf("set tenant: %v", err)
		}
		_, err = tx.Exec(ctx, call, id)
		_ = tx.Rollback(ctx)
		if err == nil || !strings.Contains(err.Error(), "refused under a tenant context") {
			t.Errorf("%s under a tenant context: err=%v; want a refusal", name, err)
		}
	}
	if p := edgeProposals(t, admin)["R-02"]; p.status != "open" {
		t.Fatalf("proposal status = %s after the refused calls; want open", p.status)
	}
	// Without a tenant context the same call goes through.
	if _, err := crosswalkremap.NewStore(app).Decide(ctx, crosswalkremap.DecideInput{
		ProposalID: id, Decision: crosswalkremap.StatusApproved, DecidedBy: uuid.NewString(),
	}); err != nil {
		t.Fatalf("approve without a tenant context: %v", err)
	}
}

// --- through the API server: authz and coverage ---

func edgeServer(t *testing.T, app *pgxpool.Pool) *api.Server {
	t.Helper()
	srv := api.New(api.Config{RotationGrace: time.Hour})
	srv.AttachDB(app)
	return srv
}

func do(t *testing.T, ts *httptest.Server, method, path, bearer, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, ts.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// superAdminBearer seeds a person holding a live admin grant in tenant and
// returns a session for them carrying the super-admin claim: the platform
// operator's shape. admin.rego admits the /v1/admin/* platform segments to
// an admin role on an admin credential (#2503).
func superAdminBearer(t *testing.T, srv *api.Server, tenant uuid.UUID) string {
	t.Helper()
	user, _ := liveroletest.Person(t, srv, tenant.String(), "admin")
	c := testjwt.AdminFor(tenant)
	c.Subject = "user:" + user.String()
	return srv.IssueTestJWT(t, c)
}

// A tenant admin who is not super-admin is refused every edge-proposal
// route, and nothing changes; a super-admin lists and decides them. Both
// are seeded people on the real engine (#3016), and the tenant admin's
// refusal is the engine's own "forbidden": admin.rego keeps these platform
// segments off a tenant admin by role.
func TestEdgeRemapAPI_TenantAdminForbiddenSuperAdminSucceeds(t *testing.T) {
	admin, app := setupEdgeRemap(t, nil)
	tenant := uuid.MustParse(dbtest.SeedTenant(t, admin, "scf_remap_decisions", "controls"))
	srv := edgeServer(t, app)
	liveroletest.Attach(t, srv, app)
	tenantBearer := liveroletest.Bearer(t, srv, tenant.String(), "admin")
	superBearer := superAdminBearer(t, srv, tenant)
	// The handler chain is built after the test signer exists.
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)

	props := edgeProposals(t, admin)
	base := "/v1/admin/crosswalk-edge-remap-proposals"
	writes := []struct{ path, body string }{
		{base + "/approve-unchanged", ""},
		{base + "/" + props["R-02"].id + "/approve", `{}`},
		{base + "/" + props["R-03"].id + "/approve", `{"scf_id":"TRM-30"}`},
		{base + "/" + props["R-04"].id + "/reject", `{"reason":"no"}`},
		{base + "/" + props["R-05"].id + "/keep-legacy", `{}`},
	}
	if code, body := do(t, ts, http.MethodGet, base, tenantBearer, ""); code != http.StatusForbidden || !strings.Contains(string(body), `"error":"forbidden"`) {
		t.Errorf("tenant admin GET: %d %s; want 403 forbidden", code, body)
	}
	for _, w := range writes {
		if code, body := do(t, ts, http.MethodPost, w.path, tenantBearer, w.body); code != http.StatusForbidden || !strings.Contains(string(body), `"error":"forbidden"`) {
			t.Errorf("tenant admin POST %s: %d %s; want 403 forbidden", w.path, code, body)
		}
	}
	for code, p := range edgeProposals(t, admin) {
		// R-01 was carried at import (#2315).
		if want := map[bool]string{true: "approved", false: "open"}[code == "R-01"]; p.status != want {
			t.Fatalf("after tenant-admin calls %s is %s; want %s", code, p.status, want)
		}
	}

	code, body := do(t, ts, http.MethodGet, base+"?status=open", superBearer, "")
	if code != http.StatusOK {
		t.Fatalf("super-admin GET: %d %s", code, body)
	}
	var list struct {
		Proposals []crosswalkremap.Proposal `json:"proposals"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if n := len(slices.DeleteFunc(list.Proposals, func(p crosswalkremap.Proposal) bool { return p.FrameworkSlug != edgeFwSlug })); n != 4 {
		t.Fatalf("super-admin lists %d open proposals of the fixture; want 4", n)
	}
	for _, w := range writes {
		if code, body := do(t, ts, http.MethodPost, w.path, superBearer, w.body); code != http.StatusOK {
			t.Errorf("super-admin POST %s: %d %s; want 200", w.path, code, body)
		}
	}
	want := map[string]string{"R-01": "approved", "R-02": "approved", "R-03": "approved", "R-04": "rejected", "R-05": "kept_legacy"}
	for code, p := range edgeProposals(t, admin) {
		if p.status != want[code] {
			t.Errorf("%s: status %s; want %s", code, p.status, want[code])
		}
	}
}

type coverage struct {
	Anchors []struct {
		SCFID  string `json:"scf_id"`
		EdgeID string `json:"edge_id"`
	} `json:"anchors"`
	Controls []struct {
		BundleID string `json:"bundle_id"`
	} `json:"controls"`
	SCFRelease *string `json:"scf_release"`
}

// Coverage for a requirement whose edge was remapped is the same before and
// after, for a control remapped to the same successor. After the upgrade
// the default read (pinned to the current release) counts the new edge and
// not the legacy one; ?scf_release=<old> still reads the legacy edge.
func TestEdgeRemapCoverage_SameBeforeAndAfterForAControlOnTheSameSuccessor(t *testing.T) {
	var tenant uuid.UUID
	var srv *api.Server
	var ts *httptest.Server
	var bearer string
	read := func(query string) coverage {
		t.Helper()
		code, body := do(t, ts, http.MethodGet, "/v1/requirements/"+edgeFwSlug+":2026:R-02/coverage"+query, bearer, "")
		if code != http.StatusOK {
			t.Fatalf("coverage%s: %d %s", query, code, body)
		}
		var c coverage
		if err := json.Unmarshal(body, &c); err != nil {
			t.Fatalf("decode coverage: %v", err)
		}
		return c
	}
	bundles := func(c coverage) []string {
		var out []string
		for _, x := range c.Controls {
			out = append(out, x.BundleID)
		}
		slices.Sort(out)
		return out
	}
	anchors := func(c coverage) []string {
		var out []string
		for _, a := range c.Anchors {
			out = append(out, a.SCFID)
		}
		slices.Sort(out)
		return out
	}

	var before coverage
	admin, app := setupEdgeRemap(t, func(admin *pgxpool.Pool) {
		// With release A current: one control on TRM-02, uploaded through
		// the control store so its manifest re-anchors on approval.
		tenant = uuid.MustParse(dbtest.SeedTenant(t, admin, "scf_remap_decisions", "controls"))
		app := dbtest.NewAppPool(t)
		b, err := control.FinalizeBundleForHTTP([]byte(`bundle_schema_version: "1"
bundle_id: edge_1777_change
title: "Change review"
scf_anchor_id: TRM-02
implementation_type: manual_attested
`))
		if err != nil {
			t.Fatalf("bundle: %v", err)
		}
		if _, err := control.NewStore(app).Upload(withTenant(t, tenant.String()), b, "seed-1777"); err != nil {
			t.Fatalf("upload: %v", err)
		}
		srv = edgeServer(t, app)
		liveroletest.Attach(t, srv, app)
		// It reads coverage and approves the edge remap, a platform route.
		bearer = superAdminBearer(t, srv, tenant)
		ts = httptest.NewServer(srv.HTTPHandlerForTests())
		t.Cleanup(ts.Close)
		before = read("")
	})
	if before.SCFRelease == nil || *before.SCFRelease != edgeReleaseA ||
		!slices.Equal(bundles(before), []string{"edge_1777_change"}) || !slices.Equal(anchors(before), []string{"TRM-02"}) {
		t.Fatalf("before the upgrade: %+v; want edge_1777_change via TRM-02 in %s", before, edgeReleaseA)
	}
	ctx := context.Background()

	// The control moves to the same successor its edge does.
	var controlProposal string
	if err := admin.QueryRow(ctx, `SELECT id::text FROM scf_remap_proposals WHERE tenant_id = $1 AND legacy_scf_id = 'TRM-02'`, tenant).Scan(&controlProposal); err != nil {
		t.Fatalf("control proposal: %v", err)
	}
	if _, err := control.NewStore(app).ApproveRemap(withTenant(t, tenant.String()), control.RemapApproveInput{
		ProposalID: controlProposal, DecidedBy: uuid.NewString(),
	}); err != nil {
		t.Fatalf("approve control remap: %v", err)
	}
	// Mid-upgrade: the control is on TRM-20 but no edge is, so the default
	// read has lost the coverage. The edge remap is what restores it.
	if mid := read(""); len(mid.Controls) != 0 {
		t.Fatalf("with only the control remapped, coverage = %v; want none", bundles(mid))
	}
	if code, body := do(t, ts, http.MethodPost, "/v1/admin/crosswalk-edge-remap-proposals/"+edgeProposals(t, admin)["R-02"].id+"/approve", bearer, `{}`); code != http.StatusOK {
		t.Fatalf("approve edge remap: %d %s", code, body)
	}

	after := read("")
	if after.SCFRelease == nil || *after.SCFRelease != edgeReleaseB {
		t.Fatalf("default read after the upgrade echoed %v; want %s", after.SCFRelease, edgeReleaseB)
	}
	if !slices.Equal(bundles(after), bundles(before)) {
		t.Fatalf("coverage changed across the remap: before %v, after %v", bundles(before), bundles(after))
	}
	if !slices.Equal(anchors(after), []string{"TRM-20"}) {
		t.Fatalf("default read anchors = %v; want only the new TRM-20 edge", anchors(after))
	}
	legacy := read("?scf_release=" + edgeReleaseA)
	if !slices.Equal(anchors(legacy), []string{"TRM-02"}) || legacy.Anchors[0].EdgeID != legacyEdge(t, admin, "R-02").ID.String() {
		t.Fatalf("?scf_release=%s anchors = %+v; want the untouched legacy TRM-02 edge", edgeReleaseA, legacy.Anchors)
	}
}
