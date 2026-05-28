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
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/policy"
	policypdf "github.com/mgoodric/security-atlas/internal/policy/pdf"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// pdfRenderTimeout caps chromedp boot + PrintToPDF for one request. Headless
// Chrome on a 1-page policy is typically <2s; 30s is generous but bounded.
const pdfRenderTimeout = 30 * time.Second

// Handler bundles the slice-022 routes over a single policy.Store.
//
// Slice 107: when `?include=ack_rate` is set on GET /v1/policies, the
// ListPolicies handler runs ONE joined query (CountFreshAcks-style
// numerator + CountRequiredRoleUsers-style denominator) against the
// pool. That path requires a transaction with the `app.current_tenant`
// GUC set via `tenancy.ApplyTenant`. The handler holds a *pgxpool.Pool
// for that case; the existing non-`?include=` paths continue through
// the pre-bound store. Mirrors the slice 104 anchors NewWithPool shape.
type Handler struct {
	store *policy.Store
	pool  *pgxpool.Pool
	// renderPDF is injectable for tests so we don't need a real Chrome
	// in unit-only servers. Production wires policypdf.Render.
	renderPDF func(ctx context.Context, doc policypdf.Doc) ([]byte, error)
}

// New constructs a Handler wired to the production PDF renderer.
//
// Backwards-compatible: callers that do not need the slice-107 joined
// ack-rate path can still pass only the store (pool=nil); the
// `?include=ack_rate` query parameter will then respond 500 with a
// clear "pool not wired" error. Production wiring
// (internal/api/httpserver.go) uses NewWithPool.
func New(store *policy.Store) *Handler {
	return &Handler{store: store, renderPDF: policypdf.Render}
}

// NewWithPool is the slice-107 constructor. The pool is used ONLY for
// the `?include=ack_rate` path's tenant-GUC-bearing transaction; every
// other read continues through the store.
func NewWithPool(store *policy.Store, pool *pgxpool.Pool) *Handler {
	return &Handler{store: store, pool: pool, renderPDF: policypdf.Render}
}

// WithRenderer overrides the PDF render function. Tests use this to inject
// a fake; production never calls it.
func (h *Handler) WithRenderer(fn func(ctx context.Context, doc policypdf.Doc) ([]byte, error)) *Handler {
	h.renderPDF = fn
	return h
}

// ----- wire shapes -----

type createReq struct {
	Title                       string   `json:"title"`
	Version                     string   `json:"version"`
	BodyMd                      string   `json:"body_md"`
	OwnerRole                   string   `json:"owner_role"`
	ApproverRole                string   `json:"approver_role"`
	LinkedControlIDs            []string `json:"linked_control_ids"`
	AcknowledgmentRequiredRoles []string `json:"acknowledgment_required_roles"`
	SourceAttribution           string   `json:"source_attribution,omitempty"`
}

type publishReq struct {
	NewVersion    string  `json:"new_version"`
	EffectiveDate *string `json:"effective_date,omitempty"` // YYYY-MM-DD
}

// policyAckRateCellWire is the per-policy ack-rate rollup the
// `?include=ack_rate` extension attaches to each row. The field names
// mirror the slice-023 `rateResponse` (internal/api/policyacks/handlers.go)
// minus the `window_seconds` field — the list view doesn't need it (the
// per-policy detail page surfaces it via /v1/policies/{id}/acknowledgment-rate).
type policyAckRateCellWire struct {
	Numerator   int64    `json:"numerator"`
	Denominator int64    `json:"denominator"`
	Percent     *float64 `json:"percent"`
}

// policyWithAckRateWire is the JSON shape returned when ?include=ack_rate
// is set on GET /v1/policies. `AckRate` is nil for non-published rows
// (the SQL CASE returns NULL on those branches).
type policyWithAckRateWire struct {
	policyWire
	AckRate *policyAckRateCellWire `json:"ack_rate"`
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
		h.writeCreateErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"policy": wireFromPolicy(created)})
}

// ListPolicies handles GET /v1/policies?status=...
//
// Slice 107 — `?include=ack_rate` (additive): when set, the response
// shape becomes `{ policies: [{ ...policyWire, ack_rate: cell | null }],
// count }`. The ack_rate column is computed by a single SQL query with
// correlated subqueries — there is NO per-policy loop calling
// AckStore.Rate (slice 107 P0 anti-criterion ISC-A1). The math is
// identical to the per-policy GET /v1/policies/{id}/acknowledgment-rate
// path: both call into the same SQL predicates that
// CountFreshAcksForVersion + CountRequiredRoleUsersForVersion express
// (slice 107 ISC-A4). Non-published rows return `ack_rate: null`
// (CASE WHEN status='published' in SQL produces NULL otherwise).
//
// Unknown `include` values are silently ignored (slice 094 precedent —
// additive query params are not errors).
func (h *Handler) ListPolicies(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := h.tenantCredContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	statusFilter := strings.TrimSpace(r.URL.Query().Get("status"))
	if includesAckRate(r) {
		out, err := h.listPoliciesWithAckRate(ctx, statusFilter)
		if err != nil {
			writeServerErr(w, r, "list policies (with ack_rate)", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"policies": out, "count": len(out)})
		return
	}
	filter := policy.ListFilter{Status: statusFilter}
	rows, err := h.store.List(ctx, filter)
	if err != nil {
		writeServerErr(w, r, "list policies", err)
		return
	}
	out := make([]policyWire, len(rows))
	for i, p := range rows {
		out[i] = wireFromPolicy(p)
	}
	writeJSON(w, http.StatusOK, map[string]any{"policies": out, "count": len(out)})
}

// listPoliciesWithAckRate is the slice-107 `?include=ack_rate` path.
// One SQL round-trip — the handler MUST NOT call AckStore.Rate in a
// loop (anti-criterion ISC-A1).
func (h *Handler) listPoliciesWithAckRate(ctx context.Context, statusFilter string) ([]policyWithAckRateWire, error) {
	if h.pool == nil {
		return nil, fmt.Errorf("policies: pool not wired; ?include=ack_rate requires NewWithPool")
	}
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		return nil, err
	}
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("policies: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return nil, err
	}
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return nil, fmt.Errorf("policies: parse tenant id: %w", err)
	}
	cutoff := time.Now().UTC().Add(-policy.AcknowledgmentFreshness)
	q := dbx.New(tx)
	rows, err := q.ListPoliciesWithAckRate(ctx, dbx.ListPoliciesWithAckRateParams{
		TenantID:        pgtype.UUID{Bytes: tenantID, Valid: true},
		FreshnessCutoff: pgtype.Timestamptz{Time: cutoff, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("policies: list with ack_rate: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("policies: commit tx: %w", err)
	}
	out := make([]policyWithAckRateWire, 0, len(rows))
	for _, r := range rows {
		if statusFilter != "" && r.Status != statusFilter {
			continue
		}
		out = append(out, wireFromAckRateRow(r))
	}
	return out, nil
}

// includesAckRate returns true when the request asked for the joined
// ack-rate column via `?include=ack_rate`. Mirrors slice 104's
// `includesState` — accepts plain `ack_rate`, CSV
// (`?include=ack_rate,state`), and repeated query params
// (`?include=ack_rate&include=other`). Trims whitespace. Unknown tokens
// are silently ignored — they are NOT errors (slice 094 calendar
// precedent: additive query params don't break the caller).
func includesAckRate(r *http.Request) bool {
	for _, v := range r.URL.Query()["include"] {
		for _, tok := range strings.Split(v, ",") {
			if strings.TrimSpace(tok) == "ack_rate" {
				return true
			}
		}
	}
	return false
}

// wireFromAckRateRow converts the sqlc-generated joined row into the
// wire shape. The ack-rate cell is nil when the SQL CASE returned NULL
// (status != 'published'); otherwise the numerator + denominator are
// populated and `percent` is computed (or nil if denominator is zero,
// matching the slice-023 rateResponse semantic).
func wireFromAckRateRow(r dbx.ListPoliciesWithAckRateRow) policyWithAckRateWire {
	base := wireFromAckRateBaseColumns(r)
	out := policyWithAckRateWire{policyWire: base}
	// Both columns are NULL together — the slice-159 CTE-with-LEFT-JOIN
	// restructure ensures `ack_cells` only carries rows whose policy is
	// `status = 'published'`; the LEFT JOIN to the outer policy list
	// produces NULL for both columns on non-published policies. sqlc
	// v1.31.1 emits `*int64` (nil = NULL) under
	// `emit_pointers_for_null_types: true`. The wire shape
	// (`ack_rate: null` vs populated cell) is unchanged from slice 107.
	if r.AckDenominator != nil && r.AckNumerator != nil {
		cell := &policyAckRateCellWire{
			Numerator:   *r.AckNumerator,
			Denominator: *r.AckDenominator,
		}
		if cell.Denominator > 0 {
			pct := (float64(cell.Numerator) / float64(cell.Denominator)) * 100.0
			cell.Percent = &pct
		}
		out.AckRate = cell
	}
	return out
}

// wireFromAckRateBaseColumns builds the policyWire portion of the
// joined row. Mirrors wireFromPolicy on the dbx-row column set so the
// joined shape stays byte-compatible with the omitted-include shape
// (anti-criterion ISC-A2: additive only).
func wireFromAckRateBaseColumns(r dbx.ListPoliciesWithAckRateRow) policyWire {
	id := uuid.UUID(r.ID.Bytes).String()
	out := policyWire{
		ID:                          id,
		Title:                       r.Title,
		Version:                     r.Version,
		BodyMd:                      r.BodyMd,
		OwnerRole:                   r.OwnerRole,
		ApproverRole:                r.ApproverRole,
		LinkedControlIDs:            pgUUIDsToStrings(r.LinkedControlIds),
		AcknowledgmentRequiredRoles: append([]string{}, r.AcknowledgmentRequiredRoles...),
		Status:                      r.Status,
		SourceAttribution:           r.SourceAttribution,
		CreatedBy:                   r.CreatedBy,
		SubmittedAt:                 timestamptzPtr(r.SubmittedAt),
		SubmittedBy:                 r.SubmittedBy,
		ApprovedAt:                  timestamptzPtr(r.ApprovedAt),
		ApprovedBy:                  r.ApprovedBy,
		PublishedAt:                 timestamptzPtr(r.PublishedAt),
		PublishedBy:                 r.PublishedBy,
		SupersededAt:                timestamptzPtr(r.SupersededAt),
		CreatedAt:                   r.CreatedAt.Time,
		UpdatedAt:                   r.UpdatedAt.Time,
	}
	if r.PredecessorID.Valid {
		s := uuid.UUID(r.PredecessorID.Bytes).String()
		out.PredecessorID = &s
	}
	if r.EffectiveDate.Valid {
		s := r.EffectiveDate.Time.Format("2006-01-02")
		out.EffectiveDate = &s
	}
	// AC-7: surface the orphan_policy warning when the row has zero
	// linked controls (parity with wireFromPolicy).
	if len(r.LinkedControlIds) == 0 {
		out.Warnings = append(out.Warnings, policy.WarningOrphanPolicy)
	}
	return out
}

func pgUUIDsToStrings(us []pgtype.UUID) []string {
	out := make([]string, 0, len(us))
	for _, u := range us {
		if !u.Valid {
			continue
		}
		out = append(out, uuid.UUID(u.Bytes).String())
	}
	return out
}

func timestamptzPtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
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
			writeServerErr(w, r, "version chain", err)
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
		writeServerErr(w, r, "get policy", err)
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
		h.writeTransitionErr(w, r, err)
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
		h.writeTransitionErr(w, r, err)
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
		h.writePublishErr(w, r, err)
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
		writeServerErr(w, r, "get policy", err)
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
		writeServerErr(w, r, "render pdf", err)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="policy-%s.pdf"`, p.ID.String()))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pdfBytes)
}

// ----- helpers -----

func (h *Handler) writeCreateErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, policy.ErrTitleRequired),
		errors.Is(err, policy.ErrVersionRequired),
		errors.Is(err, policy.ErrBodyRequired),
		errors.Is(err, policy.ErrOwnerRoleRequired),
		errors.Is(err, policy.ErrApproverRoleRequired),
		errors.Is(err, policy.ErrCreatedByRequired):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeServerErr(w, r, "create policy", err)
	}
}

func (h *Handler) writeTransitionErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, policy.ErrNotFound):
		writeError(w, http.StatusNotFound, "policy not found")
	case errors.Is(err, policy.ErrWrongState):
		writeError(w, http.StatusConflict, "policy not in expected state for this transition")
	default:
		writeServerErr(w, r, "transition", err)
	}
}

func (h *Handler) writePublishErr(w http.ResponseWriter, r *http.Request, err error) {
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
		writeServerErr(w, r, "publish policy", err)
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

func writeServerErr(w http.ResponseWriter, r *http.Request, op string, err error) {
	httperr.WriteInternal(w, r, op, err)
}
