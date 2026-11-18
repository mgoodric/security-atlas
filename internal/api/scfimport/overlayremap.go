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
// One exception, the carry-forward (#2554, Matt's call, 2026-11-17), the
// overlay counterpart of #2315's catalog carry: an edge whose legacy anchor
// keeps its code and title in the current release (SameCodeAndTitle, the
// same single rule) is approved onto that anchor by the import. The legacy
// edge stays active, and the decision is recorded by
// mappingoverlay.RemapCarryActor in the tenant's own remap history and
// overlay audit trail. Every other edge stays open for the tenant's writers.
//
// Queueing is one act over every tenant, so it runs as the migrate role
// like QueueEdgeRemapProposals. Each proposal's tenant_id is the overlay
// edge's own, and the table's composite (tenant_id, overlay_edge_id) FK
// refuses any other. The carry is not global: it runs once per tenant, in
// that tenant's own transaction, as atlas_app under its app.current_tenant,
// so RLS binds it exactly as it binds a writer's approval.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/mappingoverlay"
	"github.com/mgoodric/security-atlas/internal/tenancy"
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
    COALESCE(btrim(ca.legacy_scf_id), '')::text,
    ARRAY(SELECT s.scf_id FROM scf_anchors s
      WHERE s.framework_version_id = cfv.id AND btrim(s.legacy_scf_id) = la.scf_id
      ORDER BY s.scf_id)::text[],
    ARRAY(SELECT s.title FROM scf_anchors s
      WHERE s.framework_version_id = cfv.id AND btrim(s.legacy_scf_id) = la.scf_id
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

// OverlayRemapReport is the outcome of QueueOverlayRemapProposals.
type OverlayRemapReport struct {
	// Queued counts the proposals this run queued, by class, carried or not.
	Queued map[RemapClass]int
	// Carried counts the overlay edges this run approved onto the current
	// release's anchor with the same code and title, over every tenant.
	Carried int
	// Closed counts the proposals this run kept legacy because the
	// requirement already maps to that anchor in the edge's tenant.
	Closed int
}

// overlayCarry is one overlay edge that qualifies for the carry-forward.
type overlayCarry struct {
	edgeID, targetVersionID uuid.UUID
	scfID                   string
}

// QueueOverlayRemapProposals queues one proposal per active overlay edge,
// in every tenant, on an older SCF release's anchor, then carries forward
// each edge whose anchor kept its code and title, tenant by tenant. An edge
// already queued for the current release is left alone and a decided
// proposal is not decided again, so a re-run queues and carries nothing. db
// must be the migrate (owner, BYPASSRLS) role, as for Import, and a member
// of atlas_app (migrations/bootstrap/01-roles.sql); run it after Import.
func QueueOverlayRemapProposals(ctx context.Context, db DBTX) (OverlayRemapReport, error) {
	conn, ok := db.(pgxBeginner)
	if !ok {
		return OverlayRemapReport{}, errors.New("scfimport: db must support BeginTx (pgx.Conn / pgxpool.Pool)")
	}
	queued, carries, err := queueOverlayRemaps(ctx, conn)
	if err != nil {
		return OverlayRemapReport{}, err
	}
	report := OverlayRemapReport{Queued: queued}
	for _, tenantID := range sortedTenants(carries) {
		carried, closed, err := carryOverlaysInTenant(ctx, conn, tenantID, carries[tenantID])
		if err != nil {
			return OverlayRemapReport{}, err
		}
		report.Carried += carried
		report.Closed += closed
	}
	return report, nil
}

// queueOverlayRemaps queues the proposals as the migrate role and returns,
// by tenant, the edges that qualify for the carry.
func queueOverlayRemaps(ctx context.Context, conn pgxBeginner) (map[RemapClass]int, map[uuid.UUID][]overlayCarry, error) {
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("scfimport: queue overlay remaps: begin tx: %w", err)
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
		return nil, nil, fmt.Errorf("scfimport: list overlay edges on legacy anchors: %w", err)
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
			return nil, nil, fmt.Errorf("scfimport: scan overlay edge on legacy anchor: %w", err)
		}
		edges = append(edges, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("scfimport: list overlay edges on legacy anchors: %w", err)
	}

	queued := map[RemapClass]int{}
	carries := map[uuid.UUID][]overlayCarry{}
	for _, r := range edges {
		class, successors := ClassifyLineage(r.lineage)
		tag, err := tx.Exec(ctx, insertOverlayProposalSQL, r.tenantID, r.edgeID, r.legacyAnchorID,
			r.lineage.LegacySCFID, r.legacyRelease, r.targetVersionID, r.targetRelease,
			string(class), successors)
		if err != nil {
			return nil, nil, fmt.Errorf("scfimport: queue overlay remap for %s: %w", r.lineage.LegacySCFID, err)
		}
		if tag.RowsAffected() == 1 {
			queued[class]++
		}
		if SameCodeAndTitle(r.lineage.HasCurrentAnchor, r.lineage.CurrentAnchorTitle, r.lineage.LegacyAnchorTitle) {
			carries[r.tenantID] = append(carries[r.tenantID], overlayCarry{
				edgeID: r.edgeID, targetVersionID: r.targetVersionID, scfID: r.lineage.LegacySCFID,
			})
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("scfimport: queue overlay remaps: commit: %w", err)
	}
	return queued, carries, nil
}

// carryOverlaysInTenant carries one tenant's qualifying edges in one
// transaction that runs as atlas_app under that tenant, so RLS binds every
// read and write the carry makes.
func carryOverlaysInTenant(ctx context.Context, conn pgxBeginner, tenantID uuid.UUID, carries []overlayCarry) (carried, closed int, err error) {
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, 0, fmt.Errorf("scfimport: carry overlays: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE atlas_app"); err != nil {
		return 0, 0, fmt.Errorf("scfimport: carry overlays: become atlas_app: %w", err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config($1, $2, true)", tenancy.GUCName, tenantID.String()); err != nil {
		return 0, 0, fmt.Errorf("scfimport: carry overlays: set tenant: %w", err)
	}
	for _, c := range carries {
		outcome, err := mappingoverlay.CarryRemap(ctx, tx, tenantID, c.edgeID, c.targetVersionID, c.scfID)
		if err != nil {
			return 0, 0, fmt.Errorf("scfimport: carry overlay in tenant %s: %w", tenantID, err)
		}
		switch outcome {
		case mappingoverlay.CarryCarried:
			carried++
		case mappingoverlay.CarryClosed:
			closed++
		case mappingoverlay.CarrySkipped:
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, fmt.Errorf("scfimport: carry overlays: commit: %w", err)
	}
	return carried, closed, nil
}

// sortedTenants orders the tenants so a run's log and failures are stable.
func sortedTenants(m map[uuid.UUID][]overlayCarry) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	slices.SortFunc(out, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	return out
}
