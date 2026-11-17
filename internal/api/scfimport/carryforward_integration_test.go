//go:build integration

package scfimport_test

// #2554: the import's carry-forward closes a proposal it can never approve,
// and runs each tenant's overlay carry under that tenant's RLS. The
// fixtures are #1777's and #1804's (edgeremap_integration_test.go,
// overlayremap_integration_test.go): TRM-01 keeps its code and title in B.

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/mappingoverlay"
)

// From the review of #2552: an unchanged catalog edge whose requirement
// already maps to the same code in the current release can never be
// approved (scf_edge_remap_decide refuses the pair), so the import closes
// its proposal as kept_legacy with the reason, instead of leaving it open.
func TestEdgeRemapQueue_ClosesAnUnchangedProposalWhoseTargetPairExists(t *testing.T) {
	admin, _ := setupEdgeRemap(t, nil)
	ctx := context.Background()
	r03 := requirementID(t, admin, "R-03")
	var legacy uuid.UUID
	if err := admin.QueryRow(ctx, `
		INSERT INTO fw_to_scf_edges (id, framework_requirement_id, scf_anchor_id, relationship_type, strength, source_attribution, rationale)
		VALUES (gen_random_uuid(), $1, $2, 'equal', 1, 'community_draft', 'on A') RETURNING id`,
		r03, anchorID(t, admin, edgeReleaseA, "TRM-01")).Scan(&legacy); err != nil {
		t.Fatalf("seed legacy edge: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO fw_to_scf_edges (id, framework_requirement_id, scf_anchor_id, relationship_type, strength, source_attribution, rationale)
		VALUES (gen_random_uuid(), $1, $2, 'equal', 1, 'community_draft', 'already on B')`,
		r03, anchorID(t, admin, edgeReleaseB, "TRM-01")); err != nil {
		t.Fatalf("seed target edge: %v", err)
	}

	report, err := scfimport.QueueEdgeRemapProposals(ctx, admin)
	if err != nil {
		t.Fatalf("QueueEdgeRemapProposals: %v", err)
	}
	if report.Queued[scfimport.RemapUnchanged] != 1 || report.Carried != 0 || report.Closed != 1 {
		t.Fatalf("report = %+v; want one unchanged queued, none carried, one closed", report)
	}
	var status, decision, decidedBy, reason string
	if err := admin.QueryRow(ctx, `
		SELECT p.status, d.decision, d.decided_by, d.reason
		FROM scf_edge_remap_proposals p JOIN scf_edge_remap_decisions d ON d.proposal_id = p.id
		WHERE p.edge_id = $1`, legacy).Scan(&status, &decision, &decidedBy, &reason); err != nil {
		t.Fatalf("read closed proposal: %v", err)
	}
	if status != "kept_legacy" || decision != "kept_legacy" || decidedBy != scfimport.CarryForwardDecider ||
		!strings.Contains(reason, "already maps to TRM-01") {
		t.Fatalf("proposal %s, decision %s by %s (%q); want kept_legacy by %s naming the pair",
			status, decision, decidedBy, reason, scfimport.CarryForwardDecider)
	}
	if e, ok := readEdge(t, admin, "id = $1", legacy); !ok || e.RetiredAt != nil {
		t.Fatalf("legacy edge = %+v (found %v); want it live", e, ok)
	}
	again, err := scfimport.QueueEdgeRemapProposals(ctx, admin)
	if err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if again.Carried != 0 || again.Closed != 0 {
		t.Fatalf("re-run = %+v; want nothing carried or closed", again)
	}
}

// The overlay counterpart: a tenant that already holds the pair on the
// current release gets its proposal closed, in its own history, and its
// legacy edge is left active.
func TestOverlayRemapQueue_ClosesAnUnchangedProposalWhoseTargetPairExists(t *testing.T) {
	f := setupOverlayRemap(t)
	ctx := withTenant(t, f.a.String())
	store := mappingoverlay.NewStore(f.app)
	legacy, err := store.Create(ctx, newOverlay(overlayPair{"R-04", "TRM-01", "equal", 1}), "seed-a")
	if err != nil {
		t.Fatalf("legacy overlay on A: %v", err)
	}
	onB := newOverlay(overlayPair{"R-04", "TRM-01", "equal", 1})
	onB.SCFRelease = edgeReleaseB
	if _, err := store.Create(ctx, onB, "seed-a"); err != nil {
		t.Fatalf("overlay already on B: %v", err)
	}

	report, err := scfimport.QueueOverlayRemapProposals(context.Background(), f.admin)
	if err != nil {
		t.Fatalf("QueueOverlayRemapProposals: %v", err)
	}
	if report.Queued[scfimport.RemapUnchanged] != 1 || report.Carried != 0 || report.Closed != 1 {
		t.Fatalf("report = %+v; want one unchanged queued, none carried, one closed", report)
	}
	var status, decision, decidedBy, reason string
	var tenant uuid.UUID
	if err := f.admin.QueryRow(context.Background(), `
		SELECT p.status, d.decision, d.decided_by, d.reason, d.tenant_id
		FROM scf_overlay_remap_proposals p JOIN scf_overlay_remap_decisions d ON d.proposal_id = p.id
		WHERE p.overlay_edge_id = $1`, legacy.ID).Scan(&status, &decision, &decidedBy, &reason, &tenant); err != nil {
		t.Fatalf("read closed proposal: %v", err)
	}
	if status != "kept_legacy" || decision != "kept_legacy" || decidedBy != mappingoverlay.RemapCarryActor ||
		tenant != f.a || !strings.Contains(reason, "already maps to TRM-01") {
		t.Fatalf("proposal %s, decision %s by %s in %s (%q); want kept_legacy by %s in tenant A",
			status, decision, decidedBy, tenant, reason, mappingoverlay.RemapCarryActor)
	}
	if o := readOverlay(t, f.admin, legacy.ID); o.Status != "active" {
		t.Fatalf("legacy overlay is %s; want active", o.Status)
	}
}

// The carry binds RLS: under tenant B's context, tenant A's proposal is not
// visible, so CarryRemap named with A's ids writes nothing.
func TestOverlayCarry_UnderAnotherTenantsContextWritesNothing(t *testing.T) {
	f := setupOverlayRemap(t)
	ctx := context.Background()
	// Tenant A's TRM-01 proposal was carried at import; reopen the question
	// with a fresh A edge on TRM-01 queued but not yet carried.
	edge, err := mappingoverlay.NewStore(f.app).Create(withTenant(t, f.a.String()),
		newOverlay(overlayPair{"R-05", "TRM-01", "equal", 1}), "seed-a")
	if err != nil {
		t.Fatalf("tenant A overlay: %v", err)
	}
	if _, err := f.admin.Exec(ctx, `
		INSERT INTO scf_overlay_remap_proposals (tenant_id, overlay_edge_id, legacy_anchor_id, legacy_scf_id,
		    legacy_release, target_framework_version_id, target_release, class, successor_scf_ids)
		SELECT $1, $2, a.id, 'TRM-01', $3, b.framework_version_id, $4, 'unchanged', ARRAY['TRM-01']
		FROM scf_anchors a, scf_anchors b WHERE a.id = $5 AND b.id = $6`,
		f.a, edge.ID, edgeReleaseA, edgeReleaseB,
		anchorID(t, f.admin, edgeReleaseA, "TRM-01"), anchorID(t, f.admin, edgeReleaseB, "TRM-01")); err != nil {
		t.Fatalf("queue A's proposal: %v", err)
	}
	var target uuid.UUID
	if err := f.admin.QueryRow(ctx, `SELECT framework_version_id FROM scf_anchors WHERE id = $1`,
		anchorID(t, f.admin, edgeReleaseB, "TRM-01")).Scan(&target); err != nil {
		t.Fatalf("target release: %v", err)
	}
	aBefore, bBefore := overlayCount(t, f.admin, f.a), overlayCount(t, f.admin, f.b)

	tx, err := f.app.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.current_tenant', $1, true)`, f.b.String()); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	outcome, err := mappingoverlay.CarryRemap(ctx, tx, f.a, edge.ID, target, "TRM-01")
	if err != nil {
		t.Fatalf("CarryRemap: %v", err)
	}
	if outcome != mappingoverlay.CarrySkipped {
		t.Fatalf("outcome = %v; want CarrySkipped", outcome)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if a, b := overlayCount(t, f.admin, f.a), overlayCount(t, f.admin, f.b); a != aBefore || b != bBefore {
		t.Fatalf("overlay counts A %d->%d, B %d->%d; want unchanged", aBefore, a, bBefore, b)
	}
	var status string
	if err := f.admin.QueryRow(ctx, `SELECT status FROM scf_overlay_remap_proposals WHERE overlay_edge_id = $1`, edge.ID).Scan(&status); err != nil {
		t.Fatalf("read proposal: %v", err)
	}
	if status != "open" {
		t.Fatalf("A's proposal is %s after B's carry; want open", status)
	}
}
