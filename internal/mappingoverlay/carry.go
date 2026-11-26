package mappingoverlay

// The SCF import's carry-forward of overlay edges (#2554).
//
// After a new SCF release becomes current, an overlay edge whose legacy
// anchor keeps its code and title in that release (scfimport's
// SameCodeAndTitle, the one rule controls and catalog edges carry on) is
// approved onto that anchor by the import itself. The approval is a writer's
// approval with two differences:
//
//   - the legacy edge stays active, as a catalog carry leaves its legacy
//     edge (#2315 D3), so a read pinned to the old release is unchanged;
//   - the actor and decider are RemapCarryActor, not a person.
//
// It runs as atlas_app under the edge's own tenant, so RLS binds every read
// and write: the import cannot reach another tenant's rows from here.

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// RemapCarryActor is the actor on an overlay edge the import carried, on its
// audit row, and the decided_by of its remap decision.
const RemapCarryActor = "system:scf-import"

// CarryOutcome is what CarryRemap did with one proposal.
type CarryOutcome int

const (
	// CarrySkipped: the proposal is not open (a person decided it, or an
	// earlier run carried it), or its legacy edge changed. Nothing written.
	CarrySkipped CarryOutcome = iota
	// CarryCarried: a copy of the edge now sits on the same code in the
	// target release, and the proposal is approved.
	CarryCarried
	// CarryClosed: the requirement already maps to that anchor, through the
	// tenant's own active overlay or a live catalog edge, so the approval
	// could never succeed. The proposal is kept legacy, with the reason.
	CarryClosed
)

// CarryRemap carries the open proposal of one overlay edge for one target
// release onto the anchor with code scfID in that release, keeping the
// legacy edge. The caller decides the edge qualifies (same code and title).
//
// tx must run as atlas_app with app.current_tenant set to tenantID. A
// proposal of another tenant is not visible under RLS and is skipped.
func CarryRemap(ctx context.Context, tx pgx.Tx, tenantID, overlayEdgeID, targetVersionID uuid.UUID, scfID string) (CarryOutcome, error) {
	var proposalID uuid.UUID
	var status, targetRelease string
	err := tx.QueryRow(ctx, `
		SELECT id, status, target_release FROM scf_overlay_remap_proposals
		WHERE tenant_id = $1 AND overlay_edge_id = $2 AND target_framework_version_id = $3`,
		tenantID, overlayEdgeID, targetVersionID).Scan(&proposalID, &status, &targetRelease)
	if errors.Is(err, pgx.ErrNoRows) {
		return CarrySkipped, nil
	}
	if err != nil {
		return CarrySkipped, fmt.Errorf("mapping overlay: read remap proposal to carry: %w", err)
	}
	if status != RemapOpen {
		return CarrySkipped, nil
	}

	taken, err := remapTargetTaken(ctx, tx, tenantID, overlayEdgeID, targetVersionID, scfID)
	if err != nil {
		return CarrySkipped, err
	}
	in := RemapDecideInput{SCFID: scfID, Actor: RemapCarryActor, DecidedBy: RemapCarryActor}
	outcome := CarryCarried
	if taken {
		in.Decision = RemapKeptLegacy
		in.Reason = fmt.Sprintf("closed by the SCF import: the requirement already maps to %s in %s", scfID, targetRelease)
		outcome = CarryClosed
	} else {
		in.Decision = RemapApproved
		in.Reason = fmt.Sprintf("carried forward: %s has the same code and title in %s", scfID, targetRelease)
	}
	if _, err := decideRemapTx(ctx, tx, tenantID, proposalID, in, false); err != nil {
		// The edge was retired after it was queued: a person changed it,
		// so the import leaves the proposal to them. The check is in Go,
		// before any write, so the transaction is still usable.
		if errors.Is(err, ErrRemapEdgeChanged) {
			return CarrySkipped, nil
		}
		return CarrySkipped, fmt.Errorf("mapping overlay: carry %s forward: %w", scfID, err)
	}
	return outcome, nil
}

// remapTargetTaken reports whether the edge's requirement already maps to
// the anchor scfID in the target release, so approving the proposal would be
// refused: the tenant holds an active overlay edge on that pair, or a live
// catalog edge joins it (the rule Create and approval apply).
func remapTargetTaken(ctx context.Context, tx pgx.Tx, tenantID, overlayEdgeID, targetVersionID uuid.UUID, scfID string) (bool, error) {
	var requirementID, anchorID uuid.UUID
	var overlayTaken bool
	err := tx.QueryRow(ctx, `
		SELECT o.framework_requirement_id, a.id,
		       EXISTS (SELECT 1 FROM fw_to_scf_overlay_edges x
		               WHERE x.tenant_id = o.tenant_id
		                 AND x.framework_requirement_id = o.framework_requirement_id
		                 AND x.scf_anchor_id = a.id
		                 AND x.status = 'active')
		FROM fw_to_scf_overlay_edges o
		JOIN scf_anchors a ON a.framework_version_id = $3 AND a.scf_id = $4
		WHERE o.tenant_id = $1 AND o.id = $2`,
		tenantID, overlayEdgeID, targetVersionID, scfID).Scan(&requirementID, &anchorID, &overlayTaken)
	if errors.Is(err, pgx.ErrNoRows) {
		// No such anchor: the approval will say so.
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("mapping overlay: check carry target for %s: %w", scfID, err)
	}
	if overlayTaken {
		return true, nil
	}
	var catalog *CatalogEdgeExistsError
	err = refuseCatalogDuplicate(ctx, dbx.New(tx), pgUUID(requirementID), pgUUID(anchorID))
	if errors.As(err, &catalog) {
		return true, nil
	}
	return false, err
}
