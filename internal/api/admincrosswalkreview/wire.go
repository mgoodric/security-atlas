// Package admincrosswalkreview is the admin HTTP surface for the slice-536b
// crosswalk review/edit workflow:
//
//	GET   /v1/admin/crosswalk-review          — the review queue (edges + conflicts)
//	PATCH /v1/admin/crosswalk-edges/{id}      — edit a mapping's STRM content
//	GET   /v1/admin/crosswalk-edges/{id}/audit — the edge's full audit trail
//
// The approve/reject action is deliberately NOT here. It is slice 483's
// POST /v1/admin/crosswalk-edges/{id}/tier (internal/api/admincrosswalktier),
// and the 536b UI calls that route directly. Slice 536a's scope reconciliation
// (§1.2) established that 483 supersedes slice 536's original approval design,
// so growing a second approval path here would be the anti-criterion the slice
// explicitly forbids.
//
// Every route requires an ADMIN atlas credential (cred.IsAdmin), the same gate
// admincrosswalktier uses — reading the review queue exposes the conflict
// heuristics and the mapping rationale, and editing is a privileged catalog
// write. fw_to_scf_edges is a CATALOG table (no tenant_id, no RLS): the gate is
// this admin-role authz check plus the append-only audit trails, NOT the
// tenant-RLS pattern.
package admincrosswalkreview

// --- review queue wire types ---

// EdgeWire is one requirement -> SCF anchor mapping as the review queue renders
// it. source_attribution and mapping_tier are BOTH present because they are
// orthogonal (ADR 0018 / 483 P0-483-3): provenance says where the mapping came
// from, the tier says how trusted it is now. The UI shows both and lets the
// reviewer change neither directly — the tier moves through 483's endpoint, the
// provenance never moves at all.
type EdgeWire struct {
	ID                string  `json:"id"`
	AnchorID          string  `json:"anchor_id"`
	AnchorSCFID       string  `json:"anchor_scf_id"`
	AnchorFamily      string  `json:"anchor_family"`
	AnchorTitle       string  `json:"anchor_title"`
	RelationshipType  string  `json:"relationship_type"`
	Strength          float64 `json:"strength"`
	SourceAttribution string  `json:"source_attribution"`
	MappingTier       string  `json:"mapping_tier"`
	Rationale         string  `json:"rationale"`
	UpdatedAt         string  `json:"updated_at"`
}

// ConflictWire is one slice-536a finding, flattened for the queue. It names
// exactly one requirement and zero or more of that requirement's edges — never
// a second requirement (invariant #7, held by crosswalkconflict's types).
type ConflictWire struct {
	Kind         string   `json:"kind"`
	Reason       string   `json:"reason"`
	Severity     string   `json:"severity"`
	EdgeIDs      []string `json:"edge_ids"`
	AnchorSCFIDs []string `json:"anchor_scf_ids"`
	Detail       string   `json:"detail"`
}

// RequirementWire groups a requirement with its mappings and the findings the
// 536a heuristics raised against it. Conflicts hang off the requirement rather
// than off individual edges because several heuristics (competing anchors,
// sibling divergence, orphaned) are statements about a SET of edges, and one
// about the absence of any.
type RequirementWire struct {
	ID        string         `json:"id"`
	Code      string         `json:"code"`
	Title     string         `json:"title"`
	Edges     []EdgeWire     `json:"edges"`
	Conflicts []ConflictWire `json:"conflicts"`
}

// ReviewQueueResponse is the GET /v1/admin/crosswalk-review body.
type ReviewQueueResponse struct {
	FrameworkVersionID string            `json:"framework_version_id"`
	Requirements       []RequirementWire `json:"requirements"`
	// ConflictCount is the number of findings on THIS page, not across the
	// whole framework version — the queue is paginated (threat-model D) and
	// claiming a catalog-wide total would require the unbounded scan the
	// pagination exists to avoid.
	ConflictCount int   `json:"conflict_count"`
	Total         int64 `json:"total"`
	Limit         int32 `json:"limit"`
	Offset        int32 `json:"offset"`
}

// --- content-edit wire types ---

// EditRequest is the PATCH body. All three content fields are required: a
// partial edit would make "what did the reviewer intend for the field they
// omitted?" ambiguous, and the audit row records a full before/after of all
// three. The edge's endpoints and its source_attribution are absent by design —
// they are not editable (invariant #7 / ADR 0018).
type EditRequest struct {
	RelationshipType string  `json:"relationship_type"`
	Strength         float64 `json:"strength"`
	Rationale        string  `json:"rationale"`
	// Note is the reviewer's rationale for the CHANGE, distinct from the
	// mapping's own Rationale. Optional.
	Note string `json:"note,omitempty"`
}

// ContentWire is one side of an edit's before/after.
type ContentWire struct {
	RelationshipType string  `json:"relationship_type"`
	Strength         float64 `json:"strength"`
	Rationale        string  `json:"rationale"`
}

// EditResponse is the PATCH success body. It reports the audit row's id, so a
// caller can prove the edit was recorded, and the demotion when the edit sent a
// verified mapping back to review (D-536b-1).
type EditResponse struct {
	EdgeID   string      `json:"edge_id"`
	EditID   string      `json:"edit_id"`
	From     ContentWire `json:"from"`
	To       ContentWire `json:"to"`
	Note     string      `json:"note,omitempty"`
	EditorID string      `json:"editor_id"`
	// TierDemotedFrom / TierDemotedTo are present only when the edit demoted a
	// verified mapping. The UI surfaces this so the reviewer is never surprised
	// that their edit cost the mapping its verified badge.
	TierDemotedFrom string `json:"tier_demoted_from,omitempty"`
	TierDemotedTo   string `json:"tier_demoted_to,omitempty"`
	CreatedAt       string `json:"created_at"`
}

// --- audit-trail wire types ---

// ContentEditWire is one row of the content-edit trail.
type ContentEditWire struct {
	ID        string      `json:"id"`
	EditorID  string      `json:"editor_id"`
	From      ContentWire `json:"from"`
	To        ContentWire `json:"to"`
	Note      string      `json:"note,omitempty"`
	CreatedAt string      `json:"created_at"`
}

// TierTransitionWire is one row of slice 483's tier trail, echoed here so the
// UI can render one chronological audit view per edge rather than making the
// operator stitch two endpoints together.
type TierTransitionWire struct {
	ReviewerID string `json:"reviewer_id"`
	FromTier   string `json:"from_tier"`
	ToTier     string `json:"to_tier"`
	Note       string `json:"note,omitempty"`
	CreatedAt  string `json:"created_at"`
}

// AuditResponse is the GET /v1/admin/crosswalk-edges/{id}/audit body — both
// trails for one edge, each newest-first.
type AuditResponse struct {
	EdgeID           string               `json:"edge_id"`
	ContentEdits     []ContentEditWire    `json:"content_edits"`
	TierTransitions  []TierTransitionWire `json:"tier_transitions"`
	CurrentTier      string               `json:"current_tier"`
	ContentEditCount int                  `json:"content_edit_count"`
}
