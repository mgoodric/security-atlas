// Package decisions serves the slice-055 HTTP API for the Decision Log
// (canvas Â§6.7). Routes (registered onto the platform root router by
// internal/api/httpserver.go):
//
//	POST   /v1/decisions                              create (AC-1)
//	GET    /v1/decisions                              list (?status= &revisit_due_within_days=) (AC-2)
//	GET    /v1/decisions/overdue                      overdue calendar surface (AC-6)
//	GET    /v1/decisions/{id}                         get one + all linkage (AC-2)
//	GET    /v1/decisions/{id}/audit-log               per-decision audit trail (AC-3 read)
//	PATCH  /v1/decisions/{id}                         update mutable fields (AC-3)
//	POST   /v1/decisions/{id}/supersede               supersession workflow (AC-4)
//	POST   /v1/decisions/{id}/links/{kind}            add a linkage (AC-5)
//	DELETE /v1/decisions/{id}/links/{kind}/{targetID} remove a linkage (AC-5)
//
// `{kind}` is one of risks | controls | exceptions | scope_predicates.
//
// All handlers run with the tenant set by upstream auth middleware (see
// internal/api/authctx + internal/api/tenancymw). The store opens its own
// transaction per call and applies the tenant GUC.
//
// Cross-tenant linkage (AC-9): a link whose target does not resolve in the
// caller's tenant returns 404 -- existence-leak prevention. The failed
// attempt is recorded in decisions_audit by the store.
//
// AI-assist boundary: there is no AI auto-creation path. `decision_maker`
// is a required, human-set field on every create -- the platform never
// fabricates a Decision. AI may draft narrative / suggest constraints tags
// upstream, but a human owns the record (CLAUDE.md AI-assist boundary;
// canvas Â§6.7).
package decisions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/decision"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// MaxRevisitWindowDays caps the ?revisit_due_within_days filter so a
// runaway request doesn't drag an unbounded window. 3650 days (~10y) is a
// generous ceiling.
const MaxRevisitWindowDays = 3650

// Handler bundles the slice-055 routes over a single decision.Store.
type Handler struct {
	store *decision.Store
}

// New constructs a Handler.
func New(store *decision.Store) *Handler { return &Handler{store: store} }

// ----- wire shapes -----

type createReq struct {
	Title         string     `json:"title"`
	Narrative     string     `json:"narrative"`
	Constraints   []string   `json:"constraints"`
	Tradeoffs     string     `json:"tradeoffs"`
	DecisionMaker string     `json:"decision_maker"`
	DecidedAt     *time.Time `json:"decided_at"`
	RevisitBy     *time.Time `json:"revisit_by"`
}

type updateReq struct {
	Title         *string    `json:"title"`
	Narrative     *string    `json:"narrative"`
	Constraints   *[]string  `json:"constraints"`
	Tradeoffs     *string    `json:"tradeoffs"`
	DecisionMaker *string    `json:"decision_maker"`
	DecidedAt     *time.Time `json:"decided_at"`
	RevisitBy     *time.Time `json:"revisit_by"`
	// ClearRevisitBy, when true, sets revisit_by to NULL. Distinct from
	// omitting revisit_by entirely (which leaves it unchanged).
	ClearRevisitBy bool    `json:"clear_revisit_by"`
	Status         *string `json:"status"`
	// AuditNarrativeOptOut, when non-nil, flips the OSCAL-narrative
	// opt-out flag.
	AuditNarrativeOptOut *bool `json:"audit_narrative_opt_out"`
}

type supersedeReq struct {
	SupersededBy string `json:"superseded_by"`
}

type linkReq struct {
	TargetID string `json:"target_id"`
}

type linkWire struct {
	Kind      string    `json:"kind"`
	TargetID  string    `json:"target_id"`
	CreatedAt time.Time `json:"created_at"`
}

type linkageWire struct {
	Risks           []linkWire `json:"risks"`
	Controls        []linkWire `json:"controls"`
	Exceptions      []linkWire `json:"exceptions"`
	ScopePredicates []linkWire `json:"scope_predicates"`
}

type decisionWire struct {
	ID                   string     `json:"id"`
	DecisionID           string     `json:"decision_id"`
	Title                string     `json:"title"`
	Narrative            string     `json:"narrative"`
	Constraints          []string   `json:"constraints"`
	Tradeoffs            string     `json:"tradeoffs"`
	DecisionMaker        string     `json:"decision_maker"`
	DecidedAt            time.Time  `json:"decided_at"`
	RevisitBy            *time.Time `json:"revisit_by,omitempty"`
	Status               string     `json:"status"`
	SupersededBy         *string    `json:"superseded_by,omitempty"`
	AuditNarrativeOptOut bool       `json:"audit_narrative_opt_out"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

// CreateDecision handles POST /v1/decisions (AC-1).
func (h *Handler) CreateDecision(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := h.tenantCredContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	var req createReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	// AI-assist boundary: decision_maker is human-required. There is no
	// path that creates a Decision without it.
	if strings.TrimSpace(req.DecisionMaker) == "" {
		writeError(w, http.StatusBadRequest, "decision_maker is required")
		return
	}
	if req.DecidedAt == nil {
		writeError(w, http.StatusBadRequest, "decided_at is required")
		return
	}

	in := decision.CreateInput{
		Title:         req.Title,
		Narrative:     req.Narrative,
		Constraints:   req.Constraints,
		Tradeoffs:     req.Tradeoffs,
		DecisionMaker: req.DecisionMaker,
		DecidedAt:     req.DecidedAt.UTC(),
	}
	if req.RevisitBy != nil {
		rb := req.RevisitBy.UTC()
		in.RevisitBy = &rb
	}
	created, err := h.store.Create(ctx, in)
	if err != nil {
		h.writeStoreErr(w, "create decision", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"decision": decisionWireFrom(created)})
}

// ListDecisions handles GET /v1/decisions (AC-2). Filters:
//
//	?status=active|revisited|superseded|expired
//	?revisit_due_within_days=N
func (h *Handler) ListDecisions(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := h.tenantCredContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	filter := decision.ListFilter{Status: strings.TrimSpace(r.URL.Query().Get("status"))}
	if raw := strings.TrimSpace(r.URL.Query().Get("revisit_due_within_days")); raw != "" {
		days, err := strconv.Atoi(raw)
		if err != nil || days < 0 {
			writeError(w, http.StatusBadRequest, "revisit_due_within_days must be a non-negative integer")
			return
		}
		if days > MaxRevisitWindowDays {
			writeError(w, http.StatusBadRequest, "revisit_due_within_days exceeds maximum")
			return
		}
		filter.RevisitDueWithinDays = days
	}
	rows, err := h.store.List(ctx, filter)
	if err != nil {
		h.writeStoreErr(w, "list decisions", err)
		return
	}
	out := make([]decisionWire, len(rows))
	for i, d := range rows {
		out[i] = decisionWireFrom(d)
	}
	writeJSON(w, http.StatusOK, map[string]any{"decisions": out, "count": len(out)})
}

// Overdue handles GET /v1/decisions/overdue (AC-6). Active decisions whose
// revisit_by has already passed.
func (h *Handler) Overdue(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := h.tenantCredContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	rows, err := h.store.Overdue(ctx, time.Now().UTC())
	if err != nil {
		h.writeStoreErr(w, "list overdue decisions", err)
		return
	}
	out := make([]decisionWire, len(rows))
	for i, d := range rows {
		out[i] = decisionWireFrom(d)
	}
	writeJSON(w, http.StatusOK, map[string]any{"decisions": out, "count": len(out)})
}

// GetDecision handles GET /v1/decisions/{id} (AC-2). Returns the decision
// plus all four linkage arrays in one response.
func (h *Handler) GetDecision(w http.ResponseWriter, r *http.Request) {
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
	d, lk, err := h.store.GetWithLinkage(ctx, id)
	if err != nil {
		h.writeStoreErr(w, "get decision", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"decision": decisionWireFrom(d),
		"linkage":  linkageWireFrom(lk),
	})
}

// AuditLog handles GET /v1/decisions/{id}/audit-log (AC-3 read).
func (h *Handler) AuditLog(w http.ResponseWriter, r *http.Request) {
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
	entries, err := h.store.ListAudit(ctx, id)
	if err != nil {
		h.writeStoreErr(w, "list decision audit log", err)
		return
	}
	type entryWire struct {
		ID         string    `json:"id"`
		DecisionID string    `json:"decision_id"`
		Action     string    `json:"action"`
		Actor      string    `json:"actor"`
		Detail     string    `json:"detail"`
		OccurredAt time.Time `json:"occurred_at"`
	}
	out := make([]entryWire, len(entries))
	for i, e := range entries {
		out[i] = entryWire{
			ID:         e.ID.String(),
			DecisionID: e.DecisionID.String(),
			Action:     e.Action,
			Actor:      e.Actor,
			Detail:     e.Detail,
			OccurredAt: e.OccurredAt,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out, "count": len(out)})
}

// UpdateDecision handles PATCH /v1/decisions/{id} (AC-3).
func (h *Handler) UpdateDecision(w http.ResponseWriter, r *http.Request) {
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
	var req updateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	in := decision.UpdateInput{
		Title:          req.Title,
		Narrative:      req.Narrative,
		Constraints:    req.Constraints,
		Tradeoffs:      req.Tradeoffs,
		DecisionMaker:  req.DecisionMaker,
		ClearRevisitBy: req.ClearRevisitBy,
		Status:         req.Status,
		Actor:          cred.ID,
	}
	if req.DecidedAt != nil {
		da := req.DecidedAt.UTC()
		in.DecidedAt = &da
	}
	if req.RevisitBy != nil {
		rb := req.RevisitBy.UTC()
		in.RevisitBy = &rb
	}
	updated, err := h.store.Update(ctx, id, in)
	if err != nil {
		h.writeStoreErr(w, "update decision", err)
		return
	}

	// A nil AuditNarrativeOptOut means "leave unchanged"; a non-nil value
	// is applied as a second step (its own audit row).
	if req.AuditNarrativeOptOut != nil && *req.AuditNarrativeOptOut != updated.AuditNarrativeOptOut {
		updated, err = h.store.SetAuditNarrativeOptOut(ctx, id, *req.AuditNarrativeOptOut, cred.ID)
		if err != nil {
			h.writeStoreErr(w, "set audit-narrative opt-out", err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"decision": decisionWireFrom(updated)})
}

// Supersede handles POST /v1/decisions/{id}/supersede (AC-4). The
// replacement decision named in `superseded_by` must already exist.
func (h *Handler) Supersede(w http.ResponseWriter, r *http.Request) {
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
	var req supersedeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.SupersededBy) == "" {
		writeError(w, http.StatusBadRequest, "superseded_by is required")
		return
	}
	supersededBy, err := uuid.Parse(req.SupersededBy)
	if err != nil {
		writeError(w, http.StatusBadRequest, "superseded_by must be a UUID")
		return
	}
	superseded, err := h.store.Supersede(ctx, id, supersededBy, cred.ID)
	if err != nil {
		h.writeStoreErr(w, "supersede decision", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"decision": decisionWireFrom(superseded)})
}

// AddLink handles POST /v1/decisions/{id}/links/{kind} (AC-5). Idempotent.
func (h *Handler) AddLink(w http.ResponseWriter, r *http.Request) {
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
	kind := chi.URLParam(r, "kind")
	if !decision.ValidLinkKind(kind) {
		writeError(w, http.StatusBadRequest, "kind must be one of: risks, controls, exceptions, scope_predicates")
		return
	}
	var req linkReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.TargetID) == "" {
		writeError(w, http.StatusBadRequest, "target_id is required")
		return
	}
	targetID, err := uuid.Parse(req.TargetID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "target_id must be a UUID")
		return
	}
	if err := h.store.AddLink(ctx, id, decision.LinkKind(kind), targetID, cred.ID); err != nil {
		h.writeStoreErr(w, "add link", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"linked":      true,
		"kind":        kind,
		"target_id":   targetID.String(),
		"decision_id": id.String(),
	})
}

// RemoveLink handles DELETE /v1/decisions/{id}/links/{kind}/{targetID}
// (AC-5). Idempotent.
func (h *Handler) RemoveLink(w http.ResponseWriter, r *http.Request) {
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
	kind := chi.URLParam(r, "kind")
	if !decision.ValidLinkKind(kind) {
		writeError(w, http.StatusBadRequest, "kind must be one of: risks, controls, exceptions, scope_predicates")
		return
	}
	targetID, err := uuid.Parse(chi.URLParam(r, "targetID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "targetID must be a UUID")
		return
	}
	if err := h.store.RemoveLink(ctx, id, decision.LinkKind(kind), targetID, cred.ID); err != nil {
		h.writeStoreErr(w, "remove link", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"linked":      false,
		"kind":        kind,
		"target_id":   targetID.String(),
		"decision_id": id.String(),
	})
}

// ----- helpers -----

// writeStoreErr maps decision.Store errors to HTTP status codes.
//
//	ErrNotFound / ErrCrossTenantLink -> 404 (existence-leak prevention)
//	ErrWrongState                    -> 409
//	validation errors                -> 400
//	anything else                    -> 500
func (h *Handler) writeStoreErr(w http.ResponseWriter, op string, err error) {
	switch {
	case errors.Is(err, decision.ErrNotFound):
		writeError(w, http.StatusNotFound, "decision not found")
	case errors.Is(err, decision.ErrCrossTenantLink):
		// AC-9: a cross-tenant (or absent) link target is reported as 404,
		// never as a 403 -- a 403 would confirm the entity exists.
		writeError(w, http.StatusNotFound, "link target not found")
	case errors.Is(err, decision.ErrWrongState):
		writeError(w, http.StatusConflict, "decision not in expected state for this operation")
	case errors.Is(err, decision.ErrTitleRequired),
		errors.Is(err, decision.ErrDecisionMakerRequired),
		errors.Is(err, decision.ErrDecidedAtRequired),
		errors.Is(err, decision.ErrSupersededByRequired),
		errors.Is(err, decision.ErrSelfSupersede),
		errors.Is(err, decision.ErrInvalidLinkKind):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeServerErr(w, op, err)
	}
}

// tenantCredContext returns the request context (with the tenant GUC
// already applied by slice-033's tenancy.Middleware) plus the resolved
// credential, so handlers can use cred.ID as the actor.
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

func decisionWireFrom(d decision.Decision) decisionWire {
	out := decisionWire{
		ID:                   d.ID.String(),
		DecisionID:           d.DecisionID,
		Title:                d.Title,
		Narrative:            d.Narrative,
		Constraints:          append([]string{}, d.Constraints...),
		Tradeoffs:            d.Tradeoffs,
		DecisionMaker:        d.DecisionMaker,
		DecidedAt:            d.DecidedAt,
		Status:               d.Status,
		AuditNarrativeOptOut: d.AuditNarrativeOptOut,
		CreatedAt:            d.CreatedAt,
		UpdatedAt:            d.UpdatedAt,
	}
	if d.RevisitBy != nil {
		rb := *d.RevisitBy
		out.RevisitBy = &rb
	}
	if d.SupersededBy != nil {
		s := d.SupersededBy.String()
		out.SupersededBy = &s
	}
	return out
}

func linkageWireFrom(lk decision.Linkage) linkageWire {
	return linkageWire{
		Risks:           linkSliceWire(lk.Risks),
		Controls:        linkSliceWire(lk.Controls),
		Exceptions:      linkSliceWire(lk.Exceptions),
		ScopePredicates: linkSliceWire(lk.ScopePredicates),
	}
}

func linkSliceWire(links []decision.Link) []linkWire {
	out := make([]linkWire, len(links))
	for i, l := range links {
		out[i] = linkWire{
			Kind:      string(l.Kind),
			TargetID:  l.TargetID.String(),
			CreatedAt: l.CreatedAt,
		}
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
