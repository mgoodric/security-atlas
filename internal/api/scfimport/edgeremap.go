package scfimport

// The crosswalk-edge remap queue (#1777).
//
// The control queue (#1775) has a global counterpart: after a new SCF
// release becomes current, every live catalog edge (fw_to_scf_edges) left on
// an anchor of an older release gets one proposal in
// scf_edge_remap_proposals, classified by ClassifyLineage exactly as a
// control on that anchor would be. Nothing moves: every edge proposal starts
// open, and only a super-admin decides it (internal/crosswalkremap).
//
// One exception, the carry-forward (#2315, Matt's call, 2026-11-15): an edge
// whose legacy anchor keeps its code and title in the current release
// (SameCodeAndTitle, the rule RebindControls moves controls on) is approved
// onto that anchor in the same transaction that queues it, through the same
// scf_edge_remap_decide a super-admin uses. The approval copies the edge and
// leaves the legacy one in place, so default coverage, which reads the
// current release (#2203), keeps the requirement, and a read pinned to the
// legacy release still finds its edge. Every other edge stays open for a
// person: a changed title is flagged, never carried and never dropped.
//
// Tenant overlay edges (fw_to_scf_overlay_edges, #1735) are tenant-scoped
// and are not queued here; QueueOverlayRemapProposals (#1804) queues them
// in a tenant-scoped table.

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// legacyEdgesSQL lists every live catalog edge on an anchor of an SCF
// release that is no longer current, with the lineage the current release
// gives that anchor. The lineage columns match ListControlsOnLegacySCFAnchors.
const legacyEdgesSQL = `
SELECT
    e.id,
    la.id,
    la.scf_id,
    la.title,
    lfv.version,
    cfv.id,
    cfv.version,
    ca.id IS NOT NULL,
    ca.title,
    COALESCE(ca.legacy_scf_id, '')::text,
    ARRAY(SELECT s.scf_id FROM scf_anchors s
      WHERE s.framework_version_id = cfv.id AND s.legacy_scf_id = la.scf_id
      ORDER BY s.scf_id)::text[],
    ARRAY(SELECT s.title FROM scf_anchors s
      WHERE s.framework_version_id = cfv.id AND s.legacy_scf_id = la.scf_id
      ORDER BY s.scf_id)::text[],
    EXISTS (SELECT 1 FROM scf_anchors s
      WHERE s.framework_version_id = cfv.id
        AND COALESCE(btrim(s.legacy_scf_id), '') <> '')
FROM fw_to_scf_edges e
JOIN scf_anchors la            ON la.id = e.scf_anchor_id
JOIN framework_versions lfv    ON lfv.id = la.framework_version_id
JOIN frameworks f              ON f.id = lfv.framework_id AND f.slug = 'scf' AND f.tenant_id IS NULL
JOIN framework_versions cfv    ON cfv.framework_id = f.id AND cfv.status = 'current'
LEFT JOIN scf_anchors ca       ON ca.framework_version_id = cfv.id AND ca.scf_id = la.scf_id
WHERE e.retired_at IS NULL
  AND lfv.id <> cfv.id
ORDER BY la.scf_id, e.id`

const insertEdgeProposalSQL = `
INSERT INTO scf_edge_remap_proposals (
    edge_id, legacy_anchor_id, legacy_scf_id,
    legacy_release, target_framework_version_id, target_release, class,
    successor_scf_ids)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (edge_id, target_framework_version_id) DO NOTHING`

// edgeProposalSQL reads the proposal of one edge for one target release,
// whether this run queued it or an earlier one did.
const edgeProposalSQL = `
SELECT id, status FROM scf_edge_remap_proposals
WHERE edge_id = $1 AND target_framework_version_id = $2`

// targetEdgeExistsSQL reports whether the edge's requirement already maps
// to the current anchor, retired or not. scf_edge_remap_decide refuses that
// approval, and a crosswalk import has already decided the pair.
const targetEdgeExistsSQL = `
SELECT EXISTS (
    SELECT 1 FROM fw_to_scf_edges x
    JOIN fw_to_scf_edges e ON e.framework_requirement_id = x.framework_requirement_id
    JOIN scf_anchors ca ON ca.id = x.scf_anchor_id
    WHERE e.id = $1 AND ca.framework_version_id = $2 AND ca.scf_id = $3)`

// carryEdgeSQL approves one proposal onto the anchor with the legacy code,
// recorded in scf_edge_remap_decisions like any approval.
const carryEdgeSQL = `
SELECT new_edge_id FROM scf_edge_remap_decide($1, 'approved', $2, $3, $4, true)`

// CarryForwardDecider is the decided_by of a carry-forward approval in
// scf_edge_remap_decisions: the import, not a person.
const CarryForwardDecider = "system:scf-import"

// EdgeRemapReport is the outcome of QueueEdgeRemapProposals.
type EdgeRemapReport struct {
	// Queued counts the proposals this run queued, by class, carried or not.
	Queued map[RemapClass]int
	// Carried counts the edges this run approved onto the current release's
	// anchor with the same code and title (#2315).
	Carried int
}

// QueueEdgeRemapProposals queues one proposal per live catalog edge on an
// older SCF release's anchor, carries forward the edges whose anchor kept
// its code and title, and reports both. An edge already queued for the
// current release is not queued again, and a decided proposal is not decided
// again, so a re-run queues and carries nothing. db must be the migrate
// (owner) role, as for Import; run it after Import.
func QueueEdgeRemapProposals(ctx context.Context, db DBTX) (EdgeRemapReport, error) {
	conn, ok := db.(pgxBeginner)
	if !ok {
		return EdgeRemapReport{}, errors.New("scfimport: db must support BeginTx (pgx.Conn / pgxpool.Pool)")
	}
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return EdgeRemapReport{}, fmt.Errorf("scfimport: queue edge remaps: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	type edgeRow struct {
		edgeID, legacyAnchorID       uuid.UUID
		legacyRelease, targetRelease string
		targetVersionID              uuid.UUID
		lineage                      AnchorLineage
	}
	rows, err := tx.Query(ctx, legacyEdgesSQL)
	if err != nil {
		return EdgeRemapReport{}, fmt.Errorf("scfimport: list edges on legacy anchors: %w", err)
	}
	var edges []edgeRow
	for rows.Next() {
		var r edgeRow
		if err := rows.Scan(&r.edgeID, &r.legacyAnchorID,
			&r.lineage.LegacySCFID, &r.lineage.LegacyAnchorTitle, &r.legacyRelease,
			&r.targetVersionID, &r.targetRelease, &r.lineage.HasCurrentAnchor,
			&r.lineage.CurrentAnchorTitle, &r.lineage.CurrentAnchorLegacySCFID,
			&r.lineage.LegacySuccessorSCFIDs, &r.lineage.LegacySuccessorTitles,
			&r.lineage.CurrentReleaseHasLegacyIDs); err != nil {
			rows.Close()
			return EdgeRemapReport{}, fmt.Errorf("scfimport: scan edge on legacy anchor: %w", err)
		}
		edges = append(edges, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return EdgeRemapReport{}, fmt.Errorf("scfimport: list edges on legacy anchors: %w", err)
	}

	report := EdgeRemapReport{Queued: map[RemapClass]int{}}
	for _, r := range edges {
		class, successors := ClassifyLineage(r.lineage)
		tag, err := tx.Exec(ctx, insertEdgeProposalSQL, r.edgeID, r.legacyAnchorID,
			r.lineage.LegacySCFID, r.legacyRelease, r.targetVersionID, r.targetRelease,
			string(class), successors)
		if err != nil {
			return EdgeRemapReport{}, fmt.Errorf("scfimport: queue edge remap for %s: %w", r.lineage.LegacySCFID, err)
		}
		if tag.RowsAffected() == 1 {
			report.Queued[class]++
		}
		if !SameCodeAndTitle(r.lineage.HasCurrentAnchor, r.lineage.CurrentAnchorTitle, r.lineage.LegacyAnchorTitle) {
			continue
		}
		carried, err := carryEdge(ctx, tx, r.edgeID, r.targetVersionID, r.lineage.LegacySCFID, r.targetRelease)
		if err != nil {
			return EdgeRemapReport{}, err
		}
		if carried {
			report.Carried++
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return EdgeRemapReport{}, fmt.Errorf("scfimport: queue edge remaps: commit: %w", err)
	}
	return report, nil
}

// carryEdge approves the edge's open proposal onto the anchor with the same
// code in the target release. It carries nothing when a person already
// decided the proposal (a rejection stands until a person changes it) or
// when the requirement already maps to that anchor.
func carryEdge(ctx context.Context, tx pgx.Tx, edgeID, targetVersionID uuid.UUID, scfID, targetRelease string) (bool, error) {
	var proposalID uuid.UUID
	var status string
	if err := tx.QueryRow(ctx, edgeProposalSQL, edgeID, targetVersionID).Scan(&proposalID, &status); err != nil {
		return false, fmt.Errorf("scfimport: read edge proposal for %s: %w", scfID, err)
	}
	if status != "open" {
		return false, nil
	}
	var exists bool
	if err := tx.QueryRow(ctx, targetEdgeExistsSQL, edgeID, targetVersionID, scfID).Scan(&exists); err != nil {
		return false, fmt.Errorf("scfimport: check carried edge for %s: %w", scfID, err)
	}
	if exists {
		return false, nil
	}
	reason := fmt.Sprintf("carried forward: %s has the same code and title in %s", scfID, targetRelease)
	var newEdge uuid.UUID
	if err := tx.QueryRow(ctx, carryEdgeSQL, proposalID, scfID, reason, CarryForwardDecider).Scan(&newEdge); err != nil {
		return false, fmt.Errorf("scfimport: carry edge on %s forward: %w", scfID, err)
	}
	return true, nil
}
