// Package board serves the slice-031 monthly board brief HTTP API. Routes
// (appended onto the platform root chi router by httpserver.go):
//
//	POST /v1/board-briefs            AC-1: generate a brief pinned to period_end
//	GET  /v1/board-briefs            list every brief for the tenant
//	GET  /v1/board-briefs/{id}       AC-5: fetch the frozen brief content
//	GET  /v1/board-briefs/{id}.md    AC-4: download the frozen Markdown
//	GET  /v1/board-briefs/{id}/pdf   AC-4: download the on-demand PDF render
//
// Generation (POST) assembles the brief from live program metrics and
// APPENDS it as a frozen, immutable snapshot. Reads return the frozen
// content verbatim — re-fetching after live state changes returns the
// original (AC-5). A second POST with the same period_end creates a NEW
// brief, never an edit (P0 anti-criterion).
//
// The narrative is TEMPLATED — no LLM (AC-6, P0 anti-criterion). The board
// package imports no inference client.
//
// All handlers run with the tenant set by upstream auth middleware; the
// board.Store / board.Generator open their own per-call transaction and
// apply the tenant GUC so RLS is enforced (constitutional invariant 6).
package board

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	board "github.com/mgoodric/security-atlas/internal/board"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Handler bundles the slice-031 board-brief routes over the Generator (the
// POST generation path) and the Store (the read paths).
type Handler struct {
	gen   *board.Generator
	store *board.Store
}

// New constructs a Handler.
func New(gen *board.Generator, store *board.Store) *Handler {
	return &Handler{gen: gen, store: store}
}

// RegisterRoutes attaches the slice-031 routes directly onto the supplied
// chi.Router — the Mount-append convention in internal/api/httpserver.go.
// NEVER wrap with a second chi.NewRouter().Mount("/", ...): chi panics on a
// duplicate Mount at "/".
//
// Route-declaration order matters: the literal-suffix routes
// (/{id}.md, /{id}/pdf) are declared BEFORE the bare /{id} so chi's
// declaration-order match within the same method keeps them ahead of the
// generic id route. `.md` is handled as a distinct path segment shape; chi
// treats `{id}.md` as a literal-dotted param so it does not collide with the
// bare `{id}`.
func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Post("/v1/board-briefs", h.Generate)
	r.Get("/v1/board-briefs", h.List)
	r.Get("/v1/board-briefs/{id}/pdf", h.PDF)
	r.Get("/v1/board-briefs/{id}.md", h.Markdown)
	r.Get("/v1/board-briefs/{id}", h.Get)
}

// generateRequest is the POST /v1/board-briefs body.
type generateRequest struct {
	// PeriodEnd is the report date the brief is pinned to (YYYY-MM-DD).
	PeriodEnd string `json:"period_end"`
}

// Generate handles POST /v1/board-briefs (AC-1).
//
// Body: { "period_end": "YYYY-MM-DD" }. The brief is assembled from live
// program metrics at call time, the templated narrative rendered, and the
// frozen snapshot appended to board_briefs. Returns 201 with the stored
// brief.
//
//   - 201 { id, period_end, generated_at, content, narrative_md }
//   - 400 when period_end is missing or not a YYYY-MM-DD date
//   - 401 when the tenant context is missing
//   - 500 on an unexpected generation failure
func (h *Handler) Generate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	var req generateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "request body must be JSON with a period_end field")
		return
	}
	if strings.TrimSpace(req.PeriodEnd) == "" {
		writeError(w, http.StatusBadRequest, "period_end is required (YYYY-MM-DD)")
		return
	}
	// Validate the date shape up front so a malformed value is a clean 400
	// rather than reaching the Generator.
	if _, err := time.Parse("2006-01-02", req.PeriodEnd); err != nil {
		writeError(w, http.StatusBadRequest, "period_end must be a YYYY-MM-DD date")
		return
	}

	stored, err := h.gen.Generate(ctx, req.PeriodEnd)
	if err != nil {
		if errors.Is(err, board.ErrBadPeriodEnd) {
			writeError(w, http.StatusBadRequest, "period_end must be a YYYY-MM-DD date")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, briefWireFromStored(stored))
}

// Get handles GET /v1/board-briefs/{id} (AC-5).
//
// Returns the frozen brief content verbatim — re-fetching after live state
// changes returns the original.
//
//   - 200 { id, period_end, generated_at, content, narrative_md }
//   - 400 when {id} is not a UUID
//   - 404 when the id does not resolve in the caller's tenant (a
//     cross-tenant id is invisible under RLS and surfaces the same way)
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, ok := parseID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	stored, err := h.store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, board.ErrNotFound) {
			writeError(w, http.StatusNotFound, "board brief not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, briefWireFromStored(stored))
}

// List handles GET /v1/board-briefs — every brief for the tenant, newest
// report-date first.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	stored, err := h.store.List(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	wires := make([]briefWire, 0, len(stored))
	for _, sb := range stored {
		wires = append(wires, briefWireFromStored(sb))
	}
	writeJSON(w, http.StatusOK, map[string]any{"briefs": wires})
}

// Markdown handles GET /v1/board-briefs/{id}.md (AC-4).
//
// Returns the frozen narrative Markdown with a text/markdown content-type so
// a browser / curl downloads it cleanly for paste into the deck.
func (h *Handler) Markdown(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, ok := parseID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	stored, err := h.store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, board.ErrNotFound) {
			writeError(w, http.StatusNotFound, "board brief not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition",
		`attachment; filename="board-brief-`+stored.PeriodEnd+`.md"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(stored.NarrativeMd))
}

// PDF handles GET /v1/board-briefs/{id}/pdf (AC-4).
//
// Renders the frozen brief to a PDF on demand via the existing chromedp
// path. When Chrome is unavailable the handler returns 503 so an operator
// can run the platform without Chrome — the brief + Markdown still work.
//
//   - 200 application/pdf (bytes begin with %PDF-)
//   - 404 when the id does not resolve in-tenant
//   - 503 when the Chrome browser is unavailable
func (h *Handler) PDF(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, ok := parseID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	stored, err := h.store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, board.ErrNotFound) {
			writeError(w, http.StatusNotFound, "board brief not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	renderCtx, cancel := context.WithTimeout(ctx, board.PDFTimeout)
	defer cancel()
	pdfBytes, err := board.RenderPDF(renderCtx, stored)
	if err != nil {
		if errors.Is(err, board.ErrChromeUnavailable) {
			writeError(w, http.StatusServiceUnavailable,
				"PDF rendering unavailable: chrome browser not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition",
		`attachment; filename="board-brief-`+stored.PeriodEnd+`.pdf"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pdfBytes)
}

// ----- wire shapes -----

// briefWire is the JSON shape of a board brief in slice-031 responses. The
// `content` field is the frozen structured Brief; `narrative_md` is the
// frozen rendered narrative.
type briefWire struct {
	ID          string      `json:"id"`
	PeriodEnd   string      `json:"period_end"`
	GeneratedAt string      `json:"generated_at"`
	Content     board.Brief `json:"content"`
	NarrativeMd string      `json:"narrative_md"`
}

func briefWireFromStored(sb board.StoredBrief) briefWire {
	return briefWire{
		ID:          sb.ID.String(),
		PeriodEnd:   sb.PeriodEnd,
		GeneratedAt: sb.GeneratedAt.UTC().Format(time.RFC3339),
		Content:     sb.Content,
		NarrativeMd: sb.NarrativeMd,
	}
}

// ----- helpers -----

// parseID parses the {id} path segment as a UUID, writing a 400 and
// returning false on a malformed value.
func parseID(w http.ResponseWriter, raw string) (uuid.UUID, bool) {
	id, err := uuid.Parse(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "board brief id must be a UUID")
		return uuid.UUID{}, false
	}
	return id, true
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
