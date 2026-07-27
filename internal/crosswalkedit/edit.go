// Package crosswalkedit implements the slice-536b crosswalk mapping CONTENT
// edit: a reviewer changes an existing edge's relationship_type / strength /
// rationale, and the change plus its immutable audit row are written in one
// transaction.
//
// # What this package is NOT
//
// It is not an approval path. Slice 483 (internal/crosswalktier) owns the ONLY
// review lifecycle — the draft -> under_review -> verified state machine and
// its tier endpoint. Slice 536a's scope reconciliation
// (docs/audit-log/536a-crosswalk-conflict-detection-decisions.md §1.2) is
// explicit that 536b must call that route rather than grow a second workflow,
// and nothing here promotes a mapping's trust. The one tier move this package
// makes is a DEMOTION (verified -> under_review, D-536b-1) performed through
// crosswalktier's own ValidateTransition and writing crosswalktier's own audit
// row — the same machine, not a parallel one.
//
// It is also not the importer. source_attribution stays untouched: provenance
// is import-time history (ADR 0018 / 483 P0-483-3) and promoting it would
// falsify where a mapping came from.
//
// # Constitutional invariant #7
//
// An edit changes an existing requirement -> SCF anchor edge's semantics. It
// can never change the edge's endpoints: framework_requirement_id and
// scf_anchor_id are absent from the update statement AND absent from the
// atlas_app column grant (migration 20260612110000), so no edit can create the
// requirement -> requirement shape invariant #7 forbids, nor re-point an edge
// at a different requirement. Pinned by TestEditNeverTouchesEdgeEndpoints.
//
// # Repudiation (threat-model R)
//
// There is exactly one write path (Store.EditContent) and it inserts into
// fw_to_scf_edge_content_edits inside the same transaction as the update. The
// audit table is append-only by GRANT. No content edit can bypass the trail.
package crosswalkedit

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// maxRationaleLen bounds both the edge's rationale and the edit note. The
// column is unbounded TEXT; the cap is an input-validation bound (threat-model
// T / D) so a crafted request cannot push an arbitrarily large blob into a
// catalog row and, through it, into every review-queue response that renders
// the edge. 4 KiB is generous for a mapping justification paragraph.
const maxRationaleLen = 4096

// ErrInvalidRelationshipType is returned when the requested relationship type
// is not one of the five STRM types. Maps to HTTP 400.
var ErrInvalidRelationshipType = errors.New("crosswalkedit: invalid relationship type")

// ErrStrengthOutOfRange is returned when strength falls outside [0,1] or is not
// a finite number. Maps to HTTP 400. This mirrors the
// fw_to_scf_edges_strength_range CHECK — validated in Go so the operator gets a
// readable message rather than a constraint-violation 500, and so the bound
// holds even if the CHECK is ever relaxed.
var ErrStrengthOutOfRange = errors.New("crosswalkedit: strength must be within [0,1]")

// ErrRationaleTooLong is returned when the rationale or note exceeds
// maxRationaleLen. Maps to HTTP 400.
var ErrRationaleTooLong = errors.New("crosswalkedit: rationale exceeds the maximum length")

// ErrNoChange is returned when the requested edit would leave all three content
// fields exactly as they are. Maps to HTTP 422: writing an audit row that
// records no change is the same defect crosswalktier rejects for a no-op
// self-transition.
var ErrNoChange = errors.New("crosswalkedit: edit changes nothing")

// ErrEdgeNotFound is returned when the target edge id does not exist. Maps to
// HTTP 404.
var ErrEdgeNotFound = errors.New("crosswalkedit: edge not found")

// ErrEdgeRejected is returned when the target edge sits in the terminal
// `rejected` tier. Maps to HTTP 422. Slice 483 made rejected terminal; editing
// a mapping a reviewer already judged wrong would be an edit whose result can
// never re-enter review, so the edit is refused rather than silently applied to
// a dead row (D-536b-1).
var ErrEdgeRejected = errors.New("crosswalkedit: a rejected mapping cannot be edited")

// RelationshipType mirrors the strm_relationship_type Postgres enum. The values
// match internal/crosswalkconflict's domain type; both derive from NIST IR 8477
// (canvas §3.2).
type RelationshipType string

const (
	RelEqual          RelationshipType = "equal"
	RelSubsetOf       RelationshipType = "subset_of"
	RelSupersetOf     RelationshipType = "superset_of"
	RelIntersectsWith RelationshipType = "intersects_with"
	RelNoRelationship RelationshipType = "no_relationship"
)

// IsValid reports whether t is one of the five canonical STRM types.
func (t RelationshipType) IsValid() bool {
	switch t {
	case RelEqual, RelSubsetOf, RelSupersetOf, RelIntersectsWith, RelNoRelationship:
		return true
	default:
		return false
	}
}

// ParseRelationshipType validates a raw string from a request body. Returns
// ErrInvalidRelationshipType for anything else — never a coerced default, which
// would silently rewrite a mapping into a type the reviewer did not choose.
func ParseRelationshipType(s string) (RelationshipType, error) {
	t := RelationshipType(s)
	if !t.IsValid() {
		return "", fmt.Errorf("%w: %q", ErrInvalidRelationshipType, s)
	}
	return t, nil
}

// DBType converts the domain type to the dbx enum form. Caller must have
// validated first.
func (t RelationshipType) DBType() dbx.StrmRelationshipType {
	return dbx.StrmRelationshipType(t)
}

// RelationshipTypeFromDB converts the dbx enum form back to the domain type.
func RelationshipTypeFromDB(d dbx.StrmRelationshipType) RelationshipType {
	return RelationshipType(d)
}

// Content is the editable half of an edge: exactly the three columns the
// slice-536b grant widened to. The endpoints (requirement, anchor) and the
// provenance (source_attribution) are deliberately absent — they are not
// editable and this type is what the store reasons over.
type Content struct {
	RelationshipType RelationshipType
	Strength         float64
	Rationale        string
}

// Validate checks a proposed content value in pure Go, before any DB work: the
// STRM type is one of the five, the strength is a finite value in [0,1], and
// the rationale is within bounds. Returns the sentinel errors above so the HTTP
// layer can map each to its own status without string matching.
func (c Content) Validate() error {
	if !c.RelationshipType.IsValid() {
		return fmt.Errorf("%w: %q", ErrInvalidRelationshipType, c.RelationshipType)
	}
	if math.IsNaN(c.Strength) || math.IsInf(c.Strength, 0) || c.Strength < 0 || c.Strength > 1 {
		return fmt.Errorf("%w: got %v", ErrStrengthOutOfRange, c.Strength)
	}
	if len(c.Rationale) > maxRationaleLen {
		return fmt.Errorf("%w: rationale is %d bytes, max %d", ErrRationaleTooLong, len(c.Rationale), maxRationaleLen)
	}
	return nil
}

// Equal reports whether two content values are identical. Strength is compared
// exactly rather than with a tolerance: the value round-trips through DOUBLE
// PRECISION unchanged, and a reviewer who deliberately nudges 0.80 to 0.81 is
// making a real edit that must be audited, not rounded away.
func (c Content) Equal(other Content) bool {
	return c.RelationshipType == other.RelationshipType &&
		c.Strength == other.Strength &&
		c.Rationale == other.Rationale
}

// validateNote bounds the free-text edit note by the same cap as the rationale.
func validateNote(note string) error {
	if len(note) > maxRationaleLen {
		return fmt.Errorf("%w: note is %d bytes, max %d", ErrRationaleTooLong, len(note), maxRationaleLen)
	}
	return nil
}

// normalizeText trims surrounding whitespace from free text before it is
// compared or stored, so " " and "" are the same edit and a trailing newline
// from a textarea does not read as a change.
func normalizeText(s string) string { return strings.TrimSpace(s) }
