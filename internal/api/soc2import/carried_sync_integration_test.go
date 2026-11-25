//go:build integration

package soc2import_test

// #3073: a crosswalk that lists the code a carried edge sits on makes its
// own STRM content win on that copy. The copy is updated in place, so its
// row and tier history are kept, and a system transition records the sync.
// A copy a person has moved through the tier workflow is left alone.

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/api/soc2import"
)

// remapCarried approves a proposal for the fixture's GOV-02 edge in the
// sample onto GOV-04 in the operator's release, as a super-admin renaming X
// to Y does, and returns the new edge's id.
func remapCarried(t *testing.T, pool *pgxpool.Pool, sample, byo string) string {
	t.Helper()
	var newEdge string
	err := pool.QueryRow(context.Background(), `
		WITH src AS (
			SELECT e.id AS edge_id, a.id AS anchor_id
			FROM fw_to_scf_edges e
			JOIN framework_requirements fr ON fr.id = e.framework_requirement_id
			JOIN framework_versions rv ON rv.id = fr.framework_version_id
			JOIN frameworks f ON f.id = rv.framework_id AND f.slug = $1
			JOIN scf_anchors a ON a.id = e.scf_anchor_id
			JOIN framework_versions av ON av.id = a.framework_version_id AND av.version = $2
			WHERE a.scf_id = 'GOV-02'
		), tgt AS (
			SELECT v.id FROM framework_versions v
			JOIN frameworks f ON f.id = v.framework_id AND f.slug = 'scf' AND f.tenant_id IS NULL
			WHERE v.version = $3
		)
		INSERT INTO scf_edge_remap_proposals (
			edge_id, legacy_anchor_id, legacy_scf_id, legacy_release,
			target_framework_version_id, target_release, class, successor_scf_ids)
		SELECT src.edge_id, src.anchor_id, 'GOV-02', $2, tgt.id, $3, 'renamed', ARRAY['GOV-04']
		FROM src, tgt
		RETURNING id::text`, carriedSlug, sample, byo).Scan(&newEdge)
	if err != nil {
		t.Fatalf("queue remap GOV-02 -> GOV-04: %v", err)
	}
	if err := pool.QueryRow(context.Background(),
		`SELECT new_edge_id::text FROM scf_edge_remap_decide($1, 'approved', 'GOV-04', 'renamed in test', 'test:3073', false)`,
		newEdge).Scan(&newEdge); err != nil {
		t.Fatalf("approve remap GOV-02 -> GOV-04: %v", err)
	}
	return newEdge
}

// edgeContent reads one edge's STRM content, crosswalk_source and liveness.
func edgeContent(t *testing.T, pool *pgxpool.Pool, id string) (rel string, strength float64, rationale, source string, live bool) {
	t.Helper()
	if err := pool.QueryRow(context.Background(), `
		SELECT relationship_type::text, strength, rationale, crosswalk_source, retired_at IS NULL
		FROM fw_to_scf_edges WHERE id = $1`, id).Scan(&rel, &strength, &rationale, &source, &live); err != nil {
		t.Fatalf("read edge %s: %v", id, err)
	}
	return rel, strength, rationale, source, live
}

// syncFixture sets up a BYO install (operator release current, sample
// legacy beside it) whose crosswalk maps R-1 to GOV-02 in the sample, and
// remaps that edge onto GOV-04 in the operator's release. It returns the
// carried copy's id.
func syncFixture(t *testing.T) (pool *pgxpool.Pool, byo, sample, copyID string) {
	t.Helper()
	pool = carriedFixture(t)
	byo = "test-byo-3073"
	importSCF(t, pool, relabeledSample(t, byo))
	cat := loadSCFSample(t)
	if _, err := scfimport.ImportWithOptions(context.Background(), pool, cat, scfimport.Options{KeepCurrent: true}); err != nil {
		t.Fatalf("load sample with KeepCurrent: %v", err)
	}
	sample = cat.ReleaseVersion
	importCarried(t, pool, carriedCrosswalk(sample, "GOV-02"))
	copyID = remapCarried(t, pool, sample, byo)
	return pool, byo, sample, copyID
}

// movedCrosswalk moves R-1 from GOV-02 to GOV-04, with STRM content unlike
// the GOV-02 mapping's.
func movedCrosswalk(sample string) *soc2import.Crosswalk {
	cw := carriedCrosswalk(sample, "GOV-04")
	cw.Mappings[0].RelationshipType = "subset_of"
	cw.Mappings[0].Strength = 0.9
	cw.Mappings[0].Rationale = "R-1 is a subset of GOV-04"
	return cw
}

// Remap X->Y, then a crosswalk lists Y with different content: the live edge
// on Y's anchor carries the crosswalk's content, on the same row, with a
// transition recording the change.
func TestCarriedEdges_CrosswalkContentWinsOnTheCarriedCopy(t *testing.T) {
	pool, byo, sample, copyID := syncFixture(t)
	if rel, _, rationale, _, _ := edgeContent(t, pool, copyID); rel != "intersects_with" || rationale != "R-1->GOV-02" {
		t.Fatalf("carried copy starts as %s %q; want GOV-02's content", rel, rationale)
	}

	rep := importCarried(t, pool, movedCrosswalk(sample))
	rel, strength, rationale, source, live := edgeContent(t, pool, copyID)
	if !live || rel != "subset_of" || strength != 0.9 || rationale != "R-1 is a subset of GOV-04" {
		t.Errorf("carried copy on GOV-04@%s = live %v, %s %.2f %q; want live subset_of 0.90 with the crosswalk's rationale", byo, live, rel, strength, rationale)
	}
	if !strings.HasPrefix(source, "scf-remap:") {
		t.Errorf("crosswalk_source = %q; the copy keeps its remap link", source)
	}
	if got := carriedCodes(rep.EdgesCarriedSynced); !slices.Equal(got, []string{"GOV-04"}) {
		t.Errorf("EdgesCarriedSynced = %v; want [GOV-04]", got)
	}
	if n := countRows(t, pool, `
		SELECT count(*) FROM fw_to_scf_edge_tier_transitions
		WHERE edge_id = $1 AND reviewer_id = '00000000-0000-0000-0000-000000000000'
		  AND starts_with(note, 'carried-sync:"`+carriedSource+`" ')`, copyID); n != 1 {
		t.Errorf("%d carried-sync audit rows on the copy; want 1", n)
	}
	assertEdges(t, pool,
		"GOV-02@"+sample+":retired", "GOV-04@"+byo+":live", "GOV-04@"+sample+":live")

	// The carry-forward then finds the pair mapped, closes the GOV-04
	// proposal and leaves the content.
	carry(t, pool, 0)
	if rel, _, rationale, _, _ := edgeContent(t, pool, copyID); rel != "subset_of" || rationale != "R-1 is a subset of GOV-04" {
		t.Errorf("after the carry-forward the copy is %s %q; want the crosswalk's content", rel, rationale)
	}

	rep = importCarried(t, pool, movedCrosswalk(sample))
	if len(rep.EdgesCarriedSynced) != 0 {
		t.Errorf("re-import synced %v again", rep.EdgesCarriedSynced)
	}
}

// A copy a person has moved through the tier workflow keeps its content and
// is reported.
func TestCarriedEdges_HumanReviewedCopyKeepsItsContent(t *testing.T) {
	pool, _, sample, copyID := syncFixture(t)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO fw_to_scf_edge_tier_transitions (edge_id, reviewer_id, from_tier, to_tier, note)
		VALUES ($1, '11111111-1111-1111-1111-111111111111', 'draft', 'draft', 'reviewed by a person')`, copyID); err != nil {
		t.Fatalf("record a human transition: %v", err)
	}

	rep := importCarried(t, pool, movedCrosswalk(sample))
	if rel, _, rationale, _, live := edgeContent(t, pool, copyID); !live || rel != "intersects_with" || rationale != "R-1->GOV-02" {
		t.Errorf("human-reviewed copy = live %v, %s %q; want it untouched", live, rel, rationale)
	}
	if len(rep.EdgesCarriedSynced) != 0 {
		t.Errorf("EdgesCarriedSynced = %v; want none", rep.EdgesCarriedSynced)
	}
	if got := heldBack(rep); !slices.Equal(got, []string{"GOV-04:human_reviewed"}) {
		t.Errorf("EdgesCarriedHeldBack = %v; want [GOV-04:human_reviewed]", got)
	}
}

// heldBack returns "scf_id:reason" for each carried edge the sync held back.
func heldBack(rep soc2import.Report) []string {
	var out []string
	for _, e := range rep.EdgesCarriedHeldBack {
		out = append(out, e.SCFAnchor+":"+string(e.Reason))
	}
	return out
}

// setCopy sets columns on the carried copy directly, with no tier
// transition, as a copy inherits them from its source when the remap is
// approved (scf_edge_remap_decide copies source_attribution and
// mapping_tier).
func setCopy(t *testing.T, pool *pgxpool.Pool, id, set string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `UPDATE fw_to_scf_edges SET `+set+` WHERE id = $1`, id); err != nil {
		t.Fatalf("set %s on the carried copy: %v", set, err)
	}
}

// copyTierAndAttribution reads the carried copy's mapping_tier and
// source_attribution.
func copyTierAndAttribution(t *testing.T, pool *pgxpool.Pool, id string) (tier, attribution string) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT mapping_tier::text, source_attribution::text FROM fw_to_scf_edges WHERE id = $1`, id).Scan(&tier, &attribution); err != nil {
		t.Fatalf("read tier of %s: %v", id, err)
	}
	return tier, attribution
}

// officialMovedCrosswalk is movedCrosswalk with the row's provenance raised
// to scf_official; the crosswalk default stays community_draft, so the
// import still owns the GOV-02 root.
func officialMovedCrosswalk(sample string) *soc2import.Crosswalk {
	cw := movedCrosswalk(sample)
	cw.Mappings[0].SourceAttribution = "scf_official"
	return cw
}

// A copy whose stored attribution outranks the crosswalk row's keeps its
// content, and is reported held back as outranked (#3079).
func TestCarriedEdges_OutrankedCopyIsHeldBack(t *testing.T) {
	pool, _, sample, copyID := syncFixture(t)
	setCopy(t, pool, copyID, "source_attribution = 'scf_official'")

	rep := importCarried(t, pool, movedCrosswalk(sample))
	if rel, _, rationale, _, live := edgeContent(t, pool, copyID); !live || rel != "intersects_with" || rationale != "R-1->GOV-02" {
		t.Errorf("outranked copy = live %v, %s %q; want it untouched", live, rel, rationale)
	}
	if _, attribution := copyTierAndAttribution(t, pool, copyID); attribution != "scf_official" {
		t.Errorf("source_attribution = %s; want scf_official kept", attribution)
	}
	if len(rep.EdgesCarriedSynced) != 0 {
		t.Errorf("EdgesCarriedSynced = %v; want none", rep.EdgesCarriedSynced)
	}
	if got := heldBack(rep); !slices.Equal(got, []string{"GOV-04:outranked"}) {
		t.Errorf("EdgesCarriedHeldBack = %v; want [GOV-04:outranked]", got)
	}
}

// A copy that inherited verified from its source restarts at draft when a
// community_draft row rewrites its content (ADR 0018 "Tier follows
// content"), and the sync transition records verified -> draft (#3079).
func TestCarriedEdges_SyncReseedsAnInheritedVerifiedTierToDraft(t *testing.T) {
	pool, _, sample, copyID := syncFixture(t)
	setCopy(t, pool, copyID, "mapping_tier = 'verified'")

	rep := importCarried(t, pool, movedCrosswalk(sample))
	if tier, _ := copyTierAndAttribution(t, pool, copyID); tier != "draft" {
		t.Errorf("mapping_tier = %s; want draft", tier)
	}
	if len(rep.EdgesCarriedSynced) != 1 || rep.EdgesCarriedSynced[0].Tier != "draft" {
		t.Errorf("EdgesCarriedSynced = %+v; want GOV-04 at draft", rep.EdgesCarriedSynced)
	}
	if got := reseeded(rep); !slices.Equal(got, []string{"GOV-04:verified->draft"}) {
		t.Errorf("EdgesTierReseeded = %v; want the carried move listed once (#3083)", got)
	}
	if n := countRows(t, pool, `
		SELECT count(*) FROM fw_to_scf_edge_tier_transitions
		WHERE edge_id = $1 AND from_tier = 'verified' AND to_tier = 'draft'
		  AND starts_with(note, 'carried-sync:')`, copyID); n != 1 {
		t.Errorf("%d verified->draft carried-sync rows; want 1", n)
	}
}

// A draft copy rewritten by an scf_official row seeds at verified, as a new
// official edge does (ADR 0018 §2, #3079).
func TestCarriedEdges_SyncSeedsVerifiedFromAnOfficialRow(t *testing.T) {
	pool, _, sample, copyID := syncFixture(t)
	if tier, _ := copyTierAndAttribution(t, pool, copyID); tier != "draft" {
		t.Fatalf("carried copy starts at %s; want draft", tier)
	}

	rep := importCarried(t, pool, officialMovedCrosswalk(sample))
	if tier, attribution := copyTierAndAttribution(t, pool, copyID); tier != "verified" || attribution != "scf_official" {
		t.Errorf("carried copy = %s %s; want verified scf_official", tier, attribution)
	}
	if len(rep.EdgesCarriedSynced) != 1 || rep.EdgesCarriedSynced[0].Tier != "verified" || rep.EdgesCarriedSynced[0].Attribution != "scf_official" {
		t.Errorf("EdgesCarriedSynced = %+v; want GOV-04 verified scf_official", rep.EdgesCarriedSynced)
	}
	if got := reseeded(rep); !slices.Equal(got, []string{"GOV-04:draft->verified"}) {
		t.Errorf("EdgesTierReseeded = %v; want the carried move listed once (#3083)", got)
	}
	if n := countRows(t, pool, `
		SELECT count(*) FROM fw_to_scf_edge_tier_transitions
		WHERE edge_id = $1 AND from_tier = 'draft' AND to_tier = 'verified'
		  AND starts_with(note, 'carried-sync:')`, copyID); n != 1 {
		t.Errorf("%d draft->verified carried-sync rows; want 1", n)
	}
}

// A rejected copy whose content the sync rewrites with raised provenance
// stays rejected and is reported, as an owned edge is (#3079).
func TestCarriedEdges_RejectedCopyIsReportedUpgraded(t *testing.T) {
	pool, _, sample, copyID := syncFixture(t)
	setCopy(t, pool, copyID, "mapping_tier = 'rejected'")

	rep := importCarried(t, pool, officialMovedCrosswalk(sample))
	if rel, _, _, _, _ := edgeContent(t, pool, copyID); rel != "subset_of" {
		t.Errorf("relationship = %s; want the crosswalk's subset_of", rel)
	}
	if tier, _ := copyTierAndAttribution(t, pool, copyID); tier != "rejected" {
		t.Errorf("mapping_tier = %s; want rejected kept", tier)
	}
	var got []string
	for _, up := range rep.EdgesUpgradedOntoRejected {
		got = append(got, up.SCFAnchor+":"+up.PreviousAttribution+"->"+up.IncomingAttribution)
	}
	if !slices.Equal(got, []string{"GOV-04:community_draft->scf_official"}) {
		t.Errorf("EdgesUpgradedOntoRejected = %v; want [GOV-04:community_draft->scf_official]", got)
	}
	if got := reseeded(rep); len(got) != 0 {
		t.Errorf("EdgesTierReseeded = %v; a rejected copy keeps its tier", got)
	}
}

// reseeded lists rep's tier re-seeds as anchor:from->to.
func reseeded(rep soc2import.Report) []string {
	var got []string
	for _, rs := range rep.EdgesTierReseeded {
		got = append(got, rs.SCFAnchor+":"+rs.PreviousTier+"->"+rs.NewTier)
	}
	return got
}

// One import both restores a copy its retire pass retired and syncs the
// restored copy's content (#3079).
func TestCarriedEdges_RestoreThenSyncInOnePass(t *testing.T) {
	pool, byo, sample, copyID := syncFixture(t)
	rep := importCarried(t, pool, carriedCrosswalk(sample, "GOV-05"))
	if got := carriedCodes(rep.EdgesCarriedRetired); !slices.Equal(got, []string{"GOV-04"}) {
		t.Fatalf("EdgesCarriedRetired = %v; want [GOV-04]", got)
	}

	rep = importCarried(t, pool, movedCrosswalk(sample))
	if got := carriedCodes(rep.EdgesCarriedRestored); !slices.Equal(got, []string{"GOV-04"}) {
		t.Errorf("EdgesCarriedRestored = %v; want [GOV-04]", got)
	}
	if got := carriedCodes(rep.EdgesCarriedSynced); !slices.Equal(got, []string{"GOV-04"}) {
		t.Errorf("EdgesCarriedSynced = %v; want [GOV-04]", got)
	}
	if rel, strength, rationale, _, live := edgeContent(t, pool, copyID); !live || rel != "subset_of" || strength != 0.9 || rationale != "R-1 is a subset of GOV-04" {
		t.Errorf("carried copy on GOV-04@%s = live %v, %s %.2f %q; want live with the crosswalk's content", byo, live, rel, strength, rationale)
	}
}
