// Package admincrosswalkreview is the admin HTTP surface for the slice-536b-1
// crosswalk-review workflow — the backend/BFF contract the 536b-2 review UI
// wires to:
//
//	GET   /v1/admin/crosswalk-review            — one framework version's STRM
//	      edges (content + provenance + trust tier) PLUS the slice-536a
//	      conflict findings computed over the FULL edge set.
//	PATCH /v1/admin/crosswalk-edges/{id}        — edit a mapping's
//	      relationship_type / strength / rationale, with an immutable
//	      before/after audit row in the same transaction
//	      (internal/crosswalkedit).
//
// Approve/reject is NOT here, by design: slice 483's tier state machine
// (POST /v1/admin/crosswalk-edges/{id}/tier, internal/api/admincrosswalktier)
// is the only review lifecycle, and this package adds no second approval
// workflow alongside it (536a decisions-log §1.2). Conflict detection is
// strictly advisory (536a D5): findings order the reviewer's queue; nothing
// here transitions a tier or acts on a mapping by itself.
//
// Both routes require an ADMIN atlas credential (cred.IsAdmin) — the same
// catalog-write boundary as 483; a non-admin caller gets 403. fw_to_scf_edges
// is a CATALOG table (no tenant_id, no RLS): the gate is this admin-role
// authz check plus the append-only audit trails, NOT the tenant-RLS pattern.
// Conflict detection reads catalog edges only — no tenant state can enter the
// computation (slice 536 threat-model I, enforced by crosswalkconflict.Input
// by construction).
package admincrosswalkreview

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
	"github.com/mgoodric/security-atlas/internal/crosswalkconflict"
	"github.com/mgoodric/security-atlas/internal/crosswalkedit"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

const maxBody = 16 * 1024

// Edge-list pagination bounds (slice 536 threat-model D: bounded reads per
// framework version). Conflicts are ALWAYS computed over the full —
// framework-version-bounded — edge set regardless of the window, because the
// heuristics need every sibling edge of a requirement to be sound.
const (
	defaultLimit = 500
	maxLimit     = 2000
)

// Handler owns the admin crosswalk-review routes.
type Handler struct {
	pool  *pgxpool.Pool
	edits *crosswalkedit.Store
}

// New constructs a Handler over the app pool and the content-edit store.
func New(pool *pgxpool.Pool, edits *crosswalkedit.Store) *Handler {
	return &Handler{pool: pool, edits: edits}
}

// ReviewEdge is one STRM mapping in the review queue. The shape is the
// 536b-2 UI contract: content (type/strength/rationale), provenance
// (source_attribution), and trust (mapping_tier) — but NO reviewer/editor
// identity (P0-483-6 stays intact on list payloads; history endpoints are a
// follow-on if the UI needs them).
type ReviewEdge struct {
	EdgeID            string  `json:"edge_id"`
	RequirementID     string  `json:"requirement_id"`
	RequirementCode   string  `json:"requirement_code"`
	RequirementTitle  string  `json:"requirement_title"`
	AnchorID          string  `json:"anchor_id"`
	AnchorSCFID       string  `json:"anchor_scf_id"`
	AnchorFamily      string  `json:"anchor_family"`
	AnchorTitle       string  `json:"anchor_title"`
	RelationshipType  string  `json:"relationship_type"`
	Strength          float64 `json:"strength"`
	Rationale         string  `json:"rationale"`
	SourceAttribution string  `json:"source_attribution"`
	MappingTier       string  `json:"mapping_tier"`
}

// ReviewConflict is one slice-536a finding, JSON form. Advisory only —
// severity orders the queue, nothing more (536a D5).
type ReviewConflict struct {
	Kind            string   `json:"kind"`
	Reason          string   `json:"reason"`
	Severity        string   `json:"severity"`
	RequirementID   string   `json:"requirement_id"`
	RequirementCode string   `json:"requirement_code"`
	EdgeIDs         []string `json:"edge_ids"`
	AnchorSCFIDs    []string `json:"anchor_scf_ids"`
	Detail          string   `json:"detail"`
}

// ReviewResponse is the GET /v1/admin/crosswalk-review success shape.
// Edges are windowed by limit/offset; Conflicts always cover the full
// framework-version edge set (TotalEdges rows).
type ReviewResponse struct {
	FrameworkVersionID string           `json:"framework_version_id"`
	TotalEdges         int              `json:"total_edges"`
	Limit              int              `json:"limit"`
	Offset             int              `json:"offset"`
	Edges              []ReviewEdge     `json:"edges"`
	Conflicts          []ReviewConflict `json:"conflicts"`
}

// Review handles GET /v1/admin/crosswalk-review?framework_version_id=<uuid>
// [&limit=<n>&offset=<n>].
func (h *Handler) Review(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
		return
	}

	fvID, err := uuid.Parse(r.URL.Query().Get("framework_version_id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "framework_version_id must be a UUID")
		return
	}
	limit, lErr := queryInt(r, "limit", defaultLimit, 1, maxLimit)
	offset, oErr := queryInt(r, "offset", 0, 0, 1<<30)
	if lErr != nil || oErr != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "limit/offset must be non-negative integers (limit 1..2000)")
		return
	}

	q := dbx.New(h.pool)
	rows, err := q.ListFwToScfEdgesForFrameworkVersion(r.Context(), pgUUID(fvID))
	if err != nil {
		httperr.WriteInternal(w, r, "list crosswalk edges", err)
		return
	}
	reqRows, err := q.ListFrameworkRequirementsForVersion(r.Context(), pgUUID(fvID))
	if err != nil {
		httperr.WriteInternal(w, r, "list framework requirements", err)
		return
	}

	// Conflicts over the FULL edge set + the full requirement inventory (a
	// requirement with zero edges is only visible through the inventory —
	// 536a D4). Pure catalog input; no tenant state (threat-model I).
	conflicts := crosswalkconflict.Detect(crosswalkconflict.Input{
		Requirements: crosswalkconflict.RequirementsFromDB(reqRows),
		Edges:        crosswalkconflict.EdgesFromFrameworkVersionRows(rows),
	})

	total := len(rows)
	window := rows
	if offset >= len(window) {
		window = nil
	} else {
		window = window[offset:]
	}
	if len(window) > limit {
		window = window[:limit]
	}

	edges := make([]ReviewEdge, 0, len(window))
	for _, e := range window {
		edges = append(edges, ReviewEdge{
			EdgeID:            uuid.UUID(e.ID.Bytes).String(),
			RequirementID:     uuid.UUID(e.FrameworkRequirementID.Bytes).String(),
			RequirementCode:   e.RequirementCode,
			RequirementTitle:  e.RequirementTitle,
			AnchorID:          uuid.UUID(e.ScfAnchorID.Bytes).String(),
			AnchorSCFID:       e.ScfID,
			AnchorFamily:      e.Family,
			AnchorTitle:       e.AnchorTitle,
			RelationshipType:  string(e.RelationshipType),
			Strength:          e.Strength,
			Rationale:         e.Rationale,
			SourceAttribution: string(e.SourceAttribution),
			MappingTier:       string(e.MappingTier),
		})
	}

	out := make([]ReviewConflict, 0, len(conflicts))
	for _, c := range conflicts {
		ids := make([]string, 0, len(c.EdgeIDs))
		for _, id := range c.EdgeIDs {
			ids = append(ids, id.String())
		}
		anchorIDs := c.AnchorSCFIDs
		if anchorIDs == nil {
			anchorIDs = []string{}
		}
		out = append(out, ReviewConflict{
			Kind:            string(c.Kind),
			Reason:          string(c.Reason),
			Severity:        string(c.Severity),
			RequirementID:   c.RequirementID.String(),
			RequirementCode: c.RequirementCode,
			EdgeIDs:         ids,
			AnchorSCFIDs:    anchorIDs,
			Detail:          c.Detail,
		})
	}

	httpresp.WriteJSON(w, http.StatusOK, ReviewResponse{
		FrameworkVersionID: fvID.String(),
		TotalEdges:         total,
		Limit:              limit,
		Offset:             offset,
		Edges:              edges,
		Conflicts:          out,
	})
}

// EditRequest is the PATCH body. Omitted (null) fields keep their current
// value; note is the editor's free-text rationale for the edit (optional).
// There is deliberately no tier field (transitions go through the slice-483
// endpoint), no source_attribution field (provenance is never rewritten), and
// no requirement/anchor field (edge endpoints are immutable — invariant #7).
type EditRequest struct {
	RelationshipType *string  `json:"relationship_type,omitempty"`
	Strength         *float64 `json:"strength,omitempty"`
	Rationale        *string  `json:"rationale,omitempty"`
	Note             string   `json:"note,omitempty"`
}

// EditContentBlock is the before/after content in the edit response.
type EditContentBlock struct {
	RelationshipType string  `json:"relationship_type"`
	Strength         float64 `json:"strength"`
	Rationale        string  `json:"rationale"`
}

// EditResponse is the success shape. Editor identity IS returned here because
// this is the admin surface (not a public catalog payload); the caller is the
// admin who just performed the edit. MappingTier is the edge's (unchanged)
// trust tier — an edit never transitions it.
type EditResponse struct {
	EdgeID      string           `json:"edge_id"`
	From        EditContentBlock `json:"from"`
	To          EditContentBlock `json:"to"`
	EditorID    string           `json:"editor_id"`
	Note        string           `json:"note,omitempty"`
	MappingTier string           `json:"mapping_tier"`
	CreatedAt   string           `json:"created_at"`
}

// EditContent handles PATCH /v1/admin/crosswalk-edges/{id}.
func (h *Handler) EditContent(w http.ResponseWriter, r *http.Request) {
	cred, ok := requireAdmin(w, r)
	if !ok {
		return
	}

	edgeID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid edge id")
		return
	}

	var req EditRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	editor, err := uuid.Parse(jwtmw.SubjectUserID(cred.UserID))
	if err != nil {
		// A verified admin JWT always carries a parseable user subject; if it
		// does not, fail closed rather than write an audit row with a nil
		// editor.
		httpresp.WriteError(w, http.StatusForbidden, "admin credential lacks a resolvable user id")
		return
	}

	e, err := h.edits.Edit(r.Context(), crosswalkedit.EditInput{
		EdgeID:   edgeID,
		EditorID: editor,
		Patch: crosswalkedit.ContentPatch{
			RelationshipType: req.RelationshipType,
			Strength:         req.Strength,
			Rationale:        req.Rationale,
		},
		Note: req.Note,
	})
	switch {
	case errors.Is(err, crosswalkedit.ErrEdgeNotFound):
		httpresp.WriteError(w, http.StatusNotFound, "unknown crosswalk edge id")
		return
	case errors.Is(err, crosswalkedit.ErrTierNotEditable):
		httpresp.WriteError(w, http.StatusConflict,
			"mapping tier does not permit content edits; demote to under_review via the tier endpoint first")
		return
	case errors.Is(err, crosswalkedit.ErrNoFields):
		httpresp.WriteError(w, http.StatusBadRequest,
			"provide at least one of relationship_type, strength, rationale")
		return
	case errors.Is(err, crosswalkedit.ErrUnknownRelationshipType):
		httpresp.WriteError(w, http.StatusBadRequest,
			"relationship_type must be one of equal, subset_of, superset_of, intersects_with, no_relationship")
		return
	case errors.Is(err, crosswalkedit.ErrStrengthOutOfRange):
		httpresp.WriteError(w, http.StatusBadRequest, "strength must be within [0, 1]")
		return
	case errors.Is(err, crosswalkedit.ErrNoChange):
		httpresp.WriteError(w, http.StatusUnprocessableEntity, "edit changes nothing")
		return
	case err != nil:
		httperr.WriteInternal(w, r, "edit crosswalk edge content", err)
		return
	}

	httpresp.WriteJSON(w, http.StatusOK, EditResponse{
		EdgeID: e.EdgeID.String(),
		From: EditContentBlock{
			RelationshipType: string(e.From.RelationshipType),
			Strength:         e.From.Strength,
			Rationale:        e.From.Rationale,
		},
		To: EditContentBlock{
			RelationshipType: string(e.To.RelationshipType),
			Strength:         e.To.Strength,
			Rationale:        e.To.Rationale,
		},
		EditorID:    e.EditorID.String(),
		Note:        e.Note,
		MappingTier: string(e.MappingTier),
		CreatedAt:   e.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	})
}

// --- helpers ---

type adminCred struct {
	TenantID string
	UserID   string
}

// requireAdmin enforces the admin gate (threat-model S/E, inherited from
// slice 483 — see 536a decisions-log §1.2). A missing credential is 401; a
// non-admin credential is 403. Authority is enforced server-side, never in
// the UI.
func requireAdmin(w http.ResponseWriter, r *http.Request) (adminCred, bool) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "missing credential")
		return adminCred{}, false
	}
	if !cred.IsAdmin {
		httpresp.WriteError(w, http.StatusForbidden, "admin credential required")
		return adminCred{}, false
	}
	return adminCred{TenantID: cred.TenantID, UserID: cred.UserID}, true
}

// queryInt parses an optional integer query parameter, clamping nothing:
// out-of-bounds values are an error so the caller sees a 400 rather than a
// silently adjusted window.
func queryInt(r *http.Request, name string, def, min, max int) (int, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, err
	}
	if n < min || n > max {
		return 0, errors.New("out of range")
	}
	return n, nil
}

func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}
