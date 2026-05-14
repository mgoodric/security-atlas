// Package risks serves the slice-019 HTTP API for the risk register. Routes
// (registered onto the platform root router by internal/api/httpserver.go):
//
//	POST   /v1/risks           create
//	GET    /v1/risks           list (optional ?treatment=&category=&methodology= filters)
//	GET    /v1/risks/{id}      get one
//	PATCH  /v1/risks/{id}      partial update (TBD; v1 ships POST/GET/DELETE+heatmap)
//	DELETE /v1/risks/{id}      delete
//	GET    /v1/risks/heatmap   5x5 grid for nist_800_30 + qualitative_5x5
//
// All handlers run with the tenant set by upstream auth middleware (see
// internal/api/authctx). The store opens its own transaction per call and
// applies the tenant GUC.
package risks

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/risk"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Handler bundles the slice-019 routes over a single risk.Store. Slice 020
// adds an optional ResidualDeriver — when set, GET /v1/risks/{id} returns the
// derived residual + effectiveness breakdown and POST /v1/risks/{id}/controls
// is served. When nil (a deployment without NATS/eval wired), the risk routes
// still work and residual_score is whatever was last persisted.
type Handler struct {
	store   *risk.Store
	deriver *risk.ResidualDeriver
}

// New constructs a Handler. The ResidualDeriver is attached separately via
// WithDeriver so the slice-019/053 callers that pass only a Store keep
// working unchanged.
func New(store *risk.Store) *Handler { return &Handler{store: store} }

// WithDeriver attaches the slice-020 ResidualDeriver and returns the handler
// for chaining. httpserver.go calls this when the eval engine is available.
func (h *Handler) WithDeriver(d *risk.ResidualDeriver) *Handler {
	h.deriver = d
	return h
}

// ----- wire shapes -----

type createReq struct {
	Title               string          `json:"title"`
	Description         string          `json:"description"`
	Category            string          `json:"category"`
	Methodology         string          `json:"methodology"`
	InherentScore       json.RawMessage `json:"inherent_score"`
	Treatment           string          `json:"treatment"`
	TreatmentOwner      string          `json:"treatment_owner"`
	ResidualScore       json.RawMessage `json:"residual_score"`
	ReviewDueAt         *time.Time      `json:"review_due_at,omitempty"`
	AcceptedUntil       *string         `json:"accepted_until,omitempty"` // YYYY-MM-DD
	Accepter            string          `json:"accepter"`
	InstrumentReference string          `json:"instrument_reference"`
	LinkedControlIDs    []string        `json:"linked_control_ids"`
}

type riskWire struct {
	ID                  string          `json:"id"`
	Title               string          `json:"title"`
	Description         string          `json:"description"`
	Category            string          `json:"category"`
	Methodology         string          `json:"methodology"`
	InherentScore       json.RawMessage `json:"inherent_score"`
	Treatment           string          `json:"treatment"`
	TreatmentOwner      string          `json:"treatment_owner"`
	ResidualScore       json.RawMessage `json:"residual_score"`
	ReviewDueAt         *time.Time      `json:"review_due_at,omitempty"`
	AcceptedUntil       *string         `json:"accepted_until,omitempty"`
	Accepter            string          `json:"accepter"`
	InstrumentReference string          `json:"instrument_reference"`
	LinkedControlIDs    []string        `json:"linked_control_ids"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

// CreateRisk handles POST /v1/risks (AC-1..AC-5).
func (h *Handler) CreateRisk(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	var req createReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	if req.Category == "" {
		writeError(w, http.StatusBadRequest, "category is required")
		return
	}
	methodology := dbx.RiskMethodology(req.Methodology)
	if methodology == "" {
		methodology = risk.DefaultMethodology // AC-1: default nist_800_30
	}
	treatment := dbx.RiskTreatment(req.Treatment)
	if treatment == "" {
		treatment = dbx.RiskTreatmentAvoid // sensible safe-default if caller omits
	}
	linkedIDs, err := parseUUIDs(req.LinkedControlIDs)
	if err != nil {
		writeError(w, http.StatusBadRequest, "linked_control_ids: "+err.Error())
		return
	}
	acceptedUntil, err := parseDatePtr(req.AcceptedUntil)
	if err != nil {
		writeError(w, http.StatusBadRequest, "accepted_until must be YYYY-MM-DD")
		return
	}

	in := risk.CreateInput{
		Title:               req.Title,
		Description:         req.Description,
		Category:            dbx.RiskCategory(req.Category),
		Methodology:         methodology,
		InherentScore:       []byte(req.InherentScore),
		Treatment:           treatment,
		TreatmentOwner:      req.TreatmentOwner,
		ResidualScore:       []byte(req.ResidualScore),
		ReviewDueAt:         req.ReviewDueAt,
		AcceptedUntil:       acceptedUntil,
		Accepter:            req.Accepter,
		InstrumentReference: req.InstrumentReference,
		LinkedControlIDs:    linkedIDs,
	}
	created, err := h.store.Create(ctx, in)
	if err != nil {
		switch {
		case errors.Is(err, risk.ErrInvalidMethodology),
			errors.Is(err, risk.ErrInherentScoreInvalid),
			risk.IsTreatmentValidation(err):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			writeServerErr(w, "create risk", err)
		}
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"risk": riskWireFrom(created)})
}

// ListRisks handles GET /v1/risks (AC-6).
func (h *Handler) ListRisks(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	filter := risk.ListFilter{
		Treatment:   dbx.RiskTreatment(r.URL.Query().Get("treatment")),
		Category:    dbx.RiskCategory(r.URL.Query().Get("category")),
		Methodology: dbx.RiskMethodology(r.URL.Query().Get("methodology")),
	}
	risks, err := h.store.List(ctx, filter)
	if err != nil {
		writeServerErr(w, "list risks", err)
		return
	}
	out := make([]riskWire, len(risks))
	for i, rk := range risks {
		out[i] = riskWireFrom(rk)
	}
	writeJSON(w, http.StatusOK, map[string]any{"risks": out, "count": len(out)})
}

// GetRisk handles GET /v1/risks/{id}.
func (h *Handler) GetRisk(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	rk, err := h.store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, risk.ErrNotFound) {
			writeError(w, http.StatusNotFound, "risk not found")
			return
		}
		writeServerErr(w, "get risk", err)
		return
	}
	body := map[string]any{"risk": riskWireFrom(rk)}
	// Slice 020 (AC-2): when the residual deriver is wired, return the
	// live-derived residual + the per-linked-control effectiveness breakdown.
	// Derive is the pure read path — recompute=false, so a GET never triggers
	// evaluation (the NATS subscriber + scheduler own that).
	if h.deriver != nil {
		res, derr := h.deriver.Derive(ctx, id, false)
		if derr != nil {
			writeServerErr(w, "derive residual", derr)
			return
		}
		body["residual"] = residualWireFrom(res)
	}
	writeJSON(w, http.StatusOK, body)
}

// DeleteRisk handles DELETE /v1/risks/{id}.
func (h *Handler) DeleteRisk(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	if err := h.store.Delete(ctx, id); err != nil {
		if errors.Is(err, risk.ErrNotFound) {
			writeError(w, http.StatusNotFound, "risk not found")
			return
		}
		writeServerErr(w, "delete risk", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Heatmap handles GET /v1/risks/heatmap (AC-7). Returns a sparse list of
// occupied cells plus the full 5x5 matrix (zeroes inclusive) so the frontend
// can render without a second pass.
func (h *Handler) Heatmap(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	cells, err := h.store.Heatmap(ctx)
	if err != nil {
		writeServerErr(w, "heatmap", err)
		return
	}
	// Dense 5x5 view — likelihood rows (1..5), impact cols (1..5).
	grid := [6][6]int{} // index 0 unused; 1..5 is the data range
	for _, c := range cells {
		if c.Likelihood < 1 || c.Likelihood > 5 || c.Impact < 1 || c.Impact > 5 {
			continue
		}
		grid[c.Likelihood][c.Impact] = c.Count
	}
	denseRows := make([][]int, 5)
	for i := 0; i < 5; i++ {
		row := make([]int, 5)
		for j := 0; j < 5; j++ {
			row[j] = grid[i+1][j+1]
		}
		denseRows[i] = row
	}
	type cellOut struct {
		Likelihood int `json:"likelihood"`
		Impact     int `json:"impact"`
		Count      int `json:"count"`
	}
	sparse := make([]cellOut, 0, len(cells))
	for _, c := range cells {
		if c.Likelihood < 1 || c.Likelihood > 5 || c.Impact < 1 || c.Impact > 5 {
			continue
		}
		sparse = append(sparse, cellOut{Likelihood: c.Likelihood, Impact: c.Impact, Count: c.Count})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cells": sparse,
		"grid":  denseRows,
		"shape": "5x5",
	})
}

// ----- helpers -----

func (h *Handler) tenantContext(r *http.Request) (context.Context, bool) {
	// Slice 033: the tenancy.Middleware (mounted in httpserver.go after
	// the bearer-auth middleware) already lifted cred.TenantID onto
	// r.Context() via tenancy.WithTenant. We confirm a tenant is set
	// (which doubles as a "credential is present" check on the
	// bearer-auth'd path: no credential → no tenant → 401-shaped path).
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		return nil, false
	}
	return r.Context(), true
}

func riskWireFrom(r risk.Risk) riskWire {
	w := riskWire{
		ID:                  r.ID.String(),
		Title:               r.Title,
		Description:         r.Description,
		Category:            string(r.Category),
		Methodology:         string(r.Methodology),
		InherentScore:       jsonRaw(r.InherentScore),
		Treatment:           string(r.Treatment),
		TreatmentOwner:      r.TreatmentOwner,
		ResidualScore:       jsonRaw(r.ResidualScore),
		Accepter:            r.Accepter,
		InstrumentReference: r.InstrumentReference,
		CreatedAt:           r.CreatedAt,
		UpdatedAt:           r.UpdatedAt,
	}
	if r.ReviewDueAt != nil {
		t := *r.ReviewDueAt
		w.ReviewDueAt = &t
	}
	if r.AcceptedUntil != nil {
		s := r.AcceptedUntil.Format("2006-01-02")
		w.AcceptedUntil = &s
	}
	w.LinkedControlIDs = make([]string, len(r.LinkedControlIDs))
	for i, id := range r.LinkedControlIDs {
		w.LinkedControlIDs[i] = id.String()
	}
	return w
}

func jsonRaw(b []byte) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage("{}")
	}
	return json.RawMessage(b)
}

func parseUUIDs(in []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(in))
	for _, s := range in {
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

func parseDatePtr(s *string) (*time.Time, error) {
	if s == nil || *s == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", *s)
	if err != nil {
		return nil, err
	}
	return &t, nil
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
