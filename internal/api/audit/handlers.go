// Package audit serves the slice-026 HTTP API for sample-pull primitives.
// Routes (registered onto the platform root router by
// internal/api/httpserver.go):
//
//	POST /v1/populations                  create a population
//	GET  /v1/populations/{id}             get one
//	POST /v1/samples                      draw a sample
//	GET  /v1/samples/{id}                 get one (with evidence list)
//	POST /v1/samples/{id}/annotations     annotate one record in a sample
//	GET  /v1/samples/{id}/annotations     list annotations on a sample
//
// All handlers run with the tenant set by upstream auth middleware (see
// internal/api/authctx). The store opens its own transaction per call and
// applies the tenant GUC; RLS enforces tenant isolation at the row layer.
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/audit"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Handler bundles the slice-026 routes over a single audit.Store.
type Handler struct {
	store *audit.Store
}

// New constructs a Handler.
func New(store *audit.Store) *Handler { return &Handler{store: store} }

// ----- wire shapes -----

type createPopulationReq struct {
	ControlID       string          `json:"control_id"`
	ScopePredicate  json.RawMessage `json:"scope_predicate"`
	TimeWindowStart time.Time       `json:"time_window_start"`
	TimeWindowEnd   time.Time       `json:"time_window_end"`
}

type populationWire struct {
	ID              string          `json:"id"`
	ControlID       string          `json:"control_id"`
	ScopePredicate  json.RawMessage `json:"scope_predicate"`
	TimeWindowStart time.Time       `json:"time_window_start"`
	TimeWindowEnd   time.Time       `json:"time_window_end"`
	FrozenAt        *time.Time      `json:"frozen_at,omitempty"`
	RowCount        int64           `json:"row_count"`
	CreatedBy       string          `json:"created_by"`
	CreatedAt       time.Time       `json:"created_at"`
}

type drawSampleReq struct {
	PopulationID string `json:"population_id"`
	N            int    `json:"n"`
	Seed         string `json:"seed"`
}

type sampleWire struct {
	ID                string    `json:"id"`
	PopulationID      string    `json:"population_id"`
	N                 int       `json:"n"`
	Seed              string    `json:"seed"`
	CreatedBy         string    `json:"created_by"`
	CreatedAt         time.Time `json:"created_at"`
	EvidenceRecordIDs []string  `json:"evidence_record_ids"`
}

type annotateReq struct {
	EvidenceRecordID string `json:"evidence_record_id"`
	Result           string `json:"result"`
	Notes            string `json:"notes"`
}

type annotationWire struct {
	ID               string    `json:"id"`
	SampleID         string    `json:"sample_id"`
	EvidenceRecordID string    `json:"evidence_record_id"`
	Result           string    `json:"result"`
	AnnotatedBy      string    `json:"annotated_by"`
	AnnotatedAt      time.Time `json:"annotated_at"`
	Notes            string    `json:"notes"`
}

// ----- handlers -----

// CreatePopulation handles POST /v1/populations (AC-1).
func (h *Handler) CreatePopulation(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.tenantContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}

	var req createPopulationReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	controlID, err := uuid.Parse(req.ControlID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "control_id must be a UUID")
		return
	}
	if req.TimeWindowStart.IsZero() || req.TimeWindowEnd.IsZero() {
		writeError(w, http.StatusBadRequest, "time_window_start and time_window_end are required")
		return
	}
	if req.TimeWindowStart.After(req.TimeWindowEnd) {
		writeError(w, http.StatusBadRequest, "time_window_start must be <= time_window_end")
		return
	}

	pop, err := h.store.CreatePopulation(ctx, audit.CreatePopulationInput{
		ControlID:       controlID,
		ScopePredicate:  req.ScopePredicate,
		TimeWindowStart: req.TimeWindowStart,
		TimeWindowEnd:   req.TimeWindowEnd,
		CreatedBy:       cred,
	})
	if err != nil {
		writeServerErr(w, r, "create population", err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"population": populationWireFrom(pop),
		"row_count":  pop.RowCount, // explicit echo per AC-1 contract
	})
}

// GetPopulation handles GET /v1/populations/{id}.
func (h *Handler) GetPopulation(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := h.tenantContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	pop, err := h.store.GetPopulation(ctx, id)
	if err != nil {
		if errors.Is(err, audit.ErrNotFound) {
			writeError(w, http.StatusNotFound, "population not found")
			return
		}
		writeServerErr(w, r, "get population", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"population": populationWireFrom(pop)})
}

// DrawSample handles POST /v1/samples (AC-2).
func (h *Handler) DrawSample(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.tenantContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}

	var req drawSampleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	popID, err := uuid.Parse(req.PopulationID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "population_id must be a UUID")
		return
	}
	if req.N <= 0 {
		writeError(w, http.StatusBadRequest, "n must be a positive integer")
		return
	}
	if req.Seed == "" {
		writeError(w, http.StatusBadRequest, "seed must be a non-empty string")
		return
	}

	sample, err := h.store.DrawSample(ctx, audit.DrawSampleInput{
		PopulationID: popID,
		N:            req.N,
		Seed:         req.Seed,
		CreatedBy:    cred,
	})
	if err != nil {
		switch {
		case errors.Is(err, audit.ErrNotFound):
			writeError(w, http.StatusNotFound, "population not found")
		case errors.Is(err, audit.ErrEmptyPopulation):
			writeError(w, http.StatusBadRequest, "population is empty")
		default:
			writeServerErr(w, r, "draw sample", err)
		}
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"sample": sampleWireFrom(sample),
	})
}

// GetSample handles GET /v1/samples/{id}.
func (h *Handler) GetSample(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := h.tenantContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	sample, err := h.store.GetSample(ctx, id)
	if err != nil {
		if errors.Is(err, audit.ErrNotFound) {
			writeError(w, http.StatusNotFound, "sample not found")
			return
		}
		writeServerErr(w, r, "get sample", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sample": sampleWireFrom(sample)})
}

// Annotate handles POST /v1/samples/{id}/annotations (AC-4).
func (h *Handler) Annotate(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.tenantContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	sampleID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "sample id must be a UUID")
		return
	}

	var req annotateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	recID, err := uuid.Parse(req.EvidenceRecordID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "evidence_record_id must be a UUID")
		return
	}
	if _, ok := audit.AnnotationResults[req.Result]; !ok {
		writeError(w, http.StatusBadRequest, "result must be one of: passed, failed, not-applicable")
		return
	}

	ann, err := h.store.AnnotateSample(ctx, audit.AnnotateSampleInput{
		SampleID:         sampleID,
		EvidenceRecordID: recID,
		Result:           req.Result,
		AnnotatedBy:      cred,
		Notes:            req.Notes,
	})
	if err != nil {
		switch {
		case errors.Is(err, audit.ErrNotFound):
			writeError(w, http.StatusNotFound, "sample not found")
		case errors.Is(err, audit.ErrInvalidAnnotation):
			writeError(w, http.StatusBadRequest, "invalid annotation result")
		default:
			writeServerErr(w, r, "annotate sample", err)
		}
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"annotation": annotationWireFrom(ann)})
}

// ListAnnotations handles GET /v1/samples/{id}/annotations.
func (h *Handler) ListAnnotations(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := h.tenantContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	sampleID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "sample id must be a UUID")
		return
	}
	anns, err := h.store.ListAnnotations(ctx, sampleID)
	if err != nil {
		writeServerErr(w, r, "list annotations", err)
		return
	}
	out := make([]annotationWire, len(anns))
	for i, a := range anns {
		out[i] = annotationWireFrom(a)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"annotations": out,
		"count":       len(out),
	})
}

// ----- helpers -----

// tenantContext returns the request context (tenant GUC already set by
// slice-033's tenancy.Middleware) plus the credential identifier the
// audit log uses as `actor`. The credential id (slice 003's
// `key_<32hex>`) is the most stable per-request identity surface
// available pre-OIDC (slice 034).
func (h *Handler) tenantContext(r *http.Request) (context.Context, string, bool) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || cred.TenantID == "" {
		return nil, "", false
	}
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		return nil, "", false
	}
	return r.Context(), cred.ID, true
}

func populationWireFrom(p audit.Population) populationWire {
	w := populationWire{
		ID:              p.ID.String(),
		ControlID:       p.ControlID.String(),
		ScopePredicate:  p.ScopePredicate,
		TimeWindowStart: p.TimeWindowStart,
		TimeWindowEnd:   p.TimeWindowEnd,
		RowCount:        p.RowCount,
		CreatedBy:       p.CreatedBy,
		CreatedAt:       p.CreatedAt,
	}
	if p.FrozenAt != nil {
		t := *p.FrozenAt
		w.FrozenAt = &t
	}
	if len(w.ScopePredicate) == 0 {
		w.ScopePredicate = json.RawMessage(`{"op":"true"}`)
	}
	return w
}

func sampleWireFrom(s audit.Sample) sampleWire {
	w := sampleWire{
		ID:           s.ID.String(),
		PopulationID: s.PopulationID.String(),
		N:            s.N,
		Seed:         s.Seed,
		CreatedBy:    s.CreatedBy,
		CreatedAt:    s.CreatedAt,
	}
	w.EvidenceRecordIDs = make([]string, len(s.EvidenceRecordIDs))
	for i, id := range s.EvidenceRecordIDs {
		w.EvidenceRecordIDs[i] = id.String()
	}
	return w
}

func annotationWireFrom(a audit.Annotation) annotationWire {
	return annotationWire{
		ID:               a.ID.String(),
		SampleID:         a.SampleID.String(),
		EvidenceRecordID: a.EvidenceRecordID.String(),
		Result:           a.Result,
		AnnotatedBy:      a.AnnotatedBy,
		AnnotatedAt:      a.AnnotatedAt,
		Notes:            a.Notes,
	}
}

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
