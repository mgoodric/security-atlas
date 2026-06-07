// pack_handlers.go — the slice-032 quarterly board pack HTTP API. Routes
// (appended onto the platform root chi router by httpserver.go, alongside
// the slice-031 board-brief routes):
//
//	POST /v1/board-packs                              AC-1: generate a draft pack
//	GET  /v1/board-packs                              list every pack for the tenant
//	GET  /v1/board-packs/{id}                         fetch the pack content
//	PUT  /v1/board-packs/{id}/sections/{key}          AC-2/3/4: edit a draft section
//	POST /v1/board-packs/{id}/sections/{key}/approve  AC-5: approve a draft section
//	POST /v1/board-packs/{id}/publish                 AC-5: publish (gated on D6)
//	GET  /v1/board-packs/{id}.md                      AC-6: download the Markdown
//	GET  /v1/board-packs/{id}/pdf                     AC-6: download the PDF render
//
// Generation (POST) assembles a DRAFT pack from live program metrics. The
// operator then edits + approves each section (PUT / approve), and publishes
// (POST :publish) — which succeeds only when EVERY section is approved
// (decision D6). A published pack is immutable: PUT / approve on it return
// 409 (AC-7, P0 anti-criterion).
//
// The narrative is TEMPLATED — no LLM (P0 anti-criterion). The board package
// imports no inference client.
//
// All handlers run with the tenant set by upstream auth middleware; the
// board.PackStore / board.PackGenerator open their own per-call transaction
// and apply the tenant GUC so RLS is enforced (constitutional invariant 6).
package board

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	board "github.com/mgoodric/security-atlas/internal/board"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// PackHandler bundles the slice-032 board-pack routes over the PackGenerator
// (the POST generation path) and the PackStore (read + edit + publish).
type PackHandler struct {
	gen   *board.PackGenerator
	store *board.PackStore
}

// NewPack constructs a PackHandler.
func NewPack(gen *board.PackGenerator, store *board.PackStore) *PackHandler {
	return &PackHandler{gen: gen, store: store}
}

// RegisterRoutes attaches the slice-032 routes directly onto the supplied
// chi.Router — the Mount-append convention in internal/api/httpserver.go.
// NEVER wrap with a second chi.NewRouter().Mount("/", ...): chi panics on a
// duplicate Mount at "/".
//
// Route-declaration order matters: the literal-suffix routes
// (/{id}.md, /{id}/pdf) and the deeper /{id}/sections/... routes are
// declared before the bare /{id} so chi's declaration-order match keeps
// them ahead of the generic id route.
func (h *PackHandler) RegisterRoutes(r chi.Router) {
	r.Post("/v1/board-packs", h.Generate)
	r.Get("/v1/board-packs", h.List)
	r.Post("/v1/board-packs/{id}/publish", h.Publish)
	r.Post("/v1/board-packs/{id}/sections/{key}/approve", h.ApproveSection)
	r.Put("/v1/board-packs/{id}/sections/{key}", h.UpdateSection)
	r.Get("/v1/board-packs/{id}/pdf", h.PDF)
	r.Get("/v1/board-packs/{id}.md", h.Markdown)
	r.Get("/v1/board-packs/{id}", h.Get)
}

// packGenerateRequest is the POST /v1/board-packs body.
type packGenerateRequest struct {
	// PeriodEnd is the quarter-end report date the pack is pinned to
	// (YYYY-MM-DD).
	PeriodEnd string `json:"period_end"`
}

// Generate handles POST /v1/board-packs (AC-1).
//
// Body: { "period_end": "YYYY-MM-DD" }. A DRAFT pack is assembled from live
// program metrics with all fixed sections, the templated per-section
// narrative rendered, and the draft row appended. Returns 201 with the
// stored draft pack.
//
//   - 201 the stored draft pack
//   - 400 when period_end is missing or not a YYYY-MM-DD date
//   - 401 when the tenant context is missing
//   - 500 on an unexpected generation failure
func (h *PackHandler) Generate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	var req packGenerateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "request body must be JSON with a period_end field")
		return
	}
	if strings.TrimSpace(req.PeriodEnd) == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "period_end is required (YYYY-MM-DD)")
		return
	}
	if _, err := time.Parse("2006-01-02", req.PeriodEnd); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "period_end must be a YYYY-MM-DD date")
		return
	}

	stored, err := h.gen.Generate(ctx, req.PeriodEnd)
	if err != nil {
		if errors.Is(err, board.ErrPackBadPeriodEnd) {
			httpresp.WriteError(w, http.StatusBadRequest, "period_end must be a YYYY-MM-DD date")
			return
		}
		httperr.WriteInternal(w, r, "board", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusCreated, packWireFromStored(stored))
}

// Get handles GET /v1/board-packs/{id}.
//
//   - 200 the stored pack (draft or published)
//   - 400 when {id} is not a UUID
//   - 404 when the id does not resolve in the caller's tenant
func (h *PackHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, ok := parseID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	stored, err := h.store.Get(ctx, id)
	if err != nil {
		h.writePackError(w, r, err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, packWireFromStored(stored))
}

// List handles GET /v1/board-packs — every pack for the tenant, newest
// report-date first.
func (h *PackHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	stored, err := h.store.List(ctx)
	if err != nil {
		httperr.WriteInternal(w, r, "board", err)
		return
	}
	wires := make([]packWire, 0, len(stored))
	for _, sp := range stored {
		wires = append(wires, packWireFromStored(sp))
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"packs": wires})
}

// updateSectionRequest is the PUT /v1/board-packs/{id}/sections/{key} body.
// Every field is optional — only the populated ones are applied. The pointer
// types let the handler distinguish "not supplied" from "set to zero/empty"
// (AC-2, AC-3, AC-4).
type updateSectionRequest struct {
	// OverrideText, when non-nil, replaces the section's override narrative
	// (AC-2 / AC-4). An empty string clears the override.
	OverrideText *string `json:"override_text,omitempty"`
	// Inputs, when non-nil, carries operator-entered structured inputs for
	// the operational_metrics / investment / coverage_trend sections
	// (AC-3, decisions D3 + D5).
	Inputs *board.SectionInputs `json:"inputs,omitempty"`
}

// UpdateSection handles PUT /v1/board-packs/{id}/sections/{key}
// (AC-2 / AC-3 / AC-4).
//
// Applies an operator edit — override narrative and/or structured inputs —
// to one section of a DRAFT pack. The investment / coverage_trend computed
// fields (coverage delta, cost-per-coverage-point) are re-derived; the
// whole-pack narrative is re-rendered.
//
//   - 200 the updated stored pack
//   - 400 when {id} is not a UUID or the body is malformed
//   - 404 when the id or the section key does not resolve
//   - 409 when the pack is already published (AC-7 immutability)
func (h *PackHandler) UpdateSection(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, ok := parseID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	sectionKey := chi.URLParam(r, "key")

	var req updateSectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "request body must be JSON")
		return
	}

	edit := board.SectionEdit{
		SectionKey:   sectionKey,
		OverrideText: req.OverrideText,
		Inputs:       req.Inputs,
	}
	stored, err := h.store.UpdateSection(ctx, id, edit)
	if err != nil {
		h.writePackError(w, r, err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, packWireFromStored(stored))
}

// ApproveSection handles POST /v1/board-packs/{id}/sections/{key}/approve
// (AC-5).
//
// Sets the per-section approval flag to true on a DRAFT pack. The publish
// gate (decision D6) requires every section approved.
//
//   - 200 the updated stored pack
//   - 400 when {id} is not a UUID
//   - 404 when the id or the section key does not resolve
//   - 409 when the pack is already published
func (h *PackHandler) ApproveSection(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, ok := parseID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	sectionKey := chi.URLParam(r, "key")

	approved := true
	edit := board.SectionEdit{
		SectionKey: sectionKey,
		Approved:   &approved,
	}
	stored, err := h.store.UpdateSection(ctx, id, edit)
	if err != nil {
		h.writePackError(w, r, err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, packWireFromStored(stored))
}

// publishRequest is the POST /v1/board-packs/{id}/publish body.
type publishRequest struct {
	// PublishedBy identifies the human who one-click-approved the publish
	// (CLAUDE.md AI-assist boundary: an audit-binding artifact requires a
	// human approver). Required.
	PublishedBy string `json:"published_by"`
}

// Publish handles POST /v1/board-packs/{id}/publish (AC-5).
//
// Flips a DRAFT pack to PUBLISHED — but only when every fixed section is
// approved (decision D6). Once published the pack is immutable.
//
//   - 200 the published (frozen) pack
//   - 400 when {id} is not a UUID or published_by is missing
//   - 404 when the id does not resolve
//   - 409 when the pack is already published, or a section is not approved
func (h *PackHandler) Publish(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, ok := parseID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	var req publishRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "request body must be JSON with a published_by field")
		return
	}
	if strings.TrimSpace(req.PublishedBy) == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "published_by is required (the human approving publication)")
		return
	}

	stored, err := h.store.Publish(ctx, id, req.PublishedBy)
	if err != nil {
		h.writePackError(w, r, err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, packWireFromStored(stored))
}

// Markdown handles GET /v1/board-packs/{id}.md (AC-6).
//
// Returns the stored narrative Markdown with a text/markdown content-type so
// a browser / curl downloads it cleanly for paste into the deck.
func (h *PackHandler) Markdown(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, ok := parseID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	stored, err := h.store.Get(ctx, id)
	if err != nil {
		h.writePackError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition",
		`attachment; filename="board-pack-`+stored.PeriodEnd+`.md"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(stored.NarrativeMd))
}

// PDF handles GET /v1/board-packs/{id}/pdf (AC-6).
//
// Renders the stored pack to a PDF on demand via the existing chromedp path.
// When Chrome is unavailable the handler returns 503 so an operator can run
// the platform without Chrome — the pack + Markdown still work.
//
//   - 200 application/pdf (bytes begin with %PDF-)
//   - 404 when the id does not resolve in-tenant
//   - 503 when the Chrome browser is unavailable
func (h *PackHandler) PDF(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, ok := parseID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	stored, err := h.store.Get(ctx, id)
	if err != nil {
		h.writePackError(w, r, err)
		return
	}

	// Render budget + concurrency cap live on the shared pdfrender limiter
	// (slice 475); board.RenderPackPDF routes through it.
	pdfBytes, err := board.RenderPackPDF(ctx, stored)
	if err != nil {
		if status, msg, ok := pdfDegradation(err); ok {
			logPDFDegradation(r, "board-pack", err)
			httpresp.WriteError(w, status, msg)
			return
		}
		httperr.WriteInternal(w, r, "board", err)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition",
		`attachment; filename="board-pack-`+stored.PeriodEnd+`.pdf"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pdfBytes)
}

// writePackError maps a board pack domain error to the right HTTP status.
func (h *PackHandler) writePackError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, board.ErrPackNotFound):
		httpresp.WriteError(w, http.StatusNotFound, "board pack not found")
	case errors.Is(err, board.ErrUnknownSection):
		httpresp.WriteError(w, http.StatusNotFound, "unknown board pack section key")
	case errors.Is(err, board.ErrPackNotDraft):
		httpresp.WriteError(w, http.StatusConflict, "board pack is published and immutable")
	case errors.Is(err, board.ErrPackNotReady):
		httpresp.WriteError(w, http.StatusConflict, err.Error())
	default:
		httperr.WriteInternal(w, r, "board", err)
	}
}

// ----- wire shapes -----

// packWire is the JSON shape of a quarterly board pack in slice-032
// responses. `content` is the structured Pack; `narrative_md` is the
// rendered narrative.
type packWire struct {
	ID          string     `json:"id"`
	PeriodEnd   string     `json:"period_end"`
	Status      string     `json:"status"`
	Content     board.Pack `json:"content"`
	NarrativeMd string     `json:"narrative_md"`
	PublishedBy string     `json:"published_by,omitempty"`
	PublishedAt string     `json:"published_at,omitempty"`
	CreatedAt   string     `json:"created_at"`
	UpdatedAt   string     `json:"updated_at"`
}

func packWireFromStored(sp board.StoredPack) packWire {
	pw := packWire{
		ID:          sp.ID.String(),
		PeriodEnd:   sp.PeriodEnd,
		Status:      sp.Status,
		Content:     sp.Content,
		NarrativeMd: sp.NarrativeMd,
		PublishedBy: sp.PublishedBy,
	}
	if !sp.PublishedAt.IsZero() {
		pw.PublishedAt = sp.PublishedAt.UTC().Format(time.RFC3339)
	}
	if !sp.CreatedAt.IsZero() {
		pw.CreatedAt = sp.CreatedAt.UTC().Format(time.RFC3339)
	}
	if !sp.UpdatedAt.IsZero() {
		pw.UpdatedAt = sp.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return pw
}
