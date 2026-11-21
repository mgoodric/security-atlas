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
//	PATCH  /v1/exceptions/{id}/revoke           active -> revoked (#2113)
//
// All handlers run with the tenant set by upstream auth middleware (see
// internal/api/authctx). The store opens its own transaction per call and
// applies the tenant GUC.
//
// AC-3 + segregation of duties: approve/deny/activate/revoke require IsApprover
// (with IsAdmin implying IsApprover) and a person, never a machine
// credential (#2343). approve/deny also reject when the
// caller's credential id equals the row's requested_by (defense in depth
// against same-user filing + adjudicating).
package exceptions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/approval"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/exception"
	"github.com/mgoodric/security-atlas/internal/scope"
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

type revokeReq struct {
	Reason string `json:"reason"`
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
	// SelfApproved marks an exception its requester approved under the
	// organisation's Allow self-approval setting (#2101).
	SelfApproved  bool       `json:"self_approved"`
	DeniedBy      *string    `json:"denied_by,omitempty"`
	DeniedAt      *time.Time `json:"denied_at,omitempty"`
	ActivatedBy   *string    `json:"activated_by,omitempty"`
	ActivatedAt   *time.Time `json:"activated_at,omitempty"`
	EffectiveFrom *time.Time `json:"effective_from,omitempty"`
	ExpiresAt     time.Time  `json:"expires_at"`
	ExpiredAt     *time.Time `json:"expired_at,omitempty"`
	RevokedBy     *string    `json:"revoked_by,omitempty"`
	RevokedAt     *time.Time `json:"revoked_at,omitempty"`
	Status        string     `json:"status"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// CreateException handles POST /v1/exceptions (AC-1).
func (h *Handler) CreateException(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.tenantCredContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	var req createReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.ControlID == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "control_id is required")
		return
	}
	controlID, err := uuid.Parse(req.ControlID)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "control_id must be a UUID")
		return
	}
	if strings.TrimSpace(req.Justification) == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "justification is required")
		return
	}
	if req.ExpiresAt == nil {
		httpresp.WriteError(w, http.StatusBadRequest, "expires_at is required")
		return
	}
	if msg := checkPredicate(req.ScopeCellPredicate); msg != "" {
		httpresp.WriteError(w, http.StatusBadRequest, msg)
		return
	}

	in := exception.CreateInput{
		ControlID:            controlID,
		ScopeCellPredicate:   []byte(req.ScopeCellPredicate),
		Justification:        req.Justification,
		CompensatingControls: req.CompensatingControls,
		// The person, not the per-token JWT credential id, so approval can
		// compare one person across sessions (#2101).
		RequestedBy: credstore.PrincipalOf(cred),
		ExpiresAt:   req.ExpiresAt.UTC(),
	}
	// #2618: an Idempotency-Key makes a retry after a client timeout
	// replay the first request's exception instead of filing a second.
	if key := r.Header.Get(IdempotencyKeyHeader); key != "" {
		if !validIdempotencyKey(key) {
			httpresp.WriteError(w, http.StatusBadRequest,
				"Idempotency-Key must be 1 to 255 printable ASCII characters")
			return
		}
		in.IdempotencyKey = key
		in.RequestHash = requestHash(controlID, req)
		in.LegacyRequestHash = legacyRequestHash(controlID, req)
	}
	created, replayed, err := h.store.CreateIdempotent(ctx, in)
	if err != nil {
		h.writeCreateErr(w, r, err)
		return
	}
	if replayed {
		w.Header().Set(IdempotentReplayedHeader, "true")
	}
	httpresp.WriteJSON(w, http.StatusCreated, map[string]any{"exception": exceptionWireFrom(created)})
}

// IdempotencyKeyHeader is the request header that makes POST
// /v1/exceptions idempotent (#2618). IdempotentReplayedHeader marks a
// response that replays an earlier request's exception.
const (
	IdempotencyKeyHeader     = "Idempotency-Key"
	IdempotentReplayedHeader = "Idempotent-Replayed"
)

// validIdempotencyKey admits 1 to 255 printable ASCII characters, the
// shape a client-generated UUID or token takes.
func validIdempotencyKey(key string) bool {
	if len(key) == 0 || len(key) > 255 {
		return false
	}
	for i := 0; i < len(key); i++ {
		if key[i] < 0x21 || key[i] > 0x7e {
			return false
		}
	}
	return true
}

// requestHash identifies a create request's content, so one key sent with
// a different body is refused rather than replayed. Equivalent spellings
// of one request hash the same (#2714): the predicate is re-encoded with
// its object keys sorted, an omitted predicate counts as {} (its stored
// spelling), compensating_controls omitted, null and [] are one value, and
// the expiry is normalised to UTC. The order of compensating_controls
// still counts.
func requestHash(controlID uuid.UUID, req createReq) string {
	compensating := req.CompensatingControls
	if compensating == nil {
		compensating = []string{}
	}
	canonical, _ := json.Marshal(struct {
		ControlID            string   `json:"control_id"`
		ScopeCellPredicate   string   `json:"scope_cell_predicate"`
		Justification        string   `json:"justification"`
		CompensatingControls []string `json:"compensating_controls"`
		ExpiresAt            string   `json:"expires_at"`
	}{
		ControlID:            controlID.String(),
		ScopeCellPredicate:   canonicalPredicate(req.ScopeCellPredicate),
		Justification:        req.Justification,
		CompensatingControls: compensating,
		ExpiresAt:            req.ExpiresAt.UTC().Format(time.RFC3339Nano),
	})
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// legacyRequestHash is requestHash as #2618 shipped it: the predicate only
// compacted (an omitted one hashes as ""), and compensating_controls kept
// as sent (omitted or null hash as null). A key recorded by that build
// replays when it matches, so a retry across the upgrade is not refused.
//
// TODO(remove after next release, #2803): once every key recorded before #2714
// is past IdempotencyWindow, delete this and CreateInput.LegacyRequestHash.
func legacyRequestHash(controlID uuid.UUID, req createReq) string {
	var predicate bytes.Buffer
	if err := json.Compact(&predicate, req.ScopeCellPredicate); err != nil {
		predicate.Reset()
		predicate.Write(req.ScopeCellPredicate)
	}
	canonical, _ := json.Marshal(struct {
		ControlID            string   `json:"control_id"`
		ScopeCellPredicate   string   `json:"scope_cell_predicate"`
		Justification        string   `json:"justification"`
		CompensatingControls []string `json:"compensating_controls"`
		ExpiresAt            string   `json:"expires_at"`
	}{
		ControlID:            controlID.String(),
		ScopeCellPredicate:   predicate.String(),
		Justification:        req.Justification,
		CompensatingControls: req.CompensatingControls,
		ExpiresAt:            req.ExpiresAt.UTC().Format(time.RFC3339Nano),
	})
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// canonicalPredicate re-encodes a predicate with sorted object keys
// (encoding/json sorts map keys) and numbers kept as their literal text.
// Input that does not decode is hashed as sent.
func canonicalPredicate(raw json.RawMessage) string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return "{}"
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return string(raw)
	}
	out, err := json.Marshal(v)
	if err != nil {
		return string(raw)
	}
	return string(out)
}

// ListExceptions handles GET /v1/exceptions, filterable by ?status= and
// ?control_id= (#2140: one control's exceptions, for the control page).
// RLS keeps the list to the caller's tenant either way.
func (h *Handler) ListExceptions(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := h.tenantCredContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	filter := exception.ListFilter{Status: strings.TrimSpace(r.URL.Query().Get("status"))}
	if raw := strings.TrimSpace(r.URL.Query().Get("control_id")); raw != "" {
		cid, err := uuid.Parse(raw)
		if err != nil {
			httpresp.WriteError(w, http.StatusBadRequest, "control_id must be a UUID")
			return
		}
		filter.ControlID = cid
	}
	rows, err := h.store.List(ctx, filter)
	if err != nil {
		httperr.WriteInternal(w, r, "list exceptions", err)
		return
	}
	out := make([]exceptionWire, len(rows))
	for i, e := range rows {
		out[i] = exceptionWireFrom(e)
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"exceptions": out, "count": len(out)})
}

// GetException handles GET /v1/exceptions/{id}.
func (h *Handler) GetException(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := h.tenantCredContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	e, err := h.store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, exception.ErrNotFound) {
			httpresp.WriteError(w, http.StatusNotFound, "exception not found")
			return
		}
		httperr.WriteInternal(w, r, "get exception", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"exception": exceptionWireFrom(e)})
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
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	withinStr := strings.TrimSpace(r.URL.Query().Get("within"))
	within, err := parseWithin(withinStr)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "within must look like '30d', '12h', or '45m'")
		return
	}
	if within > MaxExpiringWindow {
		httpresp.WriteError(w, http.StatusBadRequest, "within exceeds maximum (365d)")
		return
	}
	rows, err := h.store.Expiring(ctx, time.Now().UTC(), within)
	if err != nil {
		httperr.WriteInternal(w, r, "list expiring", err)
		return
	}
	out := make([]exceptionWire, len(rows))
	for i, e := range rows {
		out[i] = exceptionWireFrom(e)
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"exceptions": out,
		"count":      len(out),
		"within":     within.String(),
	})

}

// AuditLog handles GET /v1/exceptions/{id}/audit-log (AC-7 read).
func (h *Handler) AuditLog(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := h.tenantCredContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	entries, err := h.store.ListAuditLog(ctx, id)
	if err != nil {
		httperr.WriteInternal(w, r, "list audit log", err)
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
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"entries": out, "count": len(out)})
}

// Approve handles PATCH /v1/exceptions/{id}/approve (AC-3).
func (h *Handler) Approve(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.tenantCredContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if !authz.IsTenantApprover(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "approver role required")
		return
	}
	if !requirePerson(w, cred, "approve") {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	// Only a person approves (#2101): UserID is empty for an unbound key,
	// an OAuth client or the bootstrap admin, and the store refuses it.
	approved, err := h.store.Approve(ctx, id, approval.Approver{
		UserID:       credstore.MinterUserID(cred),
		CredentialID: cred.ID,
	})
	if err != nil {
		h.writeTransitionErr(w, r, err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"exception": exceptionWireFrom(approved)})
}

// Deny handles PATCH /v1/exceptions/{id}/deny (AC-3 terminal path).
func (h *Handler) Deny(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.tenantCredContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if !authz.IsTenantApprover(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "approver role required")
		return
	}
	if !requirePerson(w, cred, "deny") {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	var req denyReq
	if r.ContentLength > 0 {
		_ = json.NewDecoder(r.Body).Decode(&req) // reason is optional
	}
	denied, err := h.store.Deny(ctx, id, exception.Denier{Principal: credstore.PrincipalOf(cred), CredentialID: cred.ID}, req.Reason)
	if err != nil {
		h.writeTransitionErr(w, r, err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"exception": exceptionWireFrom(denied)})
}

// Activate handles PATCH /v1/exceptions/{id}/activate (AC-4 enable).
func (h *Handler) Activate(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.tenantCredContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if !authz.IsTenantApprover(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "approver role required")
		return
	}
	if !requirePerson(w, cred, "activate") {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
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
		h.writeTransitionErr(w, r, err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"exception": exceptionWireFrom(activated)})
}

// Revoke handles PATCH /v1/exceptions/{id}/revoke (#2113): an approver
// ends an active exception before it expires, with a reason.
func (h *Handler) Revoke(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.tenantCredContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if !authz.IsTenantApprover(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "approver role required")
		return
	}
	if !requirePerson(w, cred, "revoke") {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	var req revokeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "reason is required")
		return
	}
	revoked, err := h.store.Revoke(ctx, id, credstore.PrincipalOf(cred), req.Reason)
	if err != nil {
		h.writeTransitionErr(w, r, err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"exception": exceptionWireFrom(revoked)})
}

// ----- helpers -----

// requirePerson refuses a lifecycle move from a machine credential (#2343).
// Approve, deny, activate and revoke are ActorPerson moves in Lifecycle:
// MinterUserID is empty for an unbound API key, an OAuth client and the
// bootstrap admin, so those get 403 whatever their roles. Writes the 403
// and returns false when the caller is not a person.
func requirePerson(w http.ResponseWriter, cred credstore.Credential, verb string) bool {
	if credstore.MinterUserID(cred) != "" {
		return true
	}
	httpresp.WriteError(w, http.StatusForbidden, "only a person can "+verb+" an exception; use your own login or a key bound to your user, not an unbound API key, OAuth client or bootstrap credential")
	return false
}

// checkPredicate refuses a scope_cell_predicate the evaluator would read
// as something the requester did not mean. A JSON null, or anything that
// is not an object, is refused: internal/scope reads a stored null as
// matching every cell, and the request form's picker emits null for an
// incomplete choice (#2103), so a null here is an unfinished form, never
// "the whole control". The whole control is {"op":"true"}, or the field
// left out. Returns the 400 message, or "" when the predicate is fine.
func checkPredicate(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return ""
	}
	if trimmed == "null" {
		return `scope_cell_predicate must not be null; send {"op":"true"} for the whole control`
	}
	if !strings.HasPrefix(trimmed, "{") {
		return "scope_cell_predicate must be a JSON object"
	}
	// {} is the stored spelling of the omitted field. Anything else must be
	// a well-formed expression in the scope grammar (#2343): {"op":"bogus"}
	// would otherwise be stored and fail every evaluation of the control.
	if trimmed == "{}" {
		return ""
	}
	if err := scope.ValidateExpr([]byte(trimmed)); err != nil {
		return "scope_cell_predicate is not a valid scope expression: " + strings.TrimPrefix(err.Error(), scope.ErrBadPredicate.Error()+": ")
	}
	return ""
}

func (h *Handler) writeCreateErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, exception.ErrControlRequired),
		errors.Is(err, exception.ErrJustificationRequired),
		errors.Is(err, exception.ErrRequesterRequired),
		errors.Is(err, exception.ErrExpiresAtRequired):
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, exception.ErrExpiresAtExceedsCap):
		httpresp.WriteError(w, http.StatusBadRequest, "expires_at exceeds 365-day cap")
	case errors.Is(err, exception.ErrExpiresAtInPast):
		httpresp.WriteError(w, http.StatusBadRequest, "expires_at must be in the future")
	case errors.Is(err, exception.ErrIdempotencyKeyReused):
		httpresp.WriteError(w, http.StatusUnprocessableEntity,
			"Idempotency-Key was already used with a different request; send a new key")
	default:
		if strings.Contains(err.Error(), "control_id does not exist") {
			httpresp.WriteError(w, http.StatusBadRequest, "control_id does not exist in tenant")
			return
		}
		httperr.WriteInternal(w, r, "create exception", err)
	}
}

func (h *Handler) writeTransitionErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, exception.ErrNotFound):
		httpresp.WriteError(w, http.StatusNotFound, "exception not found")
	case errors.Is(err, exception.ErrWrongState):
		httpresp.WriteError(w, http.StatusConflict, "exception not in expected state for this transition")
	case errors.Is(err, exception.ErrSegregationOfDuties):
		httpresp.WriteError(w, http.StatusForbidden, "approver must differ from requester (segregation of duties)")
	case errors.Is(err, exception.ErrApproverNotPerson):
		httpresp.WriteError(w, http.StatusForbidden, exception.ErrApproverNotPerson.Error())
	case errors.Is(err, exception.ErrReasonRequired):
		httpresp.WriteError(w, http.StatusBadRequest, "reason is required")
	default:
		httperr.WriteInternal(w, r, "transition", err)
	}
}

// tenantCredContext returns the request context (with the tenant GUC
// already applied by slice-033's tenancy.Middleware) plus the resolved
// credential, so handlers can gate on IsApprover / IsAdmin and use
// cred.ID as the actor.
func (h *Handler) tenantCredContext(r *http.Request) (context.Context, credstore.Credential, bool) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || cred.TenantID == "" {
		return nil, credstore.Credential{}, false
	}
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		// Middleware should have set this; bail loudly rather than
		// silently re-WithTenant (would mask a misconfig).
		return nil, credstore.Credential{}, false
	}
	return r.Context(), cred, true
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
	out.SelfApproved = e.SelfApproved
	out.DeniedBy = e.DeniedBy
	out.DeniedAt = e.DeniedAt
	out.ActivatedBy = e.ActivatedBy
	out.ActivatedAt = e.ActivatedAt
	out.EffectiveFrom = e.EffectiveFrom
	out.ExpiredAt = e.ExpiredAt
	out.RevokedBy = e.RevokedBy
	out.RevokedAt = e.RevokedAt
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
	var unit time.Duration
	switch suffix {
	case 'd':
		unit = 24 * time.Hour
	case 'h':
		unit = time.Hour
	case 'm':
		unit = time.Minute
	default:
		return 0, errors.New("unknown duration unit")
	}
	// Refuse a num that would overflow before multiplying: it wraps
	// time.Duration negative and slips under the caller's
	// MaxExpiringWindow check (#1914).
	if int64(num) > math.MaxInt64/int64(unit) {
		return 0, errors.New("out of range")
	}
	return time.Duration(num) * unit, nil
}
