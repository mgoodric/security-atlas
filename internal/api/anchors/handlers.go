// Package anchors serves the read-only HTTP API for SCF anchors and the
// framework requirements that map to them. Slice 005 backs it with an
// in-memory seed (anchorseed); slice 008 swaps for DB-backed UCF queries.
package anchors

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/mgoodric/security-atlas/internal/api/anchorseed"
)

// Handler exposes the /v1/anchors routes. Auth is enforced by middleware
// installed at the router root by cmd/atlas; this package assumes any
// request reaching it is already authenticated.
type Handler struct {
	store anchorseed.Store
}

// New constructs a Handler backed by the supplied store.
func New(store anchorseed.Store) *Handler {
	return &Handler{store: store}
}

// Routes returns a chi router with the two slice-005 endpoints mounted.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/v1/anchors", h.listAnchors)
	r.Get("/v1/anchors/{id}/requirements", h.requirementsForAnchor)
	return r
}

type listAnchorsResponse struct {
	Anchors []anchorseed.Anchor `json:"anchors"`
}

func (h *Handler) listAnchors(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, listAnchorsResponse{Anchors: h.store.ListAnchors()})
}

type requirementsResponse struct {
	Anchor       anchorseed.Anchor                   `json:"anchor"`
	Requirements []anchorseed.RequirementWithMapping `json:"requirements"`
}

func (h *Handler) requirementsForAnchor(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	anchor, ok := h.store.Anchor(id)
	if !ok {
		writeError(w, http.StatusNotFound, "anchor not found")
		return
	}
	writeJSON(w, http.StatusOK, requirementsResponse{
		Anchor:       anchor,
		Requirements: h.store.RequirementsForAnchor(id),
	})
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
