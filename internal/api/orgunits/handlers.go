// Package orgunits serves slice 053's CRUD for org_units (canvas §6.4 —
// the hierarchy under which risks live and acceptance authority is
// configured).
//
// Routes (appended to the platform root router by
// internal/api/httpserver.go — chi.Mux rejects two Mounts at "/" so this
// follows the slice-019/024/021 pattern of per-route registration):
//
//	POST   /v1/org_units           create
//	GET    /v1/org_units           list
//	GET    /v1/org_units/{id}      read one
//	PATCH  /v1/org_units/{id}      full-row replace (lite)
//	DELETE /v1/org_units/{id}      delete (risks survive via ON DELETE SET NULL)
//
// Tenant is inherited from app.current_tenant via tenancymw.Middleware
// (slice 033). No app-level tenant_id filters here — every store call goes
// through risk.Store.inTx which calls tenancy.ApplyTenant.
package orgunits

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/risk"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Handler bundles the org_units routes over a single risk.Store.
type Handler struct {
	store *risk.Store
}

// New constructs a Handler.
func New(store *risk.Store) *Handler { return &Handler{store: store} }

type wire struct {
	ID                    string          `json:"id"`
	Name                  string          `json:"name"`
	ParentID              *string         `json:"parent_id,omitempty"`
	Level                 string          `json:"level"`
	AcceptanceAuthorities json.RawMessage `json:"acceptance_authorities"`
}

type createReq struct {
	Name                  string          `json:"name"`
	ParentID              *string         `json:"parent_id,omitempty"`
	Level                 string          `json:"level"`
	AcceptanceAuthorities json.RawMessage `json:"acceptance_authorities,omitempty"`
}

// Create handles POST /v1/org_units.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	var req createReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	parentID, err := parseUUIDPtr(req.ParentID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "parent_id must be a UUID")
		return
	}
	in := risk.OrgUnitInput{
		Name:                  req.Name,
		ParentID:              parentID,
		Level:                 dbx.RiskLevel(req.Level),
		AcceptanceAuthorities: req.AcceptanceAuthorities,
	}
	out, err := h.store.CreateOrgUnit(r.Context(), in)
	if err != nil {
		switch {
		case errors.Is(err, risk.ErrInvalidLevel):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, risk.ErrNotFound):
			// parent_id pointed to a missing (or cross-tenant) unit.
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			writeServerErr(w, "create org_unit", err)
		}
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"org_unit": wireFrom(out)})
}

// Get handles GET /v1/org_units/{id}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	out, err := h.store.GetOrgUnit(r.Context(), id)
	if err != nil {
		if errors.Is(err, risk.ErrNotFound) {
			writeError(w, http.StatusNotFound, "org_unit not found")
			return
		}
		writeServerErr(w, "get org_unit", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"org_unit": wireFrom(out)})
}

// List handles GET /v1/org_units.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.store.ListOrgUnits(r.Context())
	if err != nil {
		writeServerErr(w, "list org_units", err)
		return
	}
	out := make([]wire, len(rows))
	for i, u := range rows {
		out[i] = wireFrom(u)
	}
	writeJSON(w, http.StatusOK, map[string]any{"org_units": out, "count": len(out)})
}

// Patch handles PATCH /v1/org_units/{id}.
func (h *Handler) Patch(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	var req createReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	parentID, err := parseUUIDPtr(req.ParentID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "parent_id must be a UUID")
		return
	}
	in := risk.OrgUnitInput{
		Name:                  req.Name,
		ParentID:              parentID,
		Level:                 dbx.RiskLevel(req.Level),
		AcceptanceAuthorities: req.AcceptanceAuthorities,
	}
	out, err := h.store.UpdateOrgUnit(r.Context(), id, in)
	if err != nil {
		switch {
		case errors.Is(err, risk.ErrCycleDetected):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, risk.ErrInvalidLevel):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, risk.ErrNotFound):
			writeError(w, http.StatusNotFound, err.Error())
		default:
			writeServerErr(w, "update org_unit", err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"org_unit": wireFrom(out)})
}

// Delete handles DELETE /v1/org_units/{id}.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	if err := h.store.DeleteOrgUnit(r.Context(), id); err != nil {
		if errors.Is(err, risk.ErrNotFound) {
			writeError(w, http.StatusNotFound, "org_unit not found")
			return
		}
		writeServerErr(w, "delete org_unit", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- helpers -----

func wireFrom(u risk.OrgUnit) wire {
	w := wire{
		ID:                    u.ID.String(),
		Name:                  u.Name,
		Level:                 string(u.Level),
		AcceptanceAuthorities: u.AcceptanceAuthorities,
	}
	if u.ParentID != nil {
		s := u.ParentID.String()
		w.ParentID = &s
	}
	if len(w.AcceptanceAuthorities) == 0 {
		w.AcceptanceAuthorities = json.RawMessage("[]")
	}
	return w
}

func parseUUIDPtr(s *string) (*uuid.UUID, error) {
	if s == nil || *s == "" {
		return nil, nil
	}
	id, err := uuid.Parse(*s)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func writeServerErr(w http.ResponseWriter, op string, err error) {
	writeJSON(w, http.StatusInternalServerError, map[string]string{
		"error": op + ": " + err.Error(),
	})
}
