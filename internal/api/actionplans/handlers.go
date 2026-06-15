// Package actionplans serves the slice-384 HTTP API for the ActionPlan
// primitive (canvas §6 risk register). Routes (registered onto the platform
// root router by internal/api/register_risk.go):
//
//	POST   /v1/action-plans                              create (AC-10)
//	GET    /v1/action-plans                              list + pagination (AC-11)
//	GET    /v1/action-plans/{id}                         get one + linkage (AC-12)
//	PATCH  /v1/action-plans/{id}                         update + state machine (AC-13/AC-15)
//	DELETE /v1/action-plans/{id}                         soft-delete (AC-14)
//	POST   /v1/action-plans/{id}/risks/{risk_id}         link risk (AC-17)
//	DELETE /v1/action-plans/{id}/risks/{risk_id}         unlink risk (AC-18)
//	POST   /v1/action-plans/{id}/controls/{control_id}   link control (AC-19)
//	DELETE /v1/action-plans/{id}/controls/{control_id}   unlink control (AC-20)
//
// All handlers run with the tenant set by upstream auth middleware (see
// internal/api/authctx + internal/api/tenancymw). The store opens its own
// transaction per call and applies the tenant GUC.
//
// Authorization (P0-384-9 — NO new role): mutations require
// risk_register:write, reads require risk_register:read. These are the
// slice-056 roles, reused via the same hasProgramRead / hasProgramWrite
// credential-flag derivation the slice-067 decisions guard uses. The
// slice-035 OPA middleware is the primary gate in production; this
// handler-level guard is its defense-in-depth twin and the testable
// enforcement point (OPA is nil in test servers).
//
// AI-assist boundary (P0-384-12): there is no AI auto-creation path. Every
// create/update is human-driven; the actor is the verified credential's
// user id (never the request body) for repudiation.
package actionplans

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

	"github.com/mgoodric/security-atlas/internal/actionplan"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Handler bundles the slice-384 routes over a single actionplan.Store.
type Handler struct {
	store *actionplan.Store
}

// New constructs a Handler.
func New(store *actionplan.Store) *Handler { return &Handler{store: store} }

// ----- wire shapes -----

type createReq struct {
	Title           string     `json:"title"`
	Description     string     `json:"description"`
	TriggeringEvent string     `json:"triggering_event"`
	OwnerID         string     `json:"owner_id"`
	DueDate         *time.Time `json:"due_date"`
	AuditPeriodID   *string    `json:"audit_period_id"`
}

type updateReq struct {
	Title           *string    `json:"title"`
	Description     *string    `json:"description"`
	TriggeringEvent *string    `json:"triggering_event"`
	OwnerID         *string    `json:"owner_id"`
	DueDate         *time.Time `json:"due_date"`
	ClearDueDate    bool       `json:"clear_due_date"`
	Status          *string    `json:"status"`
}

type linkWire struct {
	TargetID string    `json:"target_id"`
	LinkedAt time.Time `json:"linked_at"`
	LinkedBy string    `json:"linked_by"`
}

type linkageWire struct {
	Risks    []linkWire `json:"risks"`
	Controls []linkWire `json:"controls"`
}

type planWire struct {
	ID              string    `json:"id"`
	Title           string    `json:"title"`
	Description     string    `json:"description"`
	TriggeringEvent string    `json:"triggering_event"`
	OwnerID         string    `json:"owner_id"`
	DueDate         *string   `json:"due_date,omitempty"`
	Status          string    `json:"status"`
	AuditPeriodID   *string   `json:"audit_period_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// CreateActionPlan handles POST /v1/action-plans (AC-10).
func (h *Handler) CreateActionPlan(w http.ResponseWriter, r *http.Request) {
	if !requireProgramWrite(w, r) {
		return
	}
	ctx, actor, ok := h.identity(w, r)
	if !ok {
		return
	}
	var req createReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "title is required")
		return
	}
	ownerID, err := uuid.Parse(strings.TrimSpace(req.OwnerID))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "owner_id must be a UUID")
		return
	}
	in := actionplan.CreateInput{
		Title:           req.Title,
		Description:     req.Description,
		TriggeringEvent: req.TriggeringEvent,
		OwnerID:         ownerID,
		Actor:           actor,
	}
	if req.DueDate != nil {
		d := req.DueDate.UTC()
		in.DueDate = &d
	}
	if req.AuditPeriodID != nil && strings.TrimSpace(*req.AuditPeriodID) != "" {
		apID, perr := uuid.Parse(strings.TrimSpace(*req.AuditPeriodID))
		if perr != nil {
			httpresp.WriteError(w, http.StatusBadRequest, "audit_period_id must be a UUID")
			return
		}
		in.AuditPeriodID = &apID
	}
	created, err := h.store.Create(ctx, in)
	if err != nil {
		h.writeStoreErr(w, r, "create action plan", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusCreated, map[string]any{"action_plan": planWireFrom(created)})
}

// ListActionPlans handles GET /v1/action-plans (AC-11). Keyset pagination:
//
//	?status=draft|in_progress|blocked|completed|verified
//	?limit=25                                  (default 25, max 100)
//	?cursor=<created_at_rfc3339>,<id>          (opaque "next" cursor)
func (h *Handler) ListActionPlans(w http.ResponseWriter, r *http.Request) {
	if !requireProgramRead(w, r) {
		return
	}
	ctx, _, ok := h.tenantCtx(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	filter := actionplan.ListFilter{Status: strings.TrimSpace(r.URL.Query().Get("status"))}
	if filter.Status != "" && !actionplan.ValidStatus(filter.Status) {
		httpresp.WriteError(w, http.StatusBadRequest, "status is not a valid action plan status")
		return
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		n, perr := strconv.Atoi(raw)
		if perr != nil || n < 0 {
			httpresp.WriteError(w, http.StatusBadRequest, "limit must be a non-negative integer")
			return
		}
		filter.Limit = n
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("cursor")); raw != "" {
		ts, id, perr := parseCursor(raw)
		if perr != nil {
			httpresp.WriteError(w, http.StatusBadRequest, "cursor is malformed")
			return
		}
		filter.CursorCreatedAt = &ts
		filter.CursorID = &id
	}
	rows, err := h.store.List(ctx, filter)
	if err != nil {
		h.writeStoreErr(w, r, "list action plans", err)
		return
	}
	out := make([]planWire, len(rows))
	for i, p := range rows {
		out[i] = planWireFrom(p)
	}
	body := map[string]any{"action_plans": out, "count": len(out)}
	// Emit a next-cursor when the page is full (more rows may exist).
	if len(rows) == actionplan.ClampLimit(filter.Limit) && len(rows) > 0 {
		last := rows[len(rows)-1]
		body["next_cursor"] = encodeCursor(last.CreatedAt, last.ID)
	}
	httpresp.WriteJSON(w, http.StatusOK, body)
}

// GetActionPlan handles GET /v1/action-plans/{id} (AC-12). One round-trip
// returns the plan plus linked risks + controls.
func (h *Handler) GetActionPlan(w http.ResponseWriter, r *http.Request) {
	if !requireProgramRead(w, r) {
		return
	}
	ctx, _, ok := h.tenantCtx(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	p, lk, err := h.store.GetWithLinkage(ctx, id)
	if err != nil {
		h.writeStoreErr(w, r, "get action plan", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"action_plan": planWireFrom(p),
		"linkage":     linkageWireFrom(lk),
	})
}

// UpdateActionPlan handles PATCH /v1/action-plans/{id} (AC-13/AC-15).
func (h *Handler) UpdateActionPlan(w http.ResponseWriter, r *http.Request) {
	if !requireProgramWrite(w, r) {
		return
	}
	ctx, actor, ok := h.identity(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	var req updateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	in := actionplan.UpdateInput{
		Title:           req.Title,
		Description:     req.Description,
		TriggeringEvent: req.TriggeringEvent,
		ClearDueDate:    req.ClearDueDate,
		Status:          req.Status,
		Actor:           actor,
	}
	if req.OwnerID != nil {
		ownerID, perr := uuid.Parse(strings.TrimSpace(*req.OwnerID))
		if perr != nil {
			httpresp.WriteError(w, http.StatusBadRequest, "owner_id must be a UUID")
			return
		}
		in.OwnerID = &ownerID
	}
	if req.DueDate != nil {
		d := req.DueDate.UTC()
		in.DueDate = &d
	}
	updated, err := h.store.Update(ctx, id, in)
	if err != nil {
		h.writeStoreErr(w, r, "update action plan", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"action_plan": planWireFrom(updated)})
}

// DeleteActionPlan handles DELETE /v1/action-plans/{id} (AC-14). Soft-delete.
func (h *Handler) DeleteActionPlan(w http.ResponseWriter, r *http.Request) {
	if !requireProgramWrite(w, r) {
		return
	}
	ctx, actor, ok := h.identity(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	if err := h.store.Tombstone(ctx, id, actor); err != nil {
		h.writeStoreErr(w, r, "delete action plan", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id.String()})
}

// LinkRisk handles POST /v1/action-plans/{id}/risks/{risk_id} (AC-17).
func (h *Handler) LinkRisk(w http.ResponseWriter, r *http.Request) {
	h.linkHandler(w, r, "risk_id", func(ctx context.Context, planID, target, actor uuid.UUID) error {
		return h.store.LinkRisk(ctx, planID, target, actor)
	})
}

// UnlinkRisk handles DELETE /v1/action-plans/{id}/risks/{risk_id} (AC-18).
func (h *Handler) UnlinkRisk(w http.ResponseWriter, r *http.Request) {
	h.unlinkHandler(w, r, "risk_id", func(ctx context.Context, planID, target, actor uuid.UUID) error {
		return h.store.UnlinkRisk(ctx, planID, target, actor)
	})
}

// LinkControl handles POST /v1/action-plans/{id}/controls/{control_id} (AC-19).
func (h *Handler) LinkControl(w http.ResponseWriter, r *http.Request) {
	h.linkHandler(w, r, "control_id", func(ctx context.Context, planID, target, actor uuid.UUID) error {
		return h.store.LinkControl(ctx, planID, target, actor)
	})
}

// UnlinkControl handles DELETE /v1/action-plans/{id}/controls/{control_id} (AC-20).
func (h *Handler) UnlinkControl(w http.ResponseWriter, r *http.Request) {
	h.unlinkHandler(w, r, "control_id", func(ctx context.Context, planID, target, actor uuid.UUID) error {
		return h.store.UnlinkControl(ctx, planID, target, actor)
	})
}

func (h *Handler) linkHandler(w http.ResponseWriter, r *http.Request, param string, fn func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error) {
	if !requireProgramWrite(w, r) {
		return
	}
	ctx, actor, ok := h.identity(w, r)
	if !ok {
		return
	}
	planID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	target, err := uuid.Parse(chi.URLParam(r, param))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, param+" must be a UUID")
		return
	}
	if err := fn(ctx, planID, target, actor); err != nil {
		h.writeStoreErr(w, r, "link", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"linked": true, "action_plan_id": planID.String(), param: target.String(),
	})
}

func (h *Handler) unlinkHandler(w http.ResponseWriter, r *http.Request, param string, fn func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error) {
	if !requireProgramWrite(w, r) {
		return
	}
	ctx, actor, ok := h.identity(w, r)
	if !ok {
		return
	}
	planID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	target, err := uuid.Parse(chi.URLParam(r, param))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, param+" must be a UUID")
		return
	}
	if err := fn(ctx, planID, target, actor); err != nil {
		h.writeStoreErr(w, r, "unlink", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"linked": false, "action_plan_id": planID.String(), param: target.String(),
	})
}

// PlansForRisk handles GET /v1/risks/{id}/action-plans (AC-25 read-only
// "Linked Action Plans" section). Returns the live plans linked to a risk.
func (h *Handler) PlansForRisk(w http.ResponseWriter, r *http.Request) {
	h.plansForTarget(w, r, func(ctx context.Context, target uuid.UUID) ([]actionplan.PlanRef, error) {
		return h.store.PlansForRisk(ctx, target)
	})
}

// PlansForControl handles GET /v1/controls/{id}/action-plans (AC-26).
func (h *Handler) PlansForControl(w http.ResponseWriter, r *http.Request) {
	h.plansForTarget(w, r, func(ctx context.Context, target uuid.UUID) ([]actionplan.PlanRef, error) {
		return h.store.PlansForControl(ctx, target)
	})
}

func (h *Handler) plansForTarget(w http.ResponseWriter, r *http.Request, fn func(context.Context, uuid.UUID) ([]actionplan.PlanRef, error)) {
	if !requireProgramRead(w, r) {
		return
	}
	ctx, _, ok := h.tenantCtx(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	target, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	refs, err := fn(ctx, target)
	if err != nil {
		h.writeStoreErr(w, r, "list linked action plans", err)
		return
	}
	type refWire struct {
		ID      string  `json:"id"`
		Title   string  `json:"title"`
		Status  string  `json:"status"`
		DueDate *string `json:"due_date,omitempty"`
	}
	out := make([]refWire, len(refs))
	for i, ref := range refs {
		rw := refWire{ID: ref.ID.String(), Title: ref.Title, Status: ref.Status}
		if ref.DueDate != nil {
			d := ref.DueDate.Format("2006-01-02")
			rw.DueDate = &d
		}
		out[i] = rw
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"action_plans": out, "count": len(out)})
}

// ----- helpers -----

// writeStoreErr maps actionplan.Store errors to HTTP status codes.
//
//	ErrNotFound / ErrLinkTargetNotFound / ErrNotLinked -> 404
//	ErrAlreadyLinked                                   -> 409
//	ErrIllegalTransition / ErrLimitExceeded            -> 422
//	validation errors                                  -> 400
//	anything else                                      -> 500
func (h *Handler) writeStoreErr(w http.ResponseWriter, r *http.Request, op string, err error) {
	switch {
	case errors.Is(err, actionplan.ErrNotFound):
		httpresp.WriteError(w, http.StatusNotFound, "action plan not found")
	case errors.Is(err, actionplan.ErrLinkTargetNotFound):
		// AC-29 / P0-384-4: cross-tenant (or absent) target reported as 404,
		// never 403 — a 403 would confirm the entity exists.
		httpresp.WriteError(w, http.StatusNotFound, "link target not found")
	case errors.Is(err, actionplan.ErrNotLinked):
		httpresp.WriteError(w, http.StatusNotFound, "link does not exist")
	case errors.Is(err, actionplan.ErrAlreadyLinked):
		httpresp.WriteError(w, http.StatusConflict, "already linked")
	case errors.Is(err, actionplan.ErrIllegalTransition):
		httpresp.WriteError(w, http.StatusUnprocessableEntity, "illegal status transition")
	case errors.Is(err, actionplan.ErrLimitExceeded):
		httpresp.WriteError(w, http.StatusUnprocessableEntity, "limit_exceeded")
	case errors.Is(err, actionplan.ErrTitleRequired),
		errors.Is(err, actionplan.ErrTitleTooLong),
		errors.Is(err, actionplan.ErrDescriptionTooLong),
		errors.Is(err, actionplan.ErrTriggeringEventTooLong),
		errors.Is(err, actionplan.ErrOwnerRequired),
		errors.Is(err, actionplan.ErrOwnerNotInTenant),
		errors.Is(err, actionplan.ErrDueDateTooFar),
		errors.Is(err, actionplan.ErrInvalidStatus):
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
	default:
		httperr.WriteInternal(w, r, op, err)
	}
}

// tenantCtx returns the request context (tenant GUC already applied upstream)
// plus the resolved credential.
func (h *Handler) tenantCtx(r *http.Request) (context.Context, credstore.Credential, bool) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || cred.TenantID == "" {
		return nil, credstore.Credential{}, false
	}
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		return nil, credstore.Credential{}, false
	}
	return r.Context(), cred, true
}

// identity resolves the actor user UUID from the verified credential (never
// the request body — threat-model R). An ActionPlan mutation is a human
// action; a machine credential without a user-shaped id is rejected.
func (h *Handler) identity(w http.ResponseWriter, r *http.Request) (context.Context, uuid.UUID, bool) {
	ctx, cred, ok := h.tenantCtx(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return nil, uuid.Nil, false
	}
	actor, err := uuid.Parse(jwtmw.SubjectUserID(cred.UserID))
	if err != nil {
		httpresp.WriteError(w, http.StatusForbidden, "action plan mutation requires a user credential")
		return nil, uuid.Nil, false
	}
	return ctx, actor, true
}

func planWireFrom(p actionplan.ActionPlan) planWire {
	out := planWire{
		ID:              p.ID.String(),
		Title:           p.Title,
		Description:     p.Description,
		TriggeringEvent: p.TriggeringEvent,
		OwnerID:         p.OwnerID.String(),
		Status:          p.Status,
		CreatedAt:       p.CreatedAt,
		UpdatedAt:       p.UpdatedAt,
	}
	if p.DueDate != nil {
		d := p.DueDate.Format("2006-01-02")
		out.DueDate = &d
	}
	if p.AuditPeriodID != nil {
		a := p.AuditPeriodID.String()
		out.AuditPeriodID = &a
	}
	return out
}

func linkageWireFrom(lk actionplan.Linkage) linkageWire {
	return linkageWire{
		Risks:    linkSliceWire(lk.Risks),
		Controls: linkSliceWire(lk.Controls),
	}
}

func linkSliceWire(links []actionplan.Link) []linkWire {
	out := make([]linkWire, len(links))
	for i, l := range links {
		out[i] = linkWire{TargetID: l.TargetID.String(), LinkedAt: l.LinkedAt, LinkedBy: l.LinkedBy.String()}
	}
	return out
}

// encodeCursor / parseCursor encode the keyset cursor as
// "<rfc3339nano>,<uuid>". Opaque to clients; they round-trip next_cursor.
func encodeCursor(ts time.Time, id uuid.UUID) string {
	return ts.UTC().Format(time.RFC3339Nano) + "," + id.String()
}

func parseCursor(raw string) (time.Time, uuid.UUID, error) {
	parts := strings.SplitN(raw, ",", 2)
	if len(parts) != 2 {
		return time.Time{}, uuid.Nil, errors.New("cursor must be '<timestamp>,<id>'")
	}
	ts, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	return ts, id, nil
}
