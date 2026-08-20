// Package crosswalkedit implements the slice-536b-1 crosswalk-mapping CONTENT
// editing store: a reviewer-curated rewrite of an fw_to_scf_edges row's
// relationship_type / strength / rationale, with an immutable before/after
// audit row appended in the SAME transaction (the content twin of slice 483's
// tier-transition trail).
//
// # Relationship to slice 483 (internal/crosswalktier)
//
// 483 owns the TRUST dimension (mapping_tier + its state machine + its audit
// trail). This package owns the CONTENT dimension and deliberately does NOT
// touch the tier: a content edit never transitions a tier (no auto-approve,
// no auto-demote — every tier change stays a human act through 483's
// endpoint). The two compose through the tier GATE below.
//
// # The tier gate (536a decisions-log D-536b-1)
//
// Content is mutable ONLY while the mapping is at draft or under_review. A
// verified mapping's content is exactly what the reviewer verified — editing
// it in place would silently invalidate the trust act while the verified
// label kept asserting it. A rejected mapping is terminal. So the reviewer
// demotes a verified edge back to under_review through 483's state machine
// (the verified -> under_review edge added by this slice), edits, and
// re-verifies — one lifecycle, fully audited on both dimensions.
//
// # What cannot be edited here, by construction
//
// The edge ENDPOINTS (framework_requirement_id, scf_anchor_id) and
// source_attribution have no input field on ContentPatch and no UPDATE grant
// for atlas_app (migration 20260804000000). An edge stays the same
// requirement -> SCF anchor mapping for its whole life (invariant #7: this
// surface cannot create, re-point, or launder an edge into any other shape),
// and provenance is never rewritten (P0-483-3: that would falsify history).
package crosswalkedit

import (
	"errors"
	"fmt"

	"github.com/mgoodric/security-atlas/internal/crosswalktier"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

var (
	// ErrEdgeNotFound is returned when the target edge id does not exist. The
	// HTTP layer maps it to 404.
	ErrEdgeNotFound = errors.New("crosswalkedit: edge not found")
	// ErrTierNotEditable is returned when the edge's mapping_tier is verified
	// or rejected (the D-536b-1 gate). The HTTP layer maps it to 409: the
	// request is well-formed but conflicts with the edge's current trust
	// state — demote via the tier endpoint first.
	ErrTierNotEditable = errors.New("crosswalkedit: mapping tier does not permit content edits")
	// ErrUnknownRelationshipType is returned for a relationship_type outside
	// the STRM enum. HTTP 400.
	ErrUnknownRelationshipType = errors.New("crosswalkedit: unknown relationship type")
	// ErrStrengthOutOfRange is returned for a strength outside [0, 1] (the
	// fw_to_scf_edges_strength_range CHECK, validated in Go first so the
	// caller gets a 400, not a constraint-violation 500).
	ErrStrengthOutOfRange = errors.New("crosswalkedit: strength must be within [0, 1]")
	// ErrNoFields is returned when the patch carries no editable field at
	// all — a malformed request. HTTP 400.
	ErrNoFields = errors.New("crosswalkedit: no editable field in request")
	// ErrNoChange is returned when the patch resolves to content identical to
	// the current row. Writing it would append an audit row recording a
	// non-event (the same reason 483 rejects no-op self-transitions). HTTP 422.
	ErrNoChange = errors.New("crosswalkedit: edit changes nothing")
)

// validRelationshipTypes is the closed STRM set (NIST IR 8477; the
// strm_relationship_type Postgres enum).
var validRelationshipTypes = map[dbx.StrmRelationshipType]bool{
	dbx.StrmRelationshipTypeEqual:          true,
	dbx.StrmRelationshipTypeSubsetOf:       true,
	dbx.StrmRelationshipTypeSupersetOf:     true,
	dbx.StrmRelationshipTypeIntersectsWith: true,
	dbx.StrmRelationshipTypeNoRelationship: true,
}

// ParseRelationshipType validates a raw STRM relationship-type string and
// returns the typed enum, or ErrUnknownRelationshipType.
func ParseRelationshipType(s string) (dbx.StrmRelationshipType, error) {
	t := dbx.StrmRelationshipType(s)
	if !validRelationshipTypes[t] {
		return "", fmt.Errorf("%w: %q", ErrUnknownRelationshipType, s)
	}
	return t, nil
}

// ValidateStrength enforces the [0, 1] bound the schema CHECKs, so a bad
// value is a 400 at the boundary rather than a constraint violation mid-tx.
func ValidateStrength(f float64) error {
	if f < 0 || f > 1 {
		return fmt.Errorf("%w: %v", ErrStrengthOutOfRange, f)
	}
	return nil
}

// TierEditable reports whether a mapping at tier t accepts content edits
// (D-536b-1: draft and under_review only).
func TierEditable(t crosswalktier.Tier) bool {
	return t == crosswalktier.TierDraft || t == crosswalktier.TierUnderReview
}

// Content is the reviewer-curated STRM content of one edge — the three
// columns the D-536b-2 grant widening makes writable.
type Content struct {
	RelationshipType dbx.StrmRelationshipType
	Strength         float64
	Rationale        string
}

// ContentPatch is a partial edit: nil means "keep the current value". There
// is deliberately no field for the edge endpoints, source_attribution, or
// mapping_tier (see the package doc).
type ContentPatch struct {
	RelationshipType *string
	Strength         *float64
	Rationale        *string
}

// Resolve applies patch to current and returns the target content. It is the
// pure pre-transaction half of an edit: field validation and the
// no-field/no-change guards live here so every branch is unit-testable
// without Postgres (slice-353 Q-2 convention). Returns ErrNoFields,
// ErrUnknownRelationshipType, ErrStrengthOutOfRange, or ErrNoChange.
func Resolve(current Content, patch ContentPatch) (Content, error) {
	if patch.RelationshipType == nil && patch.Strength == nil && patch.Rationale == nil {
		return Content{}, ErrNoFields
	}
	target := current
	if patch.RelationshipType != nil {
		t, err := ParseRelationshipType(*patch.RelationshipType)
		if err != nil {
			return Content{}, err
		}
		target.RelationshipType = t
	}
	if patch.Strength != nil {
		if err := ValidateStrength(*patch.Strength); err != nil {
			return Content{}, err
		}
		target.Strength = *patch.Strength
	}
	if patch.Rationale != nil {
		target.Rationale = *patch.Rationale
	}
	if target == current {
		return Content{}, ErrNoChange
	}
	return target, nil
}
