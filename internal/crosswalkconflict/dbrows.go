package crosswalkconflict

// dbrows.go — the adapter from the slice-438/483 catalog row shape to this
// package's pure Edge/Requirement types.
//
// No new SQL is introduced: the module consumes the existing
// ListFwToScfEdgesForRequirement row as-is, which already joins scf_anchors for
// the scf_id + family the family-scoped heuristics need. Keeping the conversion
// here (rather than a dbx dependency inside conflict.go) is what lets the
// heuristics stay pure and DB-free.

import (
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// RelationshipTypeFromDB converts the dbx enum form to the domain type. An
// unrecognised value passes through verbatim rather than being coerced to a
// default: silently rewriting an unknown STRM type would make the heuristics
// reason over data that is not in the catalog.
func RelationshipTypeFromDB(t dbx.StrmRelationshipType) RelationshipType {
	return RelationshipType(t)
}

// RequirementFromDB converts a catalog requirement row to the inventory form.
func RequirementFromDB(r dbx.FrameworkRequirement) Requirement {
	return Requirement{ID: uuid.UUID(r.ID.Bytes), Code: r.Code}
}

// RequirementsFromDB converts a requirement listing to the orphan-scan
// inventory. Pass the full per-framework-version listing
// (ListFrameworkRequirementsForVersion) — a requirement with zero edges is only
// visible through this input.
func RequirementsFromDB(rows []dbx.FrameworkRequirement) []Requirement {
	out := make([]Requirement, 0, len(rows))
	for _, r := range rows {
		out = append(out, RequirementFromDB(r))
	}
	return out
}

// EdgesFromFrameworkVersionRows converts the slice-536b-1 review-surface
// listing (ListFwToScfEdgesForFrameworkVersion) to Edges. Unlike the
// per-requirement query, this row joins framework_requirements, so the
// requirement code rides along in the row itself.
//
// mapping_tier and source_attribution are deliberately dropped, same as
// EdgesFromDBRows: a finding is about mapping CONTENT, not trust state or
// provenance.
func EdgesFromFrameworkVersionRows(rows []dbx.ListFwToScfEdgesForFrameworkVersionRow) []Edge {
	out := make([]Edge, 0, len(rows))
	for _, r := range rows {
		out = append(out, Edge{
			ID:               uuid.UUID(r.ID.Bytes),
			RequirementID:    uuid.UUID(r.FrameworkRequirementID.Bytes),
			RequirementCode:  r.RequirementCode,
			AnchorID:         uuid.UUID(r.ScfAnchorID.Bytes),
			AnchorSCFID:      r.ScfID,
			AnchorFamily:     r.Family,
			RelationshipType: RelationshipTypeFromDB(r.RelationshipType),
			Strength:         r.Strength,
		})
	}
	return out
}

// EdgesFromDBRows converts one requirement's edge listing
// (ListFwToScfEdgesForRequirement) to Edges. requirementCode is supplied by the
// caller because that query joins scf_anchors, not framework_requirements, so
// the row carries the requirement id but not its code.
//
// mapping_tier and source_attribution are deliberately dropped: a finding is
// about mapping CONTENT, not about trust state or provenance. Filtering the
// queue by tier is the caller's call (decisions log "Revisit once in use").
func EdgesFromDBRows(requirementCode string, rows []dbx.ListFwToScfEdgesForRequirementRow) []Edge {
	out := make([]Edge, 0, len(rows))
	for _, r := range rows {
		out = append(out, Edge{
			ID:               uuid.UUID(r.ID.Bytes),
			RequirementID:    uuid.UUID(r.FrameworkRequirementID.Bytes),
			RequirementCode:  requirementCode,
			AnchorID:         uuid.UUID(r.ScfAnchorID.Bytes),
			AnchorSCFID:      r.ScfID,
			AnchorFamily:     r.Family,
			RelationshipType: RelationshipTypeFromDB(r.RelationshipType),
			Strength:         r.Strength,
		})
	}
	return out
}
