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
// the primary gate (resource `mapping-overlays`), and requireOverlayWrite is
// its defense-in-depth twin and the testable enforcement point.
package mappingoverlays

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/mappingoverlay"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Handler serves the overlay edge routes.
type Handler struct{ store *mappingoverlay.Store }

// New constructs a Handler.
func New(store *mappingoverlay.Store) *Handler { return &Handler{store: store} }

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
	CreatedAt              time.Time  `json:"created_at"`
	RetiredBy              string     `json:"retired_by,omitempty"`
	RetiredAt              *time.Time `json:"retired_at,omitempty"`
}

func wireFrom(e mappingoverlay.Edge) edgeWire {
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
		CreatedAt:              e.CreatedAt,
		RetiredBy:              e.RetiredBy,
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
	}, actorFrom(r))
	if err != nil {
		writeErr(w, r, "create overlay edge", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusCreated, map[string]any{"overlay_edge": wireFrom(edge)})
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
	out := make([]edgeWire, len(edges))
	for i, e := range edges {
		out[i] = wireFrom(e)
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
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	var req retireReq
	if !decodeStrict(w, r, &req) {
		return
	}
	edge, err := h.store.Retire(r.Context(), id, req.Reason, actorFrom(r))
	if err != nil {
		writeErr(w, r, "retire overlay edge", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"overlay_edge": wireFrom(edge)})
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
}

// hasOverlayWrite reports whether the credential may change the tenant's
// overlay edges: admin or grc_engineer, by flag or by JWT tenant role.
// control_owner, auditor, viewer and a bare credential are refused.
func hasOverlayWrite(c credstore.Credential) bool {
	if c.IsAdmin || c.IsApprover {
		return true
	}
	for _, role := range c.OwnerRoles {
		if role == "admin" || role == "grc_engineer" {
			return true
		}
	}
	return false
}

func requireOverlayWrite(w http.ResponseWriter, r *http.Request) bool {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || !hasOverlayWrite(cred) {
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

func actorFrom(r *http.Request) string {
	if cred, ok := authctx.CredentialFromContext(r.Context()); ok && cred.ID != "" {
		return cred.ID
	}
	return "system"
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
