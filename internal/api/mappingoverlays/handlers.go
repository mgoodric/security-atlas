// Package mappingoverlays serves tenant overlay edges over HTTP (#1797):
//
//	POST /v1/mapping-overlays              create
//	GET  /v1/mapping-overlays              list (?framework, ?requirement, ?anchor, ?status)
//	POST /v1/mapping-overlays/{id}/retire  retire with a reason
//	POST /v1/mapping-overlays/import       bulk import from CSV or YAML (#1799, bulk.go)
//	GET  /v1/mapping-overlays/export       CSV export (#1799, bulk.go)
//
// The tenant comes from the credential (tenancy middleware); RLS binds every
// read and write. Writes need admin or grc_engineer: the OPA middleware is
// the primary gate (resource `mapping-overlays`), and requireOverlayWrite
// (authz.IsTenantApprover: the flags or the live tenant role, #2447) is
// its defense-in-depth twin and the testable enforcement point.
package mappingoverlays

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/api/people"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/mappingoverlay"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Handler serves the overlay edge routes.
type Handler struct {
	store *mappingoverlay.Store
	// peoplePool is the app pool the author names are read on (#2286).
	// Without it (a unit-test handler) every author reads "Unknown".
	peoplePool *pgxpool.Pool
}

// New constructs a Handler.
func New(store *mappingoverlay.Store) *Handler { return &Handler{store: store} }

// WithPeople wires the pool the overlay authors are named from (#2286).
func (h *Handler) WithPeople(pool *pgxpool.Pool) *Handler {
	h.peoplePool = pool
	return h
}

// directory is the caller tenant's people, read under its RLS tenant, so
// an author from another tenant never resolves. With no pool it is nil,
// which names every actor but "system" as "Unknown".
func (h *Handler) directory(r *http.Request) (*people.Directory, error) {
	if h.peoplePool == nil {
		return nil, nil
	}
	return people.Load(r.Context(), h.peoplePool)
}

// writtenDirectory names the authors on a write's response. The write has
// already committed, so a failed read is logged and the names read
// "Unknown" rather than the request failing.
func (h *Handler) writtenDirectory(r *http.Request) *people.Directory {
	dir, err := h.directory(r)
	if err != nil {
		slog.WarnContext(r.Context(), "mapping overlays: name authors", "err", err)
		return nil
	}
	return dir
}

// edgeWire is an overlay edge on the wire.
type edgeWire struct {
	ID                     string     `json:"id"`
	Framework              string     `json:"framework"`
	FrameworkVersion       string     `json:"framework_version"`
	RequirementCode        string     `json:"requirement_code"`
	FrameworkRequirementID string     `json:"framework_requirement_id"`
	SCFRelease             string     `json:"scf_release"`
	AnchorCode             string     `json:"anchor_code"`
	SCFAnchorID            string     `json:"scf_anchor_id"`
	RelationshipType       string     `json:"relationship_type"`
	Strength               float64    `json:"strength"`
	Rationale              string     `json:"rationale"`
	SourceRef              string     `json:"source_ref"`
	Status                 string     `json:"status"`
	RetirementReason       string     `json:"retirement_reason,omitempty"`
	CreatedBy              string     `json:"created_by"`
	CreatedByName          string     `json:"created_by_name"`
	CreatedAt              time.Time  `json:"created_at"`
	RetiredBy              string     `json:"retired_by,omitempty"`
	RetiredByName          string     `json:"retired_by_name,omitempty"`
	RetiredAt              *time.Time `json:"retired_at,omitempty"`
}

// wireFrom renders e, naming its authors from dir (#2286). created_by and
// retired_by stay the recorded ids.
func wireFrom(e mappingoverlay.Edge, dir *people.Directory) edgeWire {
	retiredByName := ""
	if e.RetiredBy != "" {
		retiredByName = dir.Name(e.RetiredBy)
	}
	return edgeWire{
		ID:                     e.ID.String(),
		Framework:              e.Framework,
		FrameworkVersion:       e.FrameworkVersion,
		RequirementCode:        e.RequirementCode,
		FrameworkRequirementID: e.FrameworkRequirementID.String(),
		SCFRelease:             e.SCFRelease,
		AnchorCode:             e.AnchorCode,
		SCFAnchorID:            e.SCFAnchorID.String(),
		RelationshipType:       e.RelationshipType,
		Strength:               e.Strength,
		Rationale:              e.Rationale,
		SourceRef:              e.SourceRef,
		Status:                 e.Status,
		RetirementReason:       e.RetirementReason,
		CreatedBy:              e.CreatedBy,
		CreatedByName:          dir.Name(e.CreatedBy),
		CreatedAt:              e.CreatedAt,
		RetiredBy:              e.RetiredBy,
		RetiredByName:          retiredByName,
		RetiredAt:              e.RetiredAt,
	}
}

type createReq struct {
	Framework        string   `json:"framework"`
	FrameworkVersion string   `json:"framework_version"`
	RequirementCode  string   `json:"requirement_code"`
	SCFRelease       string   `json:"scf_release"`
	AnchorCode       string   `json:"anchor_code"`
	RelationshipType string   `json:"relationship_type"`
	Strength         *float64 `json:"strength"`
	Rationale        string   `json:"rationale"`
	SourceRef        string   `json:"source_ref"`
}

// Create handles POST /v1/mapping-overlays.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	if !requireOverlayWrite(w, r) || !requireTenant(w, r) {
		return
	}
	actor, ok := actorFrom(w, r)
	if !ok {
		return
	}
	var req createReq
	if !decodeStrict(w, r, &req) {
		return
	}
	if req.Strength == nil {
		httpresp.WriteError(w, http.StatusBadRequest, "strength is required")
		return
	}
	edge, err := h.store.Create(r.Context(), mappingoverlay.NewEdge{
		Framework:        req.Framework,
		FrameworkVersion: req.FrameworkVersion,
		RequirementCode:  req.RequirementCode,
		SCFRelease:       req.SCFRelease,
		AnchorCode:       req.AnchorCode,
		RelationshipType: req.RelationshipType,
		Strength:         *req.Strength,
		Rationale:        req.Rationale,
		SourceRef:        req.SourceRef,
	}, actor)
	if err != nil {
		writeErr(w, r, "create overlay edge", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusCreated, map[string]any{"overlay_edge": wireFrom(edge, h.writtenDirectory(r))})
}

// List handles GET /v1/mapping-overlays.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	if !requireTenant(w, r) {
		return
	}
	q := r.URL.Query()
	edges, err := h.store.List(r.Context(), mappingoverlay.Filter{
		Framework:   q.Get("framework"),
		Requirement: q.Get("requirement"),
		Anchor:      q.Get("anchor"),
		Status:      q.Get("status"),
	})
	if err != nil {
		writeErr(w, r, "list overlay edges", err)
		return
	}
	dir, err := h.directory(r)
	if err != nil {
		httperr.WriteInternal(w, r, "name overlay authors", err)
		return
	}
	out := make([]edgeWire, len(edges))
	for i, e := range edges {
		out[i] = wireFrom(e, dir)
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"overlay_edges": out, "count": len(out)})
}

type retireReq struct {
	Reason string `json:"reason"`
}

// Retire handles POST /v1/mapping-overlays/{id}/retire.
func (h *Handler) Retire(w http.ResponseWriter, r *http.Request) {
	if !requireOverlayWrite(w, r) || !requireTenant(w, r) {
		return
	}
	actor, ok := actorFrom(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	var req retireReq
	if !decodeStrict(w, r, &req) {
		return
	}
	edge, err := h.store.Retire(r.Context(), id, req.Reason, actor)
	if err != nil {
		writeErr(w, r, "retire overlay edge", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"overlay_edge": wireFrom(edge, h.writtenDirectory(r))})
}

// writeErr maps the store's errors. Each refusal carries a `code` so a
// client (and the CLI) can tell them apart without parsing the message.
func writeErr(w http.ResponseWriter, r *http.Request, op string, err error) {
	code, _, ok := mappingoverlay.Classify(err)
	if !ok {
		httperr.WriteInternal(w, r, op, err)
		return
	}
	body := map[string]any{"error": err.Error(), "code": code}
	var catalog *mappingoverlay.CatalogEdgeExistsError
	if errors.As(err, &catalog) {
		body["catalog_edge"] = map[string]any{
			"id":                catalog.EdgeID.String(),
			"relationship_type": catalog.RelationshipType,
			"mapping_tier":      catalog.MappingTier,
		}
	}
	httpresp.WriteJSON(w, refusalStatus[code], body)
}

// refusalStatus is the HTTP status of each mappingoverlay.Classify code.
var refusalStatus = map[string]int{
	"invalid_overlay_edge":      http.StatusBadRequest,
	"unknown_framework_version": http.StatusUnprocessableEntity,
	"unknown_requirement":       http.StatusUnprocessableEntity,
	"unknown_scf_release":       http.StatusUnprocessableEntity,
	"unknown_anchor":            http.StatusUnprocessableEntity,
	"catalog_edge_exists":       http.StatusConflict,
	"overlay_edge_exists":       http.StatusConflict,
	"already_retired":           http.StatusConflict,
	"not_found":                 http.StatusNotFound,
	// #1804: remap proposal decisions.
	"invalid_remap_decision":      http.StatusBadRequest,
	"remap_proposal_not_found":    http.StatusNotFound,
	"remap_proposal_decided":      http.StatusConflict,
	"remap_anchor_required":       http.StatusBadRequest,
	"remap_anchor_not_in_release": http.StatusBadRequest,
	"remap_edge_changed":          http.StatusConflict,
}

func requireOverlayWrite(w http.ResponseWriter, r *http.Request) bool {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || !authz.IsTenantApprover(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "role does not grant mapping overlay management")
		return false
	}
	return true
}

func requireTenant(w http.ResponseWriter, r *http.Request) bool {
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return false
	}
	return true
}

// actorFrom names the caller on the audit row. A signed-in person (a JWT
// session) is recorded as their users(id) (#2286): the session's own
// credential id is per token ("jwt:<jti>") and names no one a reader could
// resolve. Every other caller is recorded as its credential id, so an API
// key stays "key_<uuid>" even when a user owns it. With no credential (or
// one without an id) in context it writes a 401 and returns false, so a
// write is never recorded without an actor (#2375).
func actorFrom(w http.ResponseWriter, r *http.Request) (string, bool) {
	if cred, ok := authctx.CredentialFromContext(r.Context()); ok && cred.ID != "" {
		if strings.HasPrefix(cred.ID, "jwt:") {
			if u, ok := credstore.CallerUserID(cred); ok {
				return u.String(), true
			}
		}
		return cred.ID, true
	}
	httpresp.WriteError(w, http.StatusUnauthorized, "credential missing")
	return "", false
}

// decodeStrict decodes a JSON body, refusing unknown fields. On failure it
// writes a 400 and returns false.
func decodeStrict(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}
