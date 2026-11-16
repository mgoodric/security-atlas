package scfimport

// The overlay-edge remap queue (#1804).
//
// The third subject of the remap queue, after controls (#1775) and catalog
// crosswalk edges (#1777): after a new SCF release becomes current, every
// active tenant overlay edge (fw_to_scf_overlay_edges, #1735) left on an
// anchor of an older release gets one proposal in
// scf_overlay_remap_proposals, classified by ClassifyLineage exactly as a
// control or catalog edge on that anchor would be. Nothing moves: every
// proposal starts open, and a writer of the edge's own tenant decides it
// (mappingoverlay.Store.DecideRemap).
//
// Queueing is one act over every tenant, so it runs as the migrate role
// like QueueEdgeRemapProposals. Each proposal's tenant_id is the overlay
// edge's own, and the table's composite (tenant_id, overlay_edge_id) FK
// refuses any other.

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// legacyOverlayEdgesSQL lists every active overlay edge on an anchor of an
// SCF release that is no longer current, with its tenant and the lineage the
// current release gives that anchor. The lineage columns match
// legacyEdgesSQL.
const legacyOverlayEdgesSQL = `
SELECT
    o.id,
    o.tenant_id,
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
FROM fw_to_scf_overlay_edges o
JOIN scf_anchors la            ON la.id = o.scf_anchor_id
JOIN framework_versions lfv    ON lfv.id = la.framework_version_id
JOIN frameworks f              ON f.id = lfv.framework_id AND f.slug = 'scf' AND f.tenant_id IS NULL
JOIN framework_versions cfv    ON cfv.framework_id = f.id AND cfv.status = 'current'
LEFT JOIN scf_anchors ca       ON ca.framework_version_id = cfv.id AND ca.scf_id = la.scf_id
WHERE o.status = 'active'
  AND lfv.id <> cfv.id
ORDER BY o.tenant_id, la.scf_id, o.id`

const insertOverlayProposalSQL = `
INSERT INTO scf_overlay_remap_proposals (
    tenant_id, overlay_edge_id, legacy_anchor_id, legacy_scf_id,
    legacy_release, target_framework_version_id, target_release, class,
    successor_scf_ids)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (overlay_edge_id, target_framework_version_id) DO NOTHING`

// QueueOverlayRemapProposals queues one proposal per active overlay edge,
// in every tenant, on an older SCF release's anchor and returns how many
// this run queued, by class. An edge already queued for the current release
// is left alone, so a re-run queues nothing. db must be the migrate (owner,
// BYPASSRLS) role, as for Import; run it after Import.
func QueueOverlayRemapProposals(ctx context.Context, db DBTX) (map[RemapClass]int, error) {
	conn, ok := db.(pgxBeginner)
	if !ok {
		return nil, errors.New("scfimport: db must support BeginTx (pgx.Conn / pgxpool.Pool)")
	}
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("scfimport: queue overlay remaps: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	type overlayRow struct {
		edgeID, tenantID, legacyAnchorID uuid.UUID
		legacyRelease, targetRelease     string
		targetVersionID                  uuid.UUID
		lineage                          AnchorLineage
	}
	rows, err := tx.Query(ctx, legacyOverlayEdgesSQL)
	if err != nil {
		return nil, fmt.Errorf("scfimport: list overlay edges on legacy anchors: %w", err)
	}
	var edges []overlayRow
	for rows.Next() {
		var r overlayRow
		if err := rows.Scan(&r.edgeID, &r.tenantID, &r.legacyAnchorID,
			&r.lineage.LegacySCFID, &r.lineage.LegacyAnchorTitle, &r.legacyRelease,
			&r.targetVersionID, &r.targetRelease, &r.lineage.HasCurrentAnchor,
			&r.lineage.CurrentAnchorTitle, &r.lineage.CurrentAnchorLegacySCFID,
			&r.lineage.LegacySuccessorSCFIDs, &r.lineage.LegacySuccessorTitles,
			&r.lineage.CurrentReleaseHasLegacyIDs); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scfimport: scan overlay edge on legacy anchor: %w", err)
		}
		edges = append(edges, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scfimport: list overlay edges on legacy anchors: %w", err)
	}

	queued := map[RemapClass]int{}
	for _, r := range edges {
		class, successors := ClassifyLineage(r.lineage)
		tag, err := tx.Exec(ctx, insertOverlayProposalSQL, r.tenantID, r.edgeID, r.legacyAnchorID,
			r.lineage.LegacySCFID, r.legacyRelease, r.targetVersionID, r.targetRelease,
			string(class), successors)
		if err != nil {
			return nil, fmt.Errorf("scfimport: queue overlay remap for %s: %w", r.lineage.LegacySCFID, err)
		}
		if tag.RowsAffected() == 1 {
			queued[class]++
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("scfimport: queue overlay remaps: commit: %w", err)
	}
	return queued, nil
}
