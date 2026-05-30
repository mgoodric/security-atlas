// Package questionnaires serves slice 155's tracer-bullet HTTP API.
//
// Routes (registered onto the platform root chi router by httpserver.go
// via the Mount-append convention — chi rejects two Mounts at "/"):
//
//	POST   /v1/questionnaires                       create empty draft
//	GET    /v1/questionnaires                       list tenant's questionnaires
//	POST   /v1/questionnaires/{id}/import-excel     parse xlsx + insert questions
//	GET    /v1/questionnaires/{id}                  read questionnaire + questions + answers
//	PATCH  /v1/questionnaires/{id}/answers/{qid}    upsert one answer
//	GET    /v1/questionnaires/{id}/suggestions      AnswerLibrary suggestion lookup
//	POST   /v1/questionnaires/{id}/export-pdf       render PDF
//
// Tenant scoping is enforced by RLS via the Store; every handler
// requires a TenantFromContext OK before any DB call.
package questionnaires

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/questionnaire"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Handler bundles the slice-155 questionnaire routes against the Store.
type Handler struct {
	store *questionnaire.Store
}

// New constructs a Handler.
func New(store *questionnaire.Store) *Handler {
	return &Handler{store: store}
}

// RegisterRoutes attaches the slice-155 routes directly onto the
// supplied chi.Router — the Mount-append convention from
// internal/api/httpserver.go. NEVER wrap with a second
// chi.NewRouter().Mount("/", ...) — chi panics on duplicate Mount at
// "/". The longest-literal-suffix routes are declared before the bare
// {id} route so chi's declaration-order match resolves them first.
func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Post("/v1/questionnaires", h.Create)
	r.Get("/v1/questionnaires", h.List)
	r.Post("/v1/questionnaires/{id}/import-excel", h.ImportExcel)
	r.Get("/v1/questionnaires/{id}/suggestions", h.Suggestions)
	r.Post("/v1/questionnaires/{id}/export-pdf", h.ExportPDF)
	r.Patch("/v1/questionnaires/{id}/answers/{qid}", h.UpsertAnswer)
	r.Get("/v1/questionnaires/{id}", h.Get)
}

// ===== POST /v1/questionnaires =====

type createRequest struct {
	Name           string `json:"name"`
	SourceLabel    string `json:"source_label"`
	SourceFilename string `json:"source_filename"`
}

// Create handles POST /v1/questionnaires.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "request body must be JSON")
		return
	}
	if req.Name == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}
	q, err := h.store.CreateQuestionnaire(ctx, questionnaire.CreateQuestionnaireParams{
		Name:           req.Name,
		SourceLabel:    req.SourceLabel,
		SourceFilename: req.SourceFilename,
	})
	if err != nil {
		httperr.WriteInternal(w, r, "questionnaires", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusCreated, q)
}

// ===== GET /v1/questionnaires =====

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	list, err := h.store.ListQuestionnaires(ctx)
	if err != nil {
		httperr.WriteInternal(w, r, "questionnaires", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"questionnaires": list})
}

// ===== POST /v1/questionnaires/{id}/import-excel =====

// importResponse is the shape returned after a successful Excel import.
type importResponse struct {
	Questions       []questionnaire.Question `json:"questions"`
	UnmappedColumns []string                 `json:"unmapped_columns"`
}

// ImportExcel parses an inbound xlsx multipart upload and inserts the
// parsed questions onto the questionnaire. The handler caps the upload
// at MaxUploadBytes BEFORE invoking the parser (defense in depth — the
// parser also checks).
func (h *Handler) ImportExcel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "id is required")
		return
	}

	// Cap body size BEFORE parsing the multipart form.
	r.Body = http.MaxBytesReader(w, r.Body, questionnaire.MaxUploadBytes)

	if err := r.ParseMultipartForm(questionnaire.MaxUploadBytes); err != nil {
		httpresp.WriteError(w, http.StatusRequestEntityTooLarge, "upload exceeds size cap")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "missing file field")
		return
	}
	defer func() { _ = file.Close() }()

	raw, err := io.ReadAll(file)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "could not read upload")
		return
	}

	parsed, err := questionnaire.ParseExcel(raw)
	if err != nil {
		switch {
		case errors.Is(err, questionnaire.ErrUploadTooLarge):
			httpresp.WriteError(w, http.StatusRequestEntityTooLarge, err.Error())
		case errors.Is(err, questionnaire.ErrEmptyWorkbook),
			errors.Is(err, questionnaire.ErrNoHeaderRow),
			errors.Is(err, questionnaire.ErrTooManyRows):
			httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		default:
			httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		}
		return
	}

	added, err := h.store.AddQuestionsFromParse(ctx, id, parsed.Questions)
	if err != nil {
		httperr.WriteInternal(w, r, "questionnaires", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusCreated, importResponse{
		Questions:       added,
		UnmappedColumns: parsed.UnmappedColumns,
	})

}

// ===== GET /v1/questionnaires/{id} =====

// getResponse is the read-shape.
type getResponse struct {
	Questionnaire *questionnaire.Questionnaire `json:"questionnaire"`
	Questions     []questionnaire.Question     `json:"questions"`
}

// Get returns one questionnaire with its question + answer set.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id := chi.URLParam(r, "id")
	q, err := h.store.GetQuestionnaire(ctx, id)
	if err != nil {
		httpresp.WriteError(w, http.StatusNotFound, "questionnaire not found")
		return
	}
	qs, err := h.store.ListQuestionsWithAnswers(ctx, id)
	if err != nil {
		httperr.WriteInternal(w, r, "questionnaires", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, getResponse{Questionnaire: q, Questions: qs})
}

// ===== PATCH /v1/questionnaires/{id}/answers/{qid} =====

type upsertAnswerRequest struct {
	AnswerValue   string `json:"answer_value"`
	Narrative     string `json:"narrative"`
	Citations     []any  `json:"citations"`
	AuthoredBy    string `json:"authored_by"`
	SaveToLibrary bool   `json:"save_to_library"`
	SCFAnchorID   string `json:"scf_anchor_id"`
	SourceLabel   string `json:"source_label"`
}

// UpsertAnswer writes the single answer for a question. SaveToLibrary
// optionally appends the canonical entry.
func (h *Handler) UpsertAnswer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	qid := chi.URLParam(r, "qid")
	if qid == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "qid is required")
		return
	}
	var req upsertAnswerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "request body must be JSON")
		return
	}
	a, err := h.store.UpsertAnswer(ctx, questionnaire.AnswerParams{
		QuestionID:      qid,
		AnswerValue:     req.AnswerValue,
		Narrative:       req.Narrative,
		Citations:       req.Citations,
		AuthoredBy:      req.AuthoredBy,
		SaveToLibrary:   req.SaveToLibrary,
		SCFAnchorIDHint: req.SCFAnchorID,
		SourceLabel:     req.SourceLabel,
	})
	if err != nil {
		httperr.WriteInternal(w, r, "questionnaires", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, a)
}

// ===== GET /v1/questionnaires/{id}/suggestions?anchor=IAC-06&limit=10 =====

// Suggestions returns prior canonical answers for a given SCF anchor.
func (h *Handler) Suggestions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	anchor := r.URL.Query().Get("anchor")
	if anchor == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "anchor query param is required")
		return
	}
	list, err := h.store.SuggestForAnchorWithPool(ctx, anchor, questionnaire.DefaultSuggestionLimit)
	if err != nil {
		httperr.WriteInternal(w, r, "questionnaires", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"suggestions": list})
}

// ===== POST /v1/questionnaires/{id}/export-pdf =====

// ExportPDF renders the questionnaire to PDF.
func (h *Handler) ExportPDF(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id := chi.URLParam(r, "id")
	q, err := h.store.GetQuestionnaire(ctx, id)
	if err != nil {
		httpresp.WriteError(w, http.StatusNotFound, "questionnaire not found")
		return
	}
	items, err := h.store.ListQuestionsWithAnswers(ctx, id)
	if err != nil {
		httperr.WriteInternal(w, r, "questionnaires", err)
		return
	}
	in := questionnaire.PDFInput{
		QuestionnaireName: q.Name,
		SourceLabel:       q.SourceLabel,
		GeneratedAt:       time.Now().UTC().Format(time.RFC3339),
		Items:             make([]questionnaire.PDFItem, 0, len(items)),
	}
	for _, it := range items {
		pi := questionnaire.PDFItem{
			Code:         it.Code,
			Text:         it.Text,
			Domain:       it.Domain,
			ScfAnchorID:  it.ScfAnchorID,
			NeedsMapping: it.NeedsMapping,
		}
		if it.Answer != nil {
			pi.AnswerValue = it.Answer.AnswerValue
			pi.Narrative = it.Answer.Narrative
		}
		in.Items = append(in.Items, pi)
	}

	pdfCtx, cancel := contextWithPDFDeadline(r.Context())
	defer cancel()
	buf, err := questionnaire.RenderPDF(pdfCtx, in)
	if err != nil {
		if errors.Is(err, questionnaire.ErrChromeUnavailable) {
			httpresp.WriteError(w, http.StatusServiceUnavailable, "PDF export disabled: chrome not available in this deployment")
			return
		}
		httperr.WriteInternal(w, r, "questionnaires", err)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	_, _ = w.Write(buf)
}
