package mappingoverlay

// Overlay-edge remap proposals (#1804).
//
// After a new SCF release becomes current, scfimport.QueueOverlayRemapProposals
// queues one proposal per active overlay edge left on an older release's
// anchor, in that edge's tenant. A writer of the tenant resolves it:
//
//   - approve creates an overlay edge on the chosen anchor in the target
//     release, with the legacy edge's requirement, relationship, strength,
//     rationale and source_ref, and retires the legacy edge with reason
//     "remapped"; final.
//   - reject leaves the edge on its legacy anchor; the proposal can be
//     approved or kept legacy later.
//   - keep legacy leaves the edge on its legacy anchor; final.
//
// Everything runs as atlas_app under the caller's tenant: RLS hides another
// tenant's proposals and edges, so their ids are simply not found. Each
// decision sets the proposal's status and appends a row to
// scf_overlay_remap_decisions; an approval also writes the overlay audit
// trail (create for the new edge, retire for the legacy one).

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// Remap proposal statuses and the decisions that set them.
const (
	RemapOpen       = "open"
	RemapApproved   = "approved"
	RemapRejected   = "rejected"
	RemapKeptLegacy = "kept_legacy"
)

// RemapRetirementReason is the reason recorded on a legacy overlay edge
// retired by an approved remap.
const RemapRetirementReason = "remapped"

// remapClasses are the proposal classes, as scfimport.RemapClasses.
var remapClasses = []string{"unchanged", "renamed", "split", "reused_id", "no_successor"}

// maxRemapReason bounds a decision's reason, matching the column CHECK.
const maxRemapReason = 2000

var (
	// ErrRemapNotFound: the caller's tenant has no remap proposal with that
	// id. Another tenant's proposal is this too: RLS hides it.
	ErrRemapNotFound = errors.New("mapping overlay: remap proposal not found")
	// ErrRemapDecided: the proposal is approved or kept legacy, or (for a
	// reject) already rejected.
	ErrRemapDecided = errors.New("mapping overlay: remap proposal already decided")
	// ErrRemapAnchorRequired: the proposal has no single successor (split,
	// no_successor), so approval must name the anchor.
	ErrRemapAnchorRequired = errors.New("mapping overlay: choose the successor anchor (scf_id) to approve this proposal")
	// ErrRemapAnchorNotInRelease: the chosen code is not in the proposal's
	// target release.
	ErrRemapAnchorNotInRelease = errors.New("mapping overlay: the chosen anchor is not in the target SCF release")
	// ErrRemapEdgeChanged: the legacy overlay edge was retired since the
	// proposal was queued.
	ErrRemapEdgeChanged = errors.New("mapping overlay: the overlay edge changed since the proposal was queued")
)

// InvalidRemapError is a decision that fails the shape rules: an unknown
// decision, no decider, a reason over the limit, a bad filter.
type InvalidRemapError struct{ Reason string }

func (e *InvalidRemapError) Error() string {
	return "mapping overlay: invalid remap decision: " + e.Reason
}

// RemapProposal is one queued overlay-edge remap, with the codes a reviewer
// needs to read it.
type RemapProposal struct {
	ID               uuid.UUID
	OverlayEdgeID    uuid.UUID
	Framework        string
	FrameworkVersion string
	RequirementCode  string
	RelationshipType string
	LegacySCFID      string
	LegacyRelease    string
	TargetRelease    string
	Class            string
	Status           string
	Successors       []string
	// Set on an approved proposal.
	TargetSCFID      string
	NewOverlayEdgeID *uuid.UUID
	CreatedAt        time.Time
}

// RemapFilter narrows ListRemapProposals. Both fields are optional.
type RemapFilter struct {
	Class  string
	Status string
}

// Validate checks the filter's values.
func (f RemapFilter) Validate() error {
	if f.Class != "" && !slices.Contains(remapClasses, f.Class) {
		return &InvalidRemapError{Reason: "class must be one of " + strings.Join(remapClasses, ", ")}
	}
	if f.Status != "" && !slices.Contains([]string{RemapOpen, RemapApproved, RemapRejected, RemapKeptLegacy}, f.Status) {
		return &InvalidRemapError{Reason: "status must be one of open, approved, rejected, kept_legacy"}
	}
	return nil
}

// RemapDecideInput is one decision. SCFID applies to an approval: the
// successor code in the target release; empty takes the proposal's single
// successor. Actor is the credential recorded on the overlay edges and
// their audit rows, as for Create and Retire; DecidedBy is the deciding
// person recorded on the decision.
type RemapDecideInput struct {
	ProposalID string
	Decision   string
	SCFID      string
	Reason     string
	Actor      string
	DecidedBy  string
}

// Validate checks the input shape that needs no database.
func (in RemapDecideInput) Validate() error {
	switch {
	case !slices.Contains([]string{RemapApproved, RemapRejected, RemapKeptLegacy}, in.Decision):
		return &InvalidRemapError{Reason: "decision must be approved, rejected or kept_legacy"}
	case strings.TrimSpace(in.DecidedBy) == "":
		return &InvalidRemapError{Reason: "a deciding person is required"}
	case strings.TrimSpace(in.Actor) == "":
		return &InvalidRemapError{Reason: "an actor is required"}
	case len(strings.TrimSpace(in.Reason)) > maxRemapReason:
		return &InvalidRemapError{Reason: fmt.Sprintf("reason exceeds %d characters", maxRemapReason)}
	}
	return nil
}

// RemapDecision is one decision as the history records it. The New* fields
// are set on an approval only.
type RemapDecision struct {
	ProposalID          string `json:"proposal_id"`
	Decision            string `json:"decision"`
	LegacyOverlayEdgeID string `json:"legacy_overlay_edge_id"`
	LegacySCFID         string `json:"legacy_scf_id"`
	LegacyRelease       string `json:"legacy_release"`
	NewOverlayEdgeID    string `json:"new_overlay_edge_id,omitempty"`
	NewAnchorID         string `json:"new_anchor_id,omitempty"`
	NewSCFID            string `json:"new_scf_id,omitempty"`
	NewRelease          string `json:"new_release,omitempty"`
	Reason              string `json:"reason,omitempty"`
	DecidedBy           string `json:"decided_by"`
	DecidedAt           string `json:"decided_at"`
}

// checkDecidable reports whether a proposal in status may take decision:
// open and rejected may still be decided, but only an open one rejected.
func checkDecidable(status, decision string) error {
	if (status != RemapOpen && status != RemapRejected) || (decision == RemapRejected && status != RemapOpen) {
		return fmt.Errorf("%w (status %s)", ErrRemapDecided, status)
	}
	return nil
}

// successorFor is the anchor code an approval lands on: the one chosen, or
// the proposal's single successor when it has one.
func successorFor(class string, successors []string, chosen string) (string, error) {
	if code := strings.TrimSpace(chosen); code != "" {
		return code, nil
	}
	if class == "split" || class == "no_successor" || len(successors) != 1 {
		return "", fmt.Errorf("%w (class %s)", ErrRemapAnchorRequired, class)
	}
	return successors[0], nil
}

const remapProposalSelect = `
SELECT p.id, p.overlay_edge_id, f.slug, fv.version, r.code, o.relationship_type::text,
       p.legacy_scf_id, p.legacy_release, p.target_release, p.class, p.status,
       p.successor_scf_ids, COALESCE(ta.scf_id, ''), p.new_overlay_edge_id, p.created_at
FROM scf_overlay_remap_proposals p
JOIN fw_to_scf_overlay_edges o ON o.tenant_id = p.tenant_id AND o.id = p.overlay_edge_id
JOIN framework_requirements r  ON r.id = o.framework_requirement_id
JOIN framework_versions fv     ON fv.id = r.framework_version_id
JOIN frameworks f              ON f.id = fv.framework_id
LEFT JOIN scf_anchors ta       ON ta.id = p.target_anchor_id`

// ListRemapProposals returns the caller's overlay remap proposals, by
// framework, requirement and legacy code.
func (s *Store) ListRemapProposals(ctx context.Context, f RemapFilter) ([]RemapProposal, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}
	out := []RemapProposal{}
	err := s.inRawTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		rows, err := tx.Query(ctx, remapProposalSelect+`
			WHERE p.tenant_id = $1
			  AND ($2 = '' OR p.class = $2)
			  AND ($3 = '' OR p.status = $3)
			ORDER BY f.slug, fv.version, r.code, p.legacy_scf_id, p.id`, tenantID, f.Class, f.Status)
		if err != nil {
			return fmt.Errorf("mapping overlay: list remap proposals: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var p RemapProposal
			if err := rows.Scan(&p.ID, &p.OverlayEdgeID, &p.Framework, &p.FrameworkVersion, &p.RequirementCode,
				&p.RelationshipType, &p.LegacySCFID, &p.LegacyRelease, &p.TargetRelease, &p.Class, &p.Status,
				&p.Successors, &p.TargetSCFID, &p.NewOverlayEdgeID, &p.CreatedAt); err != nil {
				return fmt.Errorf("mapping overlay: scan remap proposal: %w", err)
			}
			out = append(out, p)
		}
		return rows.Err()
	})
	return out, err
}

// lockedRemap is a proposal locked for a decision.
type lockedRemap struct {
	id, edgeID, legacyAnchorID, targetVersionID uuid.UUID
	legacySCFID, legacyRelease, targetRelease   string
	class, status                               string
	successors                                  []string
}

// DecideRemap applies one decision to one of the caller's proposals.
func (s *Store) DecideRemap(ctx context.Context, in RemapDecideInput) (RemapDecision, error) {
	if err := in.Validate(); err != nil {
		return RemapDecision{}, err
	}
	proposalID, err := uuid.Parse(in.ProposalID)
	if err != nil {
		return RemapDecision{}, ErrRemapNotFound
	}
	var out RemapDecision
	err = s.inRawTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		var p lockedRemap
		err := tx.QueryRow(ctx, `
			SELECT id, overlay_edge_id, legacy_anchor_id, target_framework_version_id,
			       legacy_scf_id, legacy_release, target_release, class, status, successor_scf_ids
			FROM scf_overlay_remap_proposals
			WHERE tenant_id = $1 AND id = $2
			FOR UPDATE`, tenantID, proposalID).Scan(
			&p.id, &p.edgeID, &p.legacyAnchorID, &p.targetVersionID,
			&p.legacySCFID, &p.legacyRelease, &p.targetRelease, &p.class, &p.status, &p.successors)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRemapNotFound
		}
		if err != nil {
			return fmt.Errorf("mapping overlay: lock remap proposal: %w", err)
		}
		if err := checkDecidable(p.status, in.Decision); err != nil {
			return err
		}
		out = RemapDecision{
			ProposalID: p.id.String(), Decision: in.Decision, LegacyOverlayEdgeID: p.edgeID.String(),
			LegacySCFID: p.legacySCFID, LegacyRelease: p.legacyRelease,
			Reason: strings.TrimSpace(in.Reason), DecidedBy: strings.TrimSpace(in.DecidedBy),
		}
		var newEdge, newAnchor *uuid.UUID
		if in.Decision == RemapApproved {
			edge, anchor, code, err := approveRemap(ctx, tx, tenantID, p, in.SCFID, in.Actor)
			if err != nil {
				return err
			}
			newEdge, newAnchor = &edge, &anchor
			out.NewOverlayEdgeID, out.NewAnchorID = edge.String(), anchor.String()
			out.NewSCFID, out.NewRelease = code, p.targetRelease
		}
		if _, err := tx.Exec(ctx, `
			UPDATE scf_overlay_remap_proposals
			SET status = $3, target_anchor_id = $4, new_overlay_edge_id = $5
			WHERE tenant_id = $1 AND id = $2`, tenantID, p.id, in.Decision, newAnchor, newEdge); err != nil {
			return fmt.Errorf("mapping overlay: update remap proposal: %w", err)
		}
		var decidedAt time.Time
		if err := tx.QueryRow(ctx, `
			INSERT INTO scf_overlay_remap_decisions (
			    tenant_id, proposal_id, decision, legacy_overlay_edge_id, legacy_anchor_id,
			    legacy_scf_id, legacy_release, new_overlay_edge_id, new_anchor_id,
			    new_scf_id, new_release, reason, decided_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NULLIF($10, ''), NULLIF($11, ''), NULLIF($12, ''), $13)
			RETURNING decided_at`,
			tenantID, p.id, in.Decision, p.edgeID, p.legacyAnchorID, p.legacySCFID, p.legacyRelease,
			newEdge, newAnchor, out.NewSCFID, out.NewRelease, out.Reason, out.DecidedBy).Scan(&decidedAt); err != nil {
			return fmt.Errorf("mapping overlay: record remap decision: %w", err)
		}
		out.DecidedAt = decidedAt.UTC().Format(time.RFC3339Nano)
		return nil
	})
	if err != nil {
		return RemapDecision{}, err
	}
	return out, nil
}

// approveRemap creates the overlay edge on the chosen anchor and retires the
// legacy one, both with their audit rows. It returns the new edge, the
// anchor and its code.
func approveRemap(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, p lockedRemap, scfID, actor string) (uuid.UUID, uuid.UUID, string, error) {
	code, err := successorFor(p.class, p.successors, scfID)
	if err != nil {
		return uuid.Nil, uuid.Nil, "", err
	}
	q := dbx.New(tx)

	// The proposal describes an active edge on the legacy anchor. An edge
	// never moves anchor, so only a retirement can make it stale.
	legacy, err := q.LockOverlayEdge(ctx, dbx.LockOverlayEdgeParams{TenantID: pgUUID(tenantID), ID: pgUUID(p.edgeID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, uuid.Nil, "", ErrRemapEdgeChanged
	}
	if err != nil {
		return uuid.Nil, uuid.Nil, "", fmt.Errorf("mapping overlay: lock legacy overlay edge: %w", err)
	}
	if legacy.Status != StatusActive || uuid.UUID(legacy.ScfAnchorID.Bytes) != p.legacyAnchorID {
		return uuid.Nil, uuid.Nil, "", ErrRemapEdgeChanged
	}

	var anchor uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM scf_anchors WHERE framework_version_id = $1 AND scf_id = $2`,
		p.targetVersionID, code).Scan(&anchor)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, uuid.Nil, "", fmt.Errorf("%w (%s in %s)", ErrRemapAnchorNotInRelease, code, p.targetRelease)
	}
	if err != nil {
		return uuid.Nil, uuid.Nil, "", fmt.Errorf("mapping overlay: resolve successor anchor: %w", err)
	}
	// The same rule as Create: a pair an active catalog edge already joins
	// gets no overlay edge.
	if err := refuseCatalogDuplicate(ctx, q, legacy.FrameworkRequirementID, pgUUID(anchor)); err != nil {
		return uuid.Nil, uuid.Nil, "", err
	}

	before, err := getView(ctx, q, tenantID, legacy.ID)
	if err != nil {
		return uuid.Nil, uuid.Nil, "", err
	}
	row, err := q.InsertOverlayEdge(ctx, dbx.InsertOverlayEdgeParams{
		TenantID:               pgUUID(tenantID),
		FrameworkRequirementID: legacy.FrameworkRequirementID,
		ScfAnchorID:            pgUUID(anchor),
		RelationshipType:       legacy.RelationshipType,
		Strength:               legacy.Strength,
		Rationale:              legacy.Rationale,
		SourceRef:              legacy.SourceRef,
		CreatedBy:              actor,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return uuid.Nil, uuid.Nil, "", ErrDuplicateOverlay
		}
		return uuid.Nil, uuid.Nil, "", fmt.Errorf("mapping overlay: insert remapped overlay edge: %w", err)
	}
	created, err := getView(ctx, q, tenantID, row.ID)
	if err != nil {
		return uuid.Nil, uuid.Nil, "", err
	}
	if err := writeAudit(ctx, q, tenantID, "create", nil, &created, actor); err != nil {
		return uuid.Nil, uuid.Nil, "", err
	}

	if _, err := q.RetireOverlayEdge(ctx, dbx.RetireOverlayEdgeParams{
		Reason: RemapRetirementReason, Actor: actor, TenantID: pgUUID(tenantID), ID: legacy.ID,
	}); err != nil {
		return uuid.Nil, uuid.Nil, "", fmt.Errorf("mapping overlay: retire remapped overlay edge: %w", err)
	}
	retired, err := getView(ctx, q, tenantID, legacy.ID)
	if err != nil {
		return uuid.Nil, uuid.Nil, "", err
	}
	if err := writeAudit(ctx, q, tenantID, "retire", &before, &retired, actor); err != nil {
		return uuid.Nil, uuid.Nil, "", err
	}
	return uuid.UUID(row.ID.Bytes), anchor, code, nil
}
