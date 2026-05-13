// Package policies serves the slice-022 HTTP API for the policy library.
// Routes (registered onto the platform root router by
// internal/api/httpserver.go):
//
//	POST   /v1/policies                            create draft (AC-1)
//	GET    /v1/policies                            list (optional ?status= filter)
//	GET    /v1/policies/{id}                       get one (optional ?versions=true for chain)
//	PATCH  /v1/policies/{id}/submit                draft -> under_review
//	PATCH  /v1/policies/{id}/approve               under_review -> approved (AC-4)
//	POST   /v1/policies/{id}/publish               approved -> published (AC-1 versioned row + AC-7 orphan block)
//	GET    /v1/policies/{id}/pdf                   PDF render via chromedp (AC-5)
//
// All handlers run with the tenant set by upstream auth middleware (slice
// 033 tenancy.Middleware). The store opens its own transaction per call
// and applies the tenant GUC.
//
// Approver-role gate: PATCH approve + POST publish both require
// cred.IsApprover || cred.IsAdmin. Publish is gated because it creates an
// audit-binding artifact; defense in depth.
package policies

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/policy"
	policypdf "github.com/mgoodric/security-atlas/internal/policy/pdf"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// pdfRenderTimeout caps chromedp boot + PrintToPDF for one request. Headless
// Chrome on a 1-page policy is typically <2s; 30s is generous but bounded.
const pdfRenderTimeout = 30 * time.Second

// Handler bundles the slice-022 routes over a single policy.Store.
type Handler struct {
	store *policy.Store
	// renderPDF is injectable for tests so we don't need a real Chrome
	// in unit-only servers. Production wires policypdf.Render.
	renderPDF func(ctx context.Context, doc policypdf.Doc) ([]byte, error)
}

// New constructs a Handler wired to the production PDF renderer.
func New(store *policy.Store) *Handler {
	return &Handler{store: store, renderPDF: policypdf.Render}
}

// WithRenderer overrides the PDF render function. Tests use this to inject
// a fake; production never calls it.
func (h *Handler) WithRenderer(fn func(ctx context.Context, doc policypdf.Doc) ([]byte, error)) *Handler {
	h.renderPDF = fn
	return h
}

// ----- wire shapes -----

type createReq struct {
	Title                       string      `json:"title"`
	Version                     string      `json:"version"`
	BodyMd                      string      `json:"body_md"`
	OwnerRole                   string      `json:"owner_role"`
	ApproverRole                string      `json:"approver_role"`
	LinkedControlIDs            []string    `json:"linked_control_ids"`
	AcknowledgmentRequiredRoles []string    `json:"acknowledgment_required_roles"`
	SourceAttribution           string      `json:"source_attribution,omitempty"`
}

type publishReq struct {
	NewVersion    string  `json:"new_version"`
	EffectiveDate *string `json:"effective_date,omitempty"` // YYYY-MM-DD
}

type policyWire struct {
	ID                          string     `json:"id"`
	PredecessorID               *string    `json:"predecessor_id,omitempty"`
	Title                       string     `json:"title"`
	Version                     string     `json:"version"`
	EffectiveDate               *string    `json:"effective_date,omitempty"`
	BodyMd                      string     `json:"body_md"`
	OwnerRole                   string     `json:"owner_role"`
	ApproverRole                string     `json:"approver_role"`
	LinkedControlIDs            []string   `json:"linked_control_ids"`
	AcknowledgmentRequiredRoles []string   `json:"acknowledgment_required_roles"`
	Status                      string     `json:"status"`
	SourceAttribution           string     `json:"source_attribution"`
	CreatedBy                   string     `json:"created_by"`
	SubmittedAt                 *time.Time `json:"submitted_at,omitempty"`
	SubmittedBy                 *string    `json:"submitted_by,omitempty"`
	ApprovedAt                  *time.Time `json:"approved_at,omitempty"`
	ApprovedBy                  *string    `json:"approved_by,omitempty"`
	PublishedAt                 *time.Time `json:"published_at,omitempty"`
	PublishedBy                 *string    `json:"published_by,omitempty"`
	SupersededAt                *time.Time `json:"superseded_at,omitempty"`
	CreatedAt                   time.Time  `json:"created_at"`
	UpdatedAt                   time.Time  `json:"updated_at"`
	Warnings                    []string   `json:"warnings,omitempty"`
}

// CreatePolicy handles POST /v1/policies (AC-1 part A).
func (h *Handler) CreatePolicy(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.tenantCredContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	var req createReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	linkedIDs, err := parseUUIDs(req.LinkedControlIDs)
	if err != nil {
		writeError(w, http.StatusBadRequest, "linked_control_ids contains invalid UUID: "+err.Error())
		return
	}
	ackRoles := req.AcknowledgmentRequiredRoles
	if ackRoles == nil {
		ackRoles = []string{}
	}
	created, err := h.store.Create(ctx, policy.CreateInput{
		Title:                       req.Title,
		Version:                     req.Version,
		BodyMd:                      req.BodyMd,
		OwnerRole:                   req.OwnerRole,
		ApproverRole:                req.ApproverRole,
		LinkedControlIDs:            linkedIDs,
		AcknowledgmentRequiredRoles: ackRoles,
		SourceAttribution:           req.SourceAttribution,
		CreatedBy:                   cred.ID,
	})
	if err != nil {
		h.writeCreateErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"policy": wireFromPolicy(created)})
}

// ListPolicies handles GET /v1/policies?status=...
func (h *Handler) ListPolicies(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := h.tenantCredContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	filter := policy.ListFilter{Status: strings.TrimSpace(r.URL.Query().Get("status"))}
	rows, err := h.store.List(ctx, filter)
	if err != nil {
		writeServerErr(w, "list policies", err)
		return
	}
	out := make([]policyWire, len(rows))
	for i, p := range rows {
		out[i] = wireFromPolicy(p)
	}
	writeJSON(w, http.StatusOK, map[string]any{"policies": out, "count": len(out)})
}

// GetPolicy handles GET /v1/policies/{id}?versions=true.
func (h *Handler) GetPolicy(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := h.tenantCredContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	if r.URL.Query().Get("versions") == "true" {
		chain, err := h.store.VersionChain(ctx, id)
		if err != nil {
			writeServerErr(w, "version chain", err)
			return
		}
		if len(chain) == 0 {
			writeError(w, http.StatusNotFound, "policy not found")
			return
		}
		out := make([]policyWire, len(chain))
		for i, p := range chain {
			out[i] = wireFromPolicy(p)
		}
		writeJSON(w, http.StatusOK, map[string]any{"versions": out, "count": len(out)})
		return
	}
	p, err := h.store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, policy.ErrNotFound) {
			writeError(w, http.StatusNotFound, "policy not found")
			return
		}
		writeServerErr(w, "get policy", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"policy": wireFromPolicy(p)})
}

// Submit handles PATCH /v1/policies/{id}/submit (draft -> under_review).
func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.tenantCredContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	updated, err := h.store.SubmitForReview(ctx, id, cred.ID)
	if err != nil {
		h.writeTransitionErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"policy": wireFromPolicy(updated)})
}

// Approve handles PATCH /v1/policies/{id}/approve (AC-4).
func (h *Handler) Approve(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.tenantCredContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if !cred.IsApprover && !cred.IsAdmin {
		writeError(w, http.StatusForbidden, "approver role required")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	approved, err := h.store.Approve(ctx, id, cred.ID)
	if err != nil {
		h.writeTransitionErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"policy": wireFromPolicy(approved)})
}

// Publish handles POST /v1/policies/{id}/publish (AC-1 versioned row,
// AC-7 orphan block). Gated by IsApprover (defense in depth — publish is
// audit-binding).
func (h *Handler) Publish(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.tenantCredContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if !cred.IsApprover && !cred.IsAdmin {
		writeError(w, http.StatusForbidden, "approver role required for publish")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	var req publishReq
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}
	if req.NewVersion == "" {
		writeError(w, http.StatusBadRequest, "new_version is required")
		return
	}
	var effective *time.Time
	if req.EffectiveDate != nil && *req.EffectiveDate != "" {
		t, err := time.Parse("2006-01-02", *req.EffectiveDate)
		if err != nil {
			writeError(w, http.StatusBadRequest, "effective_date must be YYYY-MM-DD")
			return
		}
		effective = &t
	}
	published, err := h.store.Publish(ctx, id, policy.PublishInput{
		NewVersion:    req.NewVersion,
		EffectiveDate: effective,
		PublishedBy:   cred.ID,
	})
	if err != nil {
		h.writePublishErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"policy": wireFromPolicy(published)})
}

// PDF handles GET /v1/policies/{id}/pdf (AC-5). Returns
// application/pdf. The render path is real (not a stub); the integration
// test asserts the leading `%PDF-` magic bytes.
func (h *Handler) PDF(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := h.tenantCredContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	p, err := h.store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, policy.ErrNotFound) {
			writeError(w, http.StatusNotFound, "policy not found")
			return
		}
		writeServerErr(w, "get policy", err)
		return
	}
	doc := policypdf.Doc{
		Title:        p.Title,
		Version:      p.Version,
		OwnerRole:    p.OwnerRole,
		ApproverRole: p.ApproverRole,
		Status:       p.Status,
		BodyMd:       p.BodyMd,
	}
	if p.EffectiveDate != nil {
		doc.EffectiveDate = p.EffectiveDate.Format("2006-01-02")
	}
	renderCtx, cancel := context.WithTimeout(r.Context(), pdfRenderTimeout)
	defer cancel()
	pdfBytes, err := h.renderPDF(renderCtx, doc)
	if err != nil {
		if errors.Is(err, policypdf.ErrChromeUnavailable) {
			writeError(w, http.StatusServiceUnavailable, "pdf renderer unavailable: chrome not installed")
			return
		}
		writeServerErr(w, "render pdf", err)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="policy-%s.pdf"`, p.ID.String()))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pdfBytes)
}

// ----- helpers -----

func (h *Handler) writeCreateErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, policy.ErrTitleRequired),
		errors.Is(err, policy.ErrVersionRequired),
		errors.Is(err, policy.ErrBodyRequired),
		errors.Is(err, policy.ErrOwnerRoleRequired),
		errors.Is(err, policy.ErrApproverRoleRequired),
		errors.Is(err, policy.ErrCreatedByRequired):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeServerErr(w, "create policy", err)
	}
}

func (h *Handler) writeTransitionErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, policy.ErrNotFound):
		writeError(w, http.StatusNotFound, "policy not found")
	case errors.Is(err, policy.ErrWrongState):
		writeError(w, http.StatusConflict, "policy not in expected state for this transition")
	default:
		writeServerErr(w, "transition", err)
	}
}

func (h *Handler) writePublishErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, policy.ErrOrphanPublish):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, policy.ErrNotFound):
		writeError(w, http.StatusNotFound, "policy not found")
	case errors.Is(err, policy.ErrWrongState):
		writeError(w, http.StatusConflict, "policy not in expected state for publish")
	case errors.Is(err, policy.ErrInvalidVersion):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeServerErr(w, "publish policy", err)
	}
}

func (h *Handler) tenantCredContext(r *http.Request) (context.Context, credstore.Credential, bool) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || cred.TenantID == "" {
		return nil, credstore.Credential{}, false
	}
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		return nil, credstore.Credential{}, false
	}
	return r.Context(), cred, true
}

func wireFromPolicy(p policy.Policy) policyWire {
	out := policyWire{
		ID:                          p.ID.String(),
		Title:                       p.Title,
		Version:                     p.Version,
		BodyMd:                      p.BodyMd,
		OwnerRole:                   p.OwnerRole,
		ApproverRole:                p.ApproverRole,
		LinkedControlIDs:            uuidsToStrings(p.LinkedControlIDs),
		AcknowledgmentRequiredRoles: append([]string{}, p.AcknowledgmentRequiredRoles...),
		Status:                      p.Status,
		SourceAttribution:           p.SourceAttribution,
		CreatedBy:                   p.CreatedBy,
		SubmittedAt:                 p.SubmittedAt,
		SubmittedBy:                 p.SubmittedBy,
		ApprovedAt:                  p.ApprovedAt,
		ApprovedBy:                  p.ApprovedBy,
		PublishedAt:                 p.PublishedAt,
		PublishedBy:                 p.PublishedBy,
		SupersededAt:                p.SupersededAt,
		CreatedAt:                   p.CreatedAt,
		UpdatedAt:                   p.UpdatedAt,
	}
	if p.PredecessorID != nil {
		s := p.PredecessorID.String()
		out.PredecessorID = &s
	}
	if p.EffectiveDate != nil {
		s := p.EffectiveDate.Format("2006-01-02")
		out.EffectiveDate = &s
	}
	// AC-7: surface the orphan_policy warning on every read response when
	// the row has zero linked controls.
	if p.IsOrphan() {
		out.Warnings = append(out.Warnings, policy.WarningOrphanPolicy)
	}
	return out
}

func parseUUIDs(strs []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(strs))
	for _, s := range strs {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		u, err := uuid.Parse(s)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, nil
}

func uuidsToStrings(us []uuid.UUID) []string {
	out := make([]string, len(us))
	for i, u := range us {
		out[i] = u.String()
	}
	return out
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
