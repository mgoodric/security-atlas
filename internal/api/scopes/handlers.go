// Package scopes serves the slice-017 HTTP API for scope cells and the
// per-control applicability lookup. Routes:
//
//	POST /v1/scopes/cells              create a scope cell
//	GET  /v1/scopes/cells              list the tenant's universe
//	GET  /v1/scopes/dimensions         list declared dimensions
//	GET  /v1/controls/:id/applicability   resolve a control's applicability set
//
// All handlers run with the tenant set by upstream auth middleware (see
// internal/api/authctx). The store opens its own transaction per call and
// applies the tenant GUC; nothing leaks across requests.
package scopes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/scope"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Handler bundles the slice-017 routes. Auth middleware mounts the credential
// into ctx; we read the tenant id from there and inject it into a tenancy
// context so the store's per-request transaction can apply the RLS GUC.
type Handler struct {
	store *scope.Store
}

// New constructs a Handler over a scope.Store.
func New(store *scope.Store) *Handler { return &Handler{store: store} }

// Routes returns a chi router with the slice-017 endpoints. The platform
// HTTP composition wires handlers individually onto a shared root because
// chi.Mux rejects mounting two routers at the same prefix; this method is
// retained for callers (e.g., tests) that want a stand-alone slice router.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/v1/scopes/cells", h.CreateCell)
	r.Get("/v1/scopes/cells", h.ListCells)
	r.Get("/v1/scopes/dimensions", h.ListDimensions)
	r.Get("/v1/controls/{id}/applicability", h.ControlApplicability)
	return r
}

type createCellReq struct {
	Label      string            `json:"label"`
	Dimensions map[string]string `json:"dimensions"`
}

type cellWire struct {
	ID         string            `json:"id"`
	Label      string            `json:"label"`
	Dimensions map[string]string `json:"dimensions"`
}

func (h *Handler) CreateCell(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	var req createCellReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if len(req.Dimensions) == 0 {
		writeError(w, http.StatusBadRequest, "dimensions must be non-empty")
		return
	}
	cell, err := h.store.CreateCell(ctx, req.Label, req.Dimensions)
	if err != nil {
		switch {
		case errors.Is(err, scope.ErrCellExists):
			writeError(w, http.StatusConflict, "cell with these dimensions already exists")
		case errors.Is(err, scope.ErrInvalidDimension):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			writeServerErr(w, r, "create cell", err)
		}
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"cell": cellWireFrom(cell)})
}

func (h *Handler) ListCells(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	cells, err := h.store.ListCells(ctx)
	if err != nil {
		writeServerErr(w, r, "list cells", err)
		return
	}
	out := make([]cellWire, len(cells))
	for i, c := range cells {
		out[i] = cellWireFrom(c)
	}
	writeJSON(w, http.StatusOK, map[string]any{"cells": out})
}

func (h *Handler) ListDimensions(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	dims, err := h.store.ListDimensions(ctx)
	if err != nil {
		writeServerErr(w, r, "list dimensions", err)
		return
	}
	type dimWire struct {
		Name          string          `json:"name"`
		ValueType     string          `json:"value_type"`
		AllowedValues json.RawMessage `json:"allowed_values"`
		IsRequired    bool            `json:"is_required"`
		IsBuiltin     bool            `json:"is_builtin"`
	}
	out := make([]dimWire, len(dims))
	for i, d := range dims {
		out[i] = dimWire{
			Name:          d.Name,
			ValueType:     d.ValueType,
			AllowedValues: json.RawMessage(d.AllowedValues),
			IsRequired:    d.IsRequired,
			IsBuiltin:     d.IsBuiltin,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"dimensions": out})
}

func (h *Handler) ControlApplicability(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	idStr := chi.URLParam(r, "id")
	controlID, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "control id must be a UUID")
		return
	}
	cells, err := h.store.ControlApplicability(ctx, controlID)
	if err != nil {
		writeServerErr(w, r, "control applicability", err)
		return
	}
	out := make([]cellWire, len(cells))
	for i, c := range cells {
		out[i] = cellWireFrom(c)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"control_id":       controlID.String(),
		"applicable":       out,
		"applicable_count": len(out),
	})
}

func (h *Handler) tenantContext(r *http.Request) (context.Context, bool) {
	// Slice 033: tenancy.Middleware (httpserver.go) lifted cred.TenantID
	// onto r.Context() via tenancy.WithTenant. Confirm; bail if absent
	// (would mean no credential or misconfig).
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		return nil, false
	}
	return r.Context(), true
}

func cellWireFrom(c scope.Cell) cellWire {
	return cellWire{
		ID:         c.ID.String(),
		Label:      c.Label,
		Dimensions: c.Dimensions,
	}
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func writeServerErr(w http.ResponseWriter, r *http.Request, op string, err error) {
	httperr.WriteInternal(w, r, op, err)
}
