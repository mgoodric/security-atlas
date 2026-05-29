// Package auditperiods serves the slice-028 HTTP API for AuditPeriod +
// freezing primitive. Routes (registered onto the platform root router by
// internal/api/httpserver.go):
//
//	POST /v1/audit-periods                            create (status=open)
//	GET  /v1/audit-periods                            list current tenant
//	GET  /v1/audit-periods/{id}                       get one
//	POST /v1/audit-periods/{id}/freeze                AC-2 (409 on re-freeze)
//	GET  /v1/audit-periods/{id}/control-state         AC-3 horizon read
//	POST /v1/audit-periods/{id}/populations/{popID}   AC-4 attach + stamp
//
// All handlers run with the tenant set by upstream auth middleware
// (internal/api/authctx + internal/api/tenancymw). The store opens its own
// transaction per call and applies the tenant GUC.
//
// Authorization: writes (POST) and read of control-state are restricted to
// IsAdmin or OwnerRoles containing "grc_engineer". The "auditor" role gains
// read access in slice 025 via the slice 035 OPA authz middleware; the
// authz hook is already in place at the platform router level (see
// internal/authz/rego_bundle/audit_periods.rego), so this handler does not
// duplicate auditor-side gating.
package auditperiods

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/audit/period"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Handler bundles the slice-028 routes over a single period.Store.
type Handler struct {
	store *period.Store
}

// New constructs a Handler.
func New(store *period.Store) *Handler { return &Handler{store: store} }

// ----- wire shapes -----

type createReq struct {
	Name               string    `json:"name"`
	FrameworkVersionID string    `json:"framework_version_id"`
	PeriodStart        time.Time `json:"period_start"`
	PeriodEnd          time.Time `json:"period_end"`
}

type periodWire struct {
	ID                 string     `json:"id"`
	Name               string     `json:"name"`
	FrameworkVersionID string     `json:"framework_version_id"`
	PeriodStart        time.Time  `json:"period_start"`
	PeriodEnd          time.Time  `json:"period_end"`
	Status             string     `json:"status"`
	FrozenAt           *time.Time `json:"frozen_at,omitempty"`
	FrozenHashHex      string     `json:"frozen_hash,omitempty"`
	FrozenBy           string     `json:"frozen_by,omitempty"`
	CreatedBy          string     `json:"created_by"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type controlStateObservationWire struct {
	EvidenceRecordID string    `json:"evidence_record_id"`
	ObservedAt       time.Time `json:"observed_at"`
	Result           string    `json:"result"`
	Hash             string    `json:"hash"`
}

// ----- handlers -----

// Create handles POST /v1/audit-periods (AC-1).
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.authnContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if !canWrite(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "admin or grc_engineer role required")
		return
	}

	var req createReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	fwID, err := uuid.Parse(req.FrameworkVersionID)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "framework_version_id must be a UUID")
		return
	}
	if req.Name == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "name must be non-empty")
		return
	}
	if req.PeriodStart.IsZero() || req.PeriodEnd.IsZero() {
		httpresp.WriteError(w, http.StatusBadRequest, "period_start and period_end are required")
		return
	}
	if req.PeriodStart.After(req.PeriodEnd) {
		httpresp.WriteError(w, http.StatusBadRequest, "period_start must be <= period_end")
		return
	}

	p, err := h.store.Create(ctx, period.CreateInput{
		Name:               req.Name,
		FrameworkVersionID: fwID,
		PeriodStart:        req.PeriodStart,
		PeriodEnd:          req.PeriodEnd,
		CreatedBy:          cred.ID,
	})
	if err != nil {
		httperr.WriteInternal(w, r, "create audit period", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusCreated, map[string]any{
		"audit_period": periodWireFrom(p),
	})

}

// Get handles GET /v1/audit-periods/{id}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := h.authnContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	p, err := h.store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, period.ErrNotFound) {
			httpresp.WriteError(w, http.StatusNotFound, "audit period not found")
			return
		}
		httperr.WriteInternal(w, r, "get audit period", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"audit_period": periodWireFrom(p)})
}

// List handles GET /v1/audit-periods.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	ctx, _, ok := h.authnContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	ps, err := h.store.List(ctx)
	if err != nil {
		httperr.WriteInternal(w, r, "list audit periods", err)
		return
	}
	out := make([]periodWire, len(ps))
	for i, p := range ps {
		out[i] = periodWireFrom(p)
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"audit_periods": out,
		"count":         len(out),
	})

}

// Freeze handles POST /v1/audit-periods/{id}/freeze. (AC-2 + AC-6)
func (h *Handler) Freeze(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.authnContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if !canWrite(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "admin or grc_engineer role required")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	p, err := h.store.Freeze(ctx, id, cred.ID, time.Now().UTC())
	if err != nil {
		switch {
		case errors.Is(err, period.ErrNotFound):
			httpresp.WriteError(w, http.StatusNotFound, "audit period not found")
		case errors.Is(err, period.ErrAlreadyFrozen):
			httpresp.WriteError(w, http.StatusConflict, "audit period is already frozen")
		default:
			httperr.WriteInternal(w, r, "freeze audit period", err)
		}
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"audit_period": periodWireFrom(p)})
}

// ControlState handles GET /v1/audit-periods/{id}/control-state?control=<UUID>. (AC-3)
func (h *Handler) ControlState(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.authnContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if !canWrite(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "admin or grc_engineer role required")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	ctrlIDRaw := r.URL.Query().Get("control")
	if ctrlIDRaw == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "control query parameter is required")
		return
	}
	ctrlID, err := uuid.Parse(ctrlIDRaw)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "control must be a UUID")
		return
	}
	obs, err := h.store.ControlState(ctx, id, ctrlID)
	if err != nil {
		if errors.Is(err, period.ErrNotFound) {
			httpresp.WriteError(w, http.StatusNotFound, "audit period not found")
			return
		}
		httperr.WriteInternal(w, r, "control-state", err)
		return
	}
	out := make([]controlStateObservationWire, len(obs))
	for i, o := range obs {
		out[i] = controlStateObservationWire{
			EvidenceRecordID: o.EvidenceRecordID.String(),
			ObservedAt:       o.ObservedAt,
			Result:           o.Result,
			Hash:             o.Hash,
		}
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"audit_period_id": id.String(),
		"control_id":      ctrlID.String(),
		"observations":    out,
		"count":           len(out),
	})

}

// AttachPopulation handles POST /v1/audit-periods/{id}/populations/{popID}. (AC-4)
func (h *Handler) AttachPopulation(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.authnContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if !canWrite(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "admin or grc_engineer role required")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	popID, err := uuid.Parse(chi.URLParam(r, "popID"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "popID must be a UUID")
		return
	}
	if err := h.store.AttachPopulation(ctx, id, popID, cred.ID); err != nil {
		if errors.Is(err, period.ErrNotFound) {
			httpresp.WriteError(w, http.StatusNotFound, "audit period not found")
			return
		}
		httperr.WriteInternal(w, r, "attach population", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"audit_period_id": id.String(),
		"population_id":   popID.String(),
		"attached":        true,
	})

}

// ----- helpers -----

func (h *Handler) authnContext(r *http.Request) (context.Context, credstore.Credential, bool) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || cred.TenantID == "" {
		return nil, credstore.Credential{}, false
	}
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		return nil, credstore.Credential{}, false
	}
	return r.Context(), cred, true
}

// canWrite returns true when the credential is an admin or carries the
// grc_engineer owner role. Slice 035's OPA layer enforces broader RBAC
// across the platform; this handler-local check is defense-in-depth and
// returns 403 even when the OPA middleware is not wired (which is the
// case in unit-test servers).
func canWrite(cred credstore.Credential) bool {
	if cred.IsAdmin {
		return true
	}
	for _, role := range cred.OwnerRoles {
		if role == "grc_engineer" {
			return true
		}
	}
	return false
}

func periodWireFrom(p period.Period) periodWire {
	w := periodWire{
		ID:                 p.ID.String(),
		Name:               p.Name,
		FrameworkVersionID: p.FrameworkVersionID.String(),
		PeriodStart:        p.PeriodStart,
		PeriodEnd:          p.PeriodEnd,
		Status:             string(p.Status),
		CreatedBy:          p.CreatedBy,
		CreatedAt:          p.CreatedAt,
		UpdatedAt:          p.UpdatedAt,
	}
	if p.FrozenAt != nil {
		t := *p.FrozenAt
		w.FrozenAt = &t
	}
	if len(p.FrozenHash) > 0 {
		w.FrozenHashHex = hex.EncodeToString(p.FrozenHash)
	}
	w.FrozenBy = p.FrozenBy
	return w
}
