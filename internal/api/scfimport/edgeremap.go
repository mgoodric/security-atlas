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

// QueueEdgeRemapProposals queues one proposal per live catalog edge on an
// older SCF release's anchor and returns how many this run queued, by class.
// An edge already queued for the current release is left alone, so a re-run
// queues nothing. db must be the migrate (owner) role, as for Import; run it
// after Import.
func QueueEdgeRemapProposals(ctx context.Context, db DBTX) (map[RemapClass]int, error) {
	conn, ok := db.(pgxBeginner)
	if !ok {
		return nil, errors.New("scfimport: db must support BeginTx (pgx.Conn / pgxpool.Pool)")
	}
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("scfimport: queue edge remaps: begin tx: %w", err)
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
		return nil, fmt.Errorf("scfimport: list edges on legacy anchors: %w", err)
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
			return nil, fmt.Errorf("scfimport: scan edge on legacy anchor: %w", err)
		}
		edges = append(edges, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scfimport: list edges on legacy anchors: %w", err)
	}

	queued := map[RemapClass]int{}
	for _, r := range edges {
		class, successors := ClassifyLineage(r.lineage)
		tag, err := tx.Exec(ctx, insertEdgeProposalSQL, r.edgeID, r.legacyAnchorID,
			r.lineage.LegacySCFID, r.legacyRelease, r.targetVersionID, r.targetRelease,
			string(class), successors)
		if err != nil {
			return nil, fmt.Errorf("scfimport: queue edge remap for %s: %w", r.lineage.LegacySCFID, err)
		}
		if tag.RowsAffected() == 1 {
			queued[class]++
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("scfimport: queue edge remaps: commit: %w", err)
	}
	return queued, nil
}
