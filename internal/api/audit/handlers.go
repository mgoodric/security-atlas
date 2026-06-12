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
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/audit"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// sampleReader is the per-route read seam the three single-resource GET paths
// — GET /v1/populations/{id} (GetPopulation), GET /v1/samples/{id}
// (GetSample), and GET /v1/samples/{id}/annotations (ListAnnotations) — read
// through (slice 689 added the first two; slice 690's contract-tier rollout
// adds the annotation-list read). It carries JUST the read methods those
// routes need — deliberately narrow (slice 409 D1 / slice 411 D2 / slice 412
// D2 sizing rule: a three-method seam over the wider audit.Store, NOT a full
// mirror of its create/draw/annotate surface). The contract-tier recorder
// (contractrecord_test.go) injects a fixed-row stub satisfying this seam so
// the wire shapes record on the plain `go test ./...` unit surface with no
// Postgres pool (ADR-0007 / P0-409-1). The production *audit.Store satisfies
// it verbatim; the seam is unexported and New(*audit.Store) is unchanged
// (P0-409-2). The write/create handlers (CreatePopulation/DrawSample/Annotate)
// keep using the concrete h.store directly.
type sampleReader interface {
	GetPopulation(ctx context.Context, id uuid.UUID) (audit.Population, error)
	GetSample(ctx context.Context, id uuid.UUID) (audit.Sample, error)
	ListAnnotations(ctx context.Context, sampleID uuid.UUID) ([]audit.Annotation, error)
}

// Handler bundles the slice-026 routes over a single audit.Store.
//
// reader is the slice-689 per-route read seam the GetPopulation/GetSample
// paths read through; New points it at store, so production behavior is
// identical. The write handlers keep using store directly.
type Handler struct {
	store  *audit.Store
	reader sampleReader
}

// New constructs a Handler. The slice-689 per-route read seam (reader) is
// wired to the same store — the public signature is unchanged (P0-409-2).
func New(store *audit.Store) *Handler { return &Handler{store: store, reader: store} }

// newHandlerWithReader constructs a Handler whose GetPopulation/GetSample
// paths read through an arbitrary read seam. It exists ONLY for the slice-689
// contract recorder, which injects a fixed-row stub so the single-resource
// wire shapes record with no Postgres pool. Unexported — not part of the
// public surface.
func newHandlerWithReader(reader sampleReader) *Handler {
	return &Handler{reader: reader}
}

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
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}

	var req createPopulationReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	controlID, err := uuid.Parse(req.ControlID)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "control_id must be a UUID")
		return
	}
	if req.TimeWindowStart.IsZero() || req.TimeWindowEnd.IsZero() {
		httpresp.WriteError(w, http.StatusBadRequest, "time_window_start and time_window_end are required")
		return
	}
	if req.TimeWindowStart.After(req.TimeWindowEnd) {
		httpresp.WriteError(w, http.StatusBadRequest, "time_window_start must be <= time_window_end")
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
		httperr.WriteInternal(w, r, "create population", err)
		return
	}

	httpresp.WriteJSON(w, http.StatusCreated, map[string]any{
		"population": populationWireFrom(pop),
		"row_count":  pop.RowCount,
	})

}

// GetPopulation handles GET /v1/populations/{id}.
func (h *Handler) GetPopulation(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	pop, err := h.reader.GetPopulation(ctx, id)
	if err != nil {
		if errors.Is(err, audit.ErrNotFound) {
			httpresp.WriteError(w, http.StatusNotFound, "population not found")
			return
		}
		httperr.WriteInternal(w, r, "get population", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"population": populationWireFrom(pop)})
}

// DrawSample handles POST /v1/samples (AC-2).
func (h *Handler) DrawSample(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}

	var req drawSampleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	popID, err := uuid.Parse(req.PopulationID)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "population_id must be a UUID")
		return
	}
	if req.N <= 0 {
		httpresp.WriteError(w, http.StatusBadRequest, "n must be a positive integer")
		return
	}
	if req.Seed == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "seed must be a non-empty string")
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
			httpresp.WriteError(w, http.StatusNotFound, "population not found")
		case errors.Is(err, audit.ErrEmptyPopulation):
			httpresp.WriteError(w, http.StatusBadRequest, "population is empty")
		default:
			httperr.WriteInternal(w, r, "draw sample", err)
		}
		return
	}

	httpresp.WriteJSON(w, http.StatusCreated, map[string]any{
		"sample": sampleWireFrom(sample),
	})

}

// GetSample handles GET /v1/samples/{id}.
func (h *Handler) GetSample(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	sample, err := h.reader.GetSample(ctx, id)
	if err != nil {
		if errors.Is(err, audit.ErrNotFound) {
			httpresp.WriteError(w, http.StatusNotFound, "sample not found")
			return
		}
		httperr.WriteInternal(w, r, "get sample", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"sample": sampleWireFrom(sample)})
}

// Annotate handles POST /v1/samples/{id}/annotations (AC-4).
func (h *Handler) Annotate(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	sampleID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "sample id must be a UUID")
		return
	}

	var req annotateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	recID, err := uuid.Parse(req.EvidenceRecordID)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "evidence_record_id must be a UUID")
		return
	}
	if _, ok := audit.AnnotationResults[req.Result]; !ok {
		httpresp.WriteError(w, http.StatusBadRequest, "result must be one of: passed, failed, not-applicable")
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
			httpresp.WriteError(w, http.StatusNotFound, "sample not found")
		case errors.Is(err, audit.ErrInvalidAnnotation):
			httpresp.WriteError(w, http.StatusBadRequest, "invalid annotation result")
		default:
			httperr.WriteInternal(w, r, "annotate sample", err)
		}
		return
	}

	httpresp.WriteJSON(w, http.StatusCreated, map[string]any{"annotation": annotationWireFrom(ann)})
}

// ListAnnotations handles GET /v1/samples/{id}/annotations.
func (h *Handler) ListAnnotations(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	sampleID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "sample id must be a UUID")
		return
	}
	anns, err := h.reader.ListAnnotations(ctx, sampleID)
	if err != nil {
		httperr.WriteInternal(w, r, "list annotations", err)
		return
	}
	out := make([]annotationWire, len(anns))
	for i, a := range anns {
		out[i] = annotationWireFrom(a)
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
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
