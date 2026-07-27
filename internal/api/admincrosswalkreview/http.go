package admincrosswalkreview

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
	"github.com/mgoodric/security-atlas/internal/crosswalkconflict"
	"github.com/mgoodric/security-atlas/internal/crosswalkedit"
	"github.com/mgoodric/security-atlas/internal/crosswalktier"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

const (
	maxBody = 16 * 1024

	// Queue pagination (threat-model D: a bounded scan per framework_version,
	// never the whole catalog in one response). The unit is REQUIREMENTS, which
	// is the row the operator works through; each carries its handful of edges.
	defaultLimit int32 = 50
	maxLimit     int32 = 200
)

// Handler owns the slice-536b review-queue + content-edit routes.
type Handler struct {
	q          dbx.Querier
	editStore  *crosswalkedit.Store
	tierStore  *crosswalktier.Store
	defaultLim int32
	maxLim     int32
}

// New constructs a Handler. q is the read side (queue assembly), editStore the
// content-edit write path, and tierStore slice 483's store — used here ONLY to
// read the tier trail for the audit view. This package never calls
// tierStore.Transition: the approve/reject write stays on 483's own route.
func New(q dbx.Querier, editStore *crosswalkedit.Store, tierStore *crosswalktier.Store) *Handler {
	return &Handler{q: q, editStore: editStore, tierStore: tierStore, defaultLim: defaultLimit, maxLim: maxLimit}
}

// Queue handles GET /v1/admin/crosswalk-review?framework_version_id=<uuid>.
//
// It returns one page of a framework version's requirements with their
// requirement -> anchor edges and the slice-536a conflict findings raised
// against them. Detection runs per page, which is exact rather than an
// approximation: every 536a heuristic is scoped within a single requirement
// (536a "Revisit once in use" — cross-requirement shapes are explicitly not
// attempted), so a requirement's findings depend only on its own edges.
//
// Optional filters:
//
//	conflicts_only=true — drop requirements with no findings
//	tier=<draft|under_review|verified|rejected> — keep only requirements with at
//	    least one edge in that tier, so a reviewer can work the draft backlog
//
// Filters are applied AFTER detection so a filtered view never changes what a
// conflict says — narrowing the queue must not silently narrow the heuristics'
// input, which is how a "no conflicts" reading could become a false negative.
func (h *Handler) Queue(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
		return
	}

	versionID, err := uuid.Parse(r.URL.Query().Get("framework_version_id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "framework_version_id must be a uuid")
		return
	}

	var tierFilter crosswalktier.Tier
	if raw := r.URL.Query().Get("tier"); raw != "" {
		t, tErr := crosswalktier.ParseTier(raw)
		if tErr != nil {
			httpresp.WriteError(w, http.StatusBadRequest, "tier must be one of draft, under_review, verified, rejected")
			return
		}
		tierFilter = t
	}
	conflictsOnly := r.URL.Query().Get("conflicts_only") == "true"

	limit := parseBoundedInt32(r.URL.Query().Get("limit"), h.defaultLim, h.maxLim)
	offset := parseOffset(r.URL.Query().Get("offset"))

	total, err := h.q.CountFrameworkRequirementsForVersion(r.Context(), pgUUID(versionID))
	if err != nil {
		httperr.WriteInternal(w, r, "count crosswalk review requirements", err)
		return
	}

	reqRows, err := h.q.ListFrameworkRequirementsForVersionPaged(r.Context(), dbx.ListFrameworkRequirementsForVersionPagedParams{
		FrameworkVersionID: pgUUID(versionID),
		Limit:              limit,
		Offset:             offset,
	})
	if err != nil {
		httperr.WriteInternal(w, r, "list crosswalk review requirements", err)
		return
	}

	ids := make([]pgtype.UUID, 0, len(reqRows))
	for _, rr := range reqRows {
		ids = append(ids, rr.ID)
	}
	var edgeRows []dbx.ListFwToScfEdgesForRequirementIDsRow
	if len(ids) > 0 {
		edgeRows, err = h.q.ListFwToScfEdgesForRequirementIDs(r.Context(), ids)
		if err != nil {
			httperr.WriteInternal(w, r, "list crosswalk review edges", err)
			return
		}
	}

	resp := buildQueue(versionID, reqRows, edgeRows, queueFilter{
		tier:          tierFilter,
		conflictsOnly: conflictsOnly,
	})
	resp.Total = total
	resp.Limit = limit
	resp.Offset = offset
	httpresp.WriteJSON(w, http.StatusOK, resp)
}

// Edit handles PATCH /v1/admin/crosswalk-edges/{id}.
//
// This is the ONLY write in this package, and it is a CONTENT edit — it never
// approves anything. The editor identity is taken from the verified admin JWT,
// never from the body (threat-model T, the rule 483 applies to reviewer_id).
// The store writes the change and its audit row in one transaction, so no edit
// can bypass the trail.
func (h *Handler) Edit(w http.ResponseWriter, r *http.Request) {
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

	relType, err := crosswalkedit.ParseRelationshipType(req.RelationshipType)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest,
			"relationship_type must be one of equal, subset_of, superset_of, intersects_with, no_relationship")
		return
	}

	editor, err := uuid.Parse(jwtmw.SubjectUserID(cred.UserID))
	if err != nil {
		// A verified admin JWT always carries a parseable user subject; if it
		// does not, fail closed rather than write an audit row with a nil
		// editor. Same posture as admincrosswalktier.
		httpresp.WriteError(w, http.StatusForbidden, "admin credential lacks a resolvable user id")
		return
	}

	edit, err := h.editStore.EditContent(r.Context(), crosswalkedit.EditInput{
		EdgeID: edgeID,
		Content: crosswalkedit.Content{
			RelationshipType: relType,
			Strength:         req.Strength,
			Rationale:        req.Rationale,
		},
		Note:     req.Note,
		EditorID: editor,
	})
	switch {
	case errors.Is(err, crosswalkedit.ErrEdgeNotFound):
		httpresp.WriteError(w, http.StatusNotFound, "unknown crosswalk edge id")
		return
	case errors.Is(err, crosswalkedit.ErrInvalidRelationshipType):
		httpresp.WriteError(w, http.StatusBadRequest,
			"relationship_type must be one of equal, subset_of, superset_of, intersects_with, no_relationship")
		return
	case errors.Is(err, crosswalkedit.ErrStrengthOutOfRange):
		httpresp.WriteError(w, http.StatusBadRequest, "strength must be a number within [0,1]")
		return
	case errors.Is(err, crosswalkedit.ErrRationaleTooLong):
		httpresp.WriteError(w, http.StatusBadRequest, "rationale and note are limited to 4096 bytes")
		return
	case errors.Is(err, crosswalkedit.ErrNoChange):
		httpresp.WriteError(w, http.StatusUnprocessableEntity, "the edit changes nothing")
		return
	case errors.Is(err, crosswalkedit.ErrEdgeRejected):
		httpresp.WriteError(w, http.StatusUnprocessableEntity, "a rejected mapping cannot be edited")
		return
	case err != nil:
		httperr.WriteInternal(w, r, "edit crosswalk mapping content", err)
		return
	}

	httpresp.WriteJSON(w, http.StatusOK, EditResponse{
		EdgeID:          edit.EdgeID.String(),
		EditID:          edit.EditID.String(),
		From:            contentWire(edit.From),
		To:              contentWire(edit.To),
		Note:            edit.Note,
		EditorID:        edit.EditorID.String(),
		TierDemotedFrom: string(edit.TierDemotedFrom),
		TierDemotedTo:   string(edit.TierDemotedTo),
		CreatedAt:       formatTime(edit.CreatedAt),
	})
}

// Audit handles GET /v1/admin/crosswalk-edges/{id}/audit — both trails for one
// edge. This is the in-product proof that no edit and no approval went
// unrecorded, which is why the review UI links to it from every row.
//
// Admin-scoped: reviewer/editor identity appears here and NEVER on the public
// /anchors payload (the slice-483 P0-483-6 boundary, held for the content trail
// too).
func (h *Handler) Audit(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
		return
	}

	edgeID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid edge id")
		return
	}

	tier, err := h.tierStore.CurrentTier(r.Context(), edgeID)
	if errors.Is(err, crosswalktier.ErrEdgeNotFound) {
		httpresp.WriteError(w, http.StatusNotFound, "unknown crosswalk edge id")
		return
	}
	if err != nil {
		httperr.WriteInternal(w, r, "read crosswalk edge tier", err)
		return
	}

	edits, err := h.editStore.ListEdits(r.Context(), edgeID)
	if err != nil {
		httperr.WriteInternal(w, r, "list crosswalk content edits", err)
		return
	}
	transitions, err := h.tierStore.ListTransitions(r.Context(), edgeID)
	if err != nil {
		httperr.WriteInternal(w, r, "list crosswalk tier transitions", err)
		return
	}

	resp := AuditResponse{
		EdgeID:           edgeID.String(),
		CurrentTier:      string(tier),
		ContentEdits:     make([]ContentEditWire, 0, len(edits)),
		TierTransitions:  make([]TierTransitionWire, 0, len(transitions)),
		ContentEditCount: len(edits),
	}
	for _, e := range edits {
		resp.ContentEdits = append(resp.ContentEdits, ContentEditWire{
			ID:        e.ID.String(),
			EditorID:  e.EditorID.String(),
			From:      contentWire(e.From),
			To:        contentWire(e.To),
			Note:      e.Note,
			CreatedAt: formatTime(e.CreatedAt),
		})
	}
	for _, t := range transitions {
		resp.TierTransitions = append(resp.TierTransitions, TierTransitionWire{
			ReviewerID: t.ReviewerID.String(),
			FromTier:   string(t.FromTier),
			ToTier:     string(t.ToTier),
			Note:       t.Note,
			CreatedAt:  formatTime(t.CreatedAt),
		})
	}
	httpresp.WriteJSON(w, http.StatusOK, resp)
}

// --- queue assembly (pure — unit-tested without a DB) ---

type queueFilter struct {
	tier          crosswalktier.Tier
	conflictsOnly bool
}

// buildQueue turns a page of requirement rows + their edge rows into the wire
// response, running the slice-536a heuristics over the page.
//
// Kept free of *http.Request and context so the assembly — including the
// filter semantics, which are the part most easily got wrong — is exercisable
// from a table test with no Postgres (the slice-353 Q-2 pure-Go convention).
func buildQueue(
	versionID uuid.UUID,
	reqRows []dbx.FrameworkRequirement,
	edgeRows []dbx.ListFwToScfEdgesForRequirementIDsRow,
	filter queueFilter,
) ReviewQueueResponse {
	edgesByReq := make(map[uuid.UUID][]dbx.ListFwToScfEdgesForRequirementIDsRow, len(reqRows))
	for _, er := range edgeRows {
		rid := uuid.UUID(er.FrameworkRequirementID.Bytes)
		edgesByReq[rid] = append(edgesByReq[rid], er)
	}

	// Detection input: the page's full requirement inventory (so `unmapped` can
	// fire — 536a D4 notes it is invisible to an edges-only view) plus every
	// edge on the page.
	in := crosswalkconflict.Input{
		Requirements: make([]crosswalkconflict.Requirement, 0, len(reqRows)),
		Edges:        make([]crosswalkconflict.Edge, 0, len(edgeRows)),
	}
	for _, rr := range reqRows {
		in.Requirements = append(in.Requirements, crosswalkconflict.Requirement{
			ID:   uuid.UUID(rr.ID.Bytes),
			Code: rr.Code,
		})
	}
	for _, er := range edgeRows {
		in.Edges = append(in.Edges, crosswalkconflict.Edge{
			ID:               uuid.UUID(er.ID.Bytes),
			RequirementID:    uuid.UUID(er.FrameworkRequirementID.Bytes),
			RequirementCode:  er.RequirementCode,
			AnchorID:         uuid.UUID(er.ScfAnchorID.Bytes),
			AnchorSCFID:      er.ScfID,
			AnchorFamily:     er.Family,
			RelationshipType: crosswalkconflict.RelationshipTypeFromDB(er.RelationshipType),
			Strength:         er.Strength,
		})
	}

	conflictsByReq := make(map[uuid.UUID][]ConflictWire)
	for _, c := range crosswalkconflict.Detect(in) {
		ids := make([]string, 0, len(c.EdgeIDs))
		for _, id := range c.EdgeIDs {
			ids = append(ids, id.String())
		}
		anchors := c.AnchorSCFIDs
		if anchors == nil {
			anchors = []string{}
		}
		conflictsByReq[c.RequirementID] = append(conflictsByReq[c.RequirementID], ConflictWire{
			Kind:         string(c.Kind),
			Reason:       string(c.Reason),
			Severity:     string(c.Severity),
			EdgeIDs:      ids,
			AnchorSCFIDs: anchors,
			Detail:       c.Detail,
		})
	}

	out := ReviewQueueResponse{
		FrameworkVersionID: versionID.String(),
		Requirements:       make([]RequirementWire, 0, len(reqRows)),
	}
	for _, rr := range reqRows {
		rid := uuid.UUID(rr.ID.Bytes)
		conflicts := conflictsByReq[rid]
		if conflicts == nil {
			conflicts = []ConflictWire{}
		}

		edges := make([]EdgeWire, 0, len(edgesByReq[rid]))
		for _, er := range edgesByReq[rid] {
			edges = append(edges, EdgeWire{
				ID:                uuid.UUID(er.ID.Bytes).String(),
				AnchorID:          uuid.UUID(er.ScfAnchorID.Bytes).String(),
				AnchorSCFID:       er.ScfID,
				AnchorFamily:      er.Family,
				AnchorTitle:       er.AnchorTitle,
				RelationshipType:  string(er.RelationshipType),
				Strength:          er.Strength,
				SourceAttribution: string(er.SourceAttribution),
				MappingTier:       string(er.MappingTier),
				Rationale:         er.Rationale,
				UpdatedAt:         formatTime(er.UpdatedAt.Time),
			})
		}

		if filter.conflictsOnly && len(conflicts) == 0 {
			continue
		}
		if filter.tier != "" && !hasTier(edges, filter.tier) {
			continue
		}

		out.Requirements = append(out.Requirements, RequirementWire{
			ID:        rid.String(),
			Code:      rr.Code,
			Title:     rr.Title,
			Edges:     edges,
			Conflicts: conflicts,
		})
		out.ConflictCount += len(conflicts)
	}
	return out
}

func hasTier(edges []EdgeWire, tier crosswalktier.Tier) bool {
	for _, e := range edges {
		if e.MappingTier == string(tier) {
			return true
		}
	}
	return false
}

// --- helpers ---

type adminCred struct {
	TenantID string
	UserID   string
}

// requireAdmin enforces the admin gate (threat-model S / E). A missing
// credential is 401; a non-admin credential is 403. Authority is enforced
// server-side, never in the UI. Same shape as admincrosswalktier.requireAdmin —
// deliberately duplicated rather than shared, so neither package's gate can be
// loosened by a change made for the other's benefit.
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

// parseBoundedInt32 reads a limit query param, falling back to def for absent
// or unparseable input and clamping to [1, max]. A malformed limit is treated
// as absent rather than as an error: the queue is a browse surface and a
// mistyped page size should not 400 the operator out of it.
func parseBoundedInt32(raw string, def, max int32) int32 {
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	if n < 1 {
		return 1
	}
	if int32(n) > max {
		return max
	}
	return int32(n)
}

// parseOffset reads an offset query param. Negative and unparseable values
// clamp to 0 — a negative OFFSET is a Postgres error, so it is bounded here
// rather than passed through.
func parseOffset(raw string) int32 {
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0
	}
	const maxOffset = 1 << 30
	if n > maxOffset {
		return maxOffset
	}
	return int32(n)
}

func contentWire(c crosswalkedit.Content) ContentWire {
	return ContentWire{
		RelationshipType: string(c.RelationshipType),
		Strength:         c.Strength,
		Rationale:        c.Rationale,
	}
}

func formatTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05Z07:00")
}

func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}
