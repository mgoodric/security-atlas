// Package exceptions serves the slice-021 HTTP API for the exception/waiver
// workflow. Routes (registered onto the platform root router by
// internal/api/httpserver.go):
//
//	POST   /v1/exceptions                       create (AC-1)
//	GET    /v1/exceptions                       list (optional ?status=&control_id= filters)
//	GET    /v1/exceptions/expiring              calendar surface (AC-6)
//	GET    /v1/exceptions/{id}                  get one
//	GET    /v1/exceptions/{id}/audit-log        per-row audit trail (AC-7)
//	PATCH  /v1/exceptions/{id}/approve          requested -> approved (AC-3)
//	PATCH  /v1/exceptions/{id}/deny             requested -> denied
//	PATCH  /v1/exceptions/{id}/activate         approved -> active (AC-4 enable)
//
// All handlers run with the tenant set by upstream auth middleware (see
// internal/api/authctx). The store opens its own transaction per call and
// applies the tenant GUC.
//
// AC-3 + segregation of duties: approve/deny/activate require IsApprover
// (with IsAdmin implying IsApprover). approve/deny also reject when the
// caller's credential id equals the row's requested_by (defense in depth
// against same-user filing + adjudicating).
package exceptions

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
	"github.com/mgoodric/security-atlas/internal/exception"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// DefaultExpiringWindow is the default `within` for GET /v1/exceptions/expiring
// when the query parameter is omitted. Canvas §6.3 + AC-6 call out 30 days
// as the dashboard panel surface.
const DefaultExpiringWindow = 30 * 24 * time.Hour

// MaxExpiringWindow caps `within` so a runaway request doesn't drag the
// entire active set through the response. 365d matches the per-row
// max-lifetime cap.
const MaxExpiringWindow = 365 * 24 * time.Hour

// Handler bundles the slice-021 routes over a single exception.Store.
type Handler struct {
	store *exception.Store
}

// New constructs a Handler.
func New(store *exception.Store) *Handler { return &Handler{store: store} }

// ----- wire shapes -----

type createReq struct {
	ControlID            string          `json:"control_id"`
	ScopeCellPredicate   json.RawMessage `json:"scope_cell_predicate"`
	Justification        string          `json:"justification"`
	CompensatingControls []string        `json:"compensating_controls"`
	ExpiresAt            *time.Time      `json:"expires_at"`
}

type denyReq struct {
	Reason string `json:"reason"`
}

type activateReq struct {
	EffectiveFrom *time.Time `json:"effective_from"`
}

type exceptionWire struct {
	ID                   string          `json:"id"`
	ControlID            string          `json:"control_id"`
	ScopeCellPredicate   json.RawMessage `json:"scope_cell_predicate"`
	Justification        string          `json:"justification"`
	CompensatingControls []string        `json:"compensating_controls"`
	RequestedBy          string          `json:"requested_by"`
	RequestedAt          time.Time       `json:"requested_at"`
	ApprovedBy           *string         `json:"approved_by,omitempty"`
	ApprovedAt           *time.Time      `json:"approved_at,omitempty"`
	DeniedBy             *string         `json:"denied_by,omitempty"`
	DeniedAt             *time.Time      `json:"denied_at,omitempty"`
	ActivatedBy          *string         `json:"activated_by,omitempty"`
	ActivatedAt          *time.Time      `json:"activated_at,omitempty"`
	EffectiveFrom        *time.Time      `json:"effective_from,omitempty"`
	ExpiresAt            time.Time       `json:"expires_at"`
	ExpiredAt            *time.Time      `json:"expired_at,omitempty"`
	Status               string          `json:"status"`
	CreatedAt            time.Time       `json:"created_at"`
	UpdatedAt            time.Time       `json:"updated_at"`
}

// CreateException handles POST /v1/exceptions (AC-1).
func (h *Handler) CreateException(w http.ResponseWriter, r *http.Request) {
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
	if req.ControlID == "" {
		writeError(w, http.StatusBadRequest, "control_id is required")
		return
	}
	controlID, err := uuid.Parse(req.ControlID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "control_id must be a UUID")
		return
	}
	if strings.TrimSpace(req.Justification) == "" {
		writeError(w, http.StatusBadRequest, "justification is required")
		return
	}
	if req.ExpiresAt == nil {
		writeError(w, http.StatusBadRequest, "expires_at is required")
		return
	}

	in := exception.CreateInput{
		ControlID:            controlID,
		ScopeCellPredicate:   []byte(req.ScopeCellPredicate),
		Justification:        req.Justification,
		CompensatingControls: req.CompensatingControls,
		RequestedBy:          cred.ID,
		ExpiresAt:            req.ExpiresAt.UTC(),
	}
	created, err := h.store.Create(ctx, in)
	if err != nil {
		h.writeCreateErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"exception": exceptionWireFrom(created)})
}

// ListExceptions handles GET /v1/exceptions (filterable by ?status=).
func (h *Handler) ListExceptions(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := h.tenantCredContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	filter := exception.ListFilter{Status: strings.TrimSpace(r.URL.Query().Get("status"))}
	rows, err := h.store.List(ctx, filter)
	if err != nil {
		writeServerErr(w, "list exceptions", err)
		return
	}
	out := make([]exceptionWire, len(rows))
	for i, e := range rows {
		out[i] = exceptionWireFrom(e)
	}
	writeJSON(w, http.StatusOK, map[string]any{"exceptions": out, "count": len(out)})
}

// GetException handles GET /v1/exceptions/{id}.
func (h *Handler) GetException(w http.ResponseWriter, r *http.Request) {
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
	e, err := h.store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, exception.ErrNotFound) {
			writeError(w, http.StatusNotFound, "exception not found")
			return
		}
		writeServerErr(w, "get exception", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"exception": exceptionWireFrom(e)})
}

// Expiring handles GET /v1/exceptions/expiring?within=30d (AC-6).
//
// `within` accepts a small bespoke duration format: integer suffixed by `d`
// (days), `h` (hours), or `m` (minutes). Default 30d. Anything else is a
// 400. Go's time.ParseDuration does not accept `d` so the parser is
// hand-rolled to keep the URL surface stable.
func (h *Handler) Expiring(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := h.tenantCredContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	withinStr := strings.TrimSpace(r.URL.Query().Get("within"))
	within, err := parseWithin(withinStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "within must look like '30d', '12h', or '45m'")
		return
	}
	if within > MaxExpiringWindow {
		writeError(w, http.StatusBadRequest, "within exceeds maximum (365d)")
		return
	}
	rows, err := h.store.Expiring(ctx, time.Now().UTC(), within)
	if err != nil {
		writeServerErr(w, "list expiring", err)
		return
	}
	out := make([]exceptionWire, len(rows))
	for i, e := range rows {
		out[i] = exceptionWireFrom(e)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"exceptions": out,
		"count":      len(out),
		"within":     within.String(),
	})
}

// AuditLog handles GET /v1/exceptions/{id}/audit-log (AC-7 read).
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
	entries, err := h.store.ListAuditLog(ctx, id)
	if err != nil {
		writeServerErr(w, "list audit log", err)
		return
	}
	type entryWire struct {
		ID          string    `json:"id"`
		Action      string    `json:"action"`
		Actor       string    `json:"actor"`
		FromState   *string   `json:"from_state,omitempty"`
		ToState     string    `json:"to_state"`
		Reason      string    `json:"reason"`
		OccurredAt  time.Time `json:"occurred_at"`
		ExceptionID string    `json:"exception_id"`
	}
	out := make([]entryWire, len(entries))
	for i, e := range entries {
		w := entryWire{
			ID:          e.ID.String(),
			Action:      e.Action,
			Actor:       e.Actor,
			ToState:     e.ToState,
			Reason:      e.Reason,
			OccurredAt:  e.OccurredAt,
			ExceptionID: e.ExceptionID.String(),
		}
		if e.FromState != nil {
			v := *e.FromState
			w.FromState = &v
		}
		out[i] = w
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out, "count": len(out)})
}

// Approve handles PATCH /v1/exceptions/{id}/approve (AC-3).
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
	writeJSON(w, http.StatusOK, map[string]any{"exception": exceptionWireFrom(approved)})
}

// Deny handles PATCH /v1/exceptions/{id}/deny (AC-3 terminal path).
func (h *Handler) Deny(w http.ResponseWriter, r *http.Request) {
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
	var req denyReq
	if r.ContentLength > 0 {
		_ = json.NewDecoder(r.Body).Decode(&req) // reason is optional
	}
	denied, err := h.store.Deny(ctx, id, cred.ID, req.Reason)
	if err != nil {
		h.writeTransitionErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"exception": exceptionWireFrom(denied)})
}

// Activate handles PATCH /v1/exceptions/{id}/activate (AC-4 enable).
func (h *Handler) Activate(w http.ResponseWriter, r *http.Request) {
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
	var req activateReq
	if r.ContentLength > 0 {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	effectiveFrom := time.Time{}
	if req.EffectiveFrom != nil {
		effectiveFrom = req.EffectiveFrom.UTC()
	}
	activated, err := h.store.Activate(ctx, id, cred.ID, effectiveFrom)
	if err != nil {
		h.writeTransitionErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"exception": exceptionWireFrom(activated)})
}

// ----- helpers -----

func (h *Handler) writeCreateErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, exception.ErrControlRequired),
		errors.Is(err, exception.ErrJustificationRequired),
		errors.Is(err, exception.ErrRequesterRequired),
		errors.Is(err, exception.ErrExpiresAtRequired):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, exception.ErrExpiresAtExceedsCap):
		writeError(w, http.StatusBadRequest, "expires_at exceeds 365-day cap")
	case errors.Is(err, exception.ErrExpiresAtInPast):
		writeError(w, http.StatusBadRequest, "expires_at must be in the future")
	default:
		if strings.Contains(err.Error(), "control_id does not exist") {
			writeError(w, http.StatusBadRequest, "control_id does not exist in tenant")
			return
		}
		writeServerErr(w, "create exception", err)
	}
}

func (h *Handler) writeTransitionErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, exception.ErrNotFound):
		writeError(w, http.StatusNotFound, "exception not found")
	case errors.Is(err, exception.ErrWrongState):
		writeError(w, http.StatusConflict, "exception not in expected state for this transition")
	case errors.Is(err, exception.ErrSegregationOfDuties):
		writeError(w, http.StatusForbidden, "approver must differ from requester (segregation of duties)")
	default:
		writeServerErr(w, "transition", err)
	}
}

// tenantCredContext returns the request context with the tenant GUC
// applied PLUS the resolved credential (so handlers can gate on IsApprover
// / IsAdmin and use cred.ID as the actor).
func (h *Handler) tenantCredContext(r *http.Request) (context.Context, credstore.Credential, bool) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || cred.TenantID == "" {
		return nil, credstore.Credential{}, false
	}
	ctx, err := tenancy.WithTenant(r.Context(), cred.TenantID)
	if err != nil {
		return nil, credstore.Credential{}, false
	}
	return ctx, cred, true
}

func exceptionWireFrom(e exception.Exception) exceptionWire {
	out := exceptionWire{
		ID:                   e.ID.String(),
		ControlID:            e.ControlID.String(),
		ScopeCellPredicate:   jsonRaw(e.ScopeCellPredicate),
		Justification:        e.Justification,
		CompensatingControls: append([]string{}, e.CompensatingControls...),
		RequestedBy:          e.RequestedBy,
		RequestedAt:          e.RequestedAt,
		ExpiresAt:            e.ExpiresAt,
		Status:               e.Status,
		CreatedAt:            e.CreatedAt,
		UpdatedAt:            e.UpdatedAt,
	}
	out.ApprovedBy = e.ApprovedBy
	out.ApprovedAt = e.ApprovedAt
	out.DeniedBy = e.DeniedBy
	out.DeniedAt = e.DeniedAt
	out.ActivatedBy = e.ActivatedBy
	out.ActivatedAt = e.ActivatedAt
	out.EffectiveFrom = e.EffectiveFrom
	out.ExpiredAt = e.ExpiredAt
	return out
}

func jsonRaw(b []byte) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage("{}")
	}
	return json.RawMessage(b)
}

// parseWithin reads small duration strings shaped like `30d`, `12h`, `45m`.
// Empty input returns the default. Go's time.ParseDuration does not accept
// `d` so this is the hand-rolled parser.
func parseWithin(s string) (time.Duration, error) {
	if s == "" {
		return DefaultExpiringWindow, nil
	}
	if len(s) < 2 {
		return 0, errors.New("too short")
	}
	suffix := s[len(s)-1]
	num, err := strconv.Atoi(s[:len(s)-1])
	if err != nil {
		return 0, err
	}
	if num <= 0 {
		return 0, errors.New("must be positive")
	}
	switch suffix {
	case 'd':
		return time.Duration(num) * 24 * time.Hour, nil
	case 'h':
		return time.Duration(num) * time.Hour, nil
	case 'm':
		return time.Duration(num) * time.Minute, nil
	}
	return 0, errors.New("unknown duration unit")
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
