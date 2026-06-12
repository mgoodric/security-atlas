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
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/qaisuggest"
	"github.com/mgoodric/security-atlas/internal/questionnaire"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Handler bundles the slice-155 questionnaire routes against the Store, plus
// the slice-441 AI-suggestion routes against the qaisuggest.Service. The
// suggestion service is OPTIONAL — when nil (e.g. a deployment that has not
// wired local inference) the suggest/approve routes return 503 rather than
// panicking, so the rest of the questionnaire surface is unaffected.
type Handler struct {
	store   *questionnaire.Store
	suggest *qaisuggest.Service
}

// New constructs a Handler without the AI-suggestion surface (slice 155 only).
func New(store *questionnaire.Store) *Handler {
	return &Handler{store: store}
}

// NewWithSuggest constructs a Handler wired with the slice-441 AI-suggestion
// service. When suggest is non-nil the suggest + approve routes are live.
func NewWithSuggest(store *questionnaire.Store, suggest *qaisuggest.Service) *Handler {
	return &Handler{store: store, suggest: suggest}
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
	// Slice 441 — AI-answer suggestion v0. The longest-literal-suffix routes
	// (answers/{qid}/ai-suggest, answers/{qid}/ai-approve) are declared before
	// the bare answers/{qid} PATCH so chi's declaration-order match resolves
	// them first.
	r.Post("/v1/questionnaires/{id}/answers/{qid}/ai-suggest", h.AISuggest)
	r.Post("/v1/questionnaires/{id}/answers/{qid}/ai-approve", h.AIApprove)
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

	// The render budget + concurrency cap live on the shared pdfrender
	// limiter (slice 475); questionnaire.RenderPDF routes through it. We do
	// NOT apply a second deadline here — the limiter owns it.
	buf, err := questionnaire.RenderPDF(r.Context(), in)
	if err != nil {
		if status, msg, ok := pdfDegradation(err); ok {
			logPDFDegradation(r, err)
			httpresp.WriteError(w, status, msg)
			return
		}
		httperr.WriteInternal(w, r, "questionnaires", err)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	_, _ = w.Write(buf)
}

// ===== POST /v1/questionnaires/{id}/answers/{qid}/ai-suggest =====
//
// Slice 441 — generate a cited AI DRAFT answer for one question. The draft is
// persisted as ai_assisted=TRUE, human_approved=FALSE (NOT returned to a
// customer; not approved). Constitutional invariants (no fabricated coverage,
// no cross-tenant bleed, local-only) are enforced by the qaisuggest.Service.
//
// Role-gated: questionnaire-response is a grc_engineer (IsApprover) / admin
// capability (AC-9, threat-model S). A bare read role cannot generate a draft
// that will become a customer-facing answer.
func (h *Handler) AISuggest(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.tenantCred(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if !cred.IsApprover && !cred.IsAdmin {
		httpresp.WriteError(w, http.StatusForbidden, "grc_engineer role required to generate an AI answer suggestion")
		return
	}
	if h.suggest == nil {
		httpresp.WriteError(w, http.StatusServiceUnavailable, "AI suggestion is not enabled on this deployment")
		return
	}
	qid, err := uuid.Parse(chi.URLParam(r, "qid"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "qid must be a uuid")
		return
	}
	out, err := h.suggest.Suggest(ctx, qaisuggest.SuggestParams{
		QuestionID: qid,
		AuthoredBy: cred.ID,
	})
	if err != nil {
		if errors.Is(err, qaisuggest.ErrQuestionNotFound) {
			httpresp.WriteError(w, http.StatusNotFound, "question not found")
			return
		}
		httperr.WriteInternal(w, r, "questionnaires", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, out)
}

// ===== POST /v1/questionnaires/{id}/answers/{qid}/ai-approve =====
//
// Slice 441 — one-click human approval of an AI-suggested draft (AC-6/AC-12).
// Records the approver + flips human_approved=TRUE; the operator's edited
// final text is what the questionnaire stores. There is NO auto-approve path —
// this endpoint is the ONLY way an AI draft becomes approved. The DB CHECK
// makes human_approved=TRUE without a human_approver impossible (P0-441-8).
//
// Role-gated identically to AISuggest.
func (h *Handler) AIApprove(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.tenantCred(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if !cred.IsApprover && !cred.IsAdmin {
		httpresp.WriteError(w, http.StatusForbidden, "grc_engineer role required to approve an AI answer")
		return
	}
	if h.suggest == nil {
		httpresp.WriteError(w, http.StatusServiceUnavailable, "AI suggestion is not enabled on this deployment")
		return
	}
	var req aiApproveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "request body must be JSON")
		return
	}
	answerID, err := uuid.Parse(req.AnswerID)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "answer_id must be a uuid")
		return
	}
	approved, err := h.suggest.Approve(ctx, qaisuggest.ApproveParams{
		AnswerID:    answerID,
		Narrative:   req.Narrative,
		AnswerValue: req.AnswerValue,
		// The approver is the authenticated credential — NEVER client-supplied
		// (a caller cannot approve "as" someone else).
		Approver: cred.ID,
	})
	if err != nil {
		switch {
		case errors.Is(err, qaisuggest.ErrApproverRequired):
			// Should be unreachable (cred.ID is always set) but maps cleanly.
			httpresp.WriteError(w, http.StatusBadRequest, "approver is required")
		case errors.Is(err, qaisuggest.ErrAnswerNotFound):
			httpresp.WriteError(w, http.StatusNotFound, "ai-suggested answer not found")
		default:
			httperr.WriteInternal(w, r, "questionnaires", err)
		}
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, approved)
}

type aiApproveRequest struct {
	AnswerID    string `json:"answer_id"`
	Narrative   string `json:"narrative"`
	AnswerValue string `json:"answer_value"`
}

// tenantCred resolves the tenant context + the authenticated credential. Both
// must be present for the role-gated AI routes. Mirrors
// oscalcomponents.tenantCredContext.
func (h *Handler) tenantCred(r *http.Request) (context.Context, credstore.Credential, bool) {
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		return nil, credstore.Credential{}, false
	}
	c, found := authctx.CredentialFromContext(r.Context())
	if !found || c.TenantID == "" {
		return nil, credstore.Credential{}, false
	}
	return r.Context(), c, true
}
