package auditperiods

// #2118: the auditors of one audit period.
//
//	GET    /v1/audit-periods/{id}/auditors            list, plus candidates
//	POST   /v1/audit-periods/{id}/auditors            assign {user_id}
//	DELETE /v1/audit-periods/{id}/auditors/{userID}   remove
//
// Admin only, on all three. There is no audit-lead role: the ticket's
// "audit lead or admin" is admin, which matches the admin-only audit-period
// writes Matt decided in #2291. OPA admits an auditor's GET on the
// audit-periods resource, so the handler check is what keeps one auditor
// from listing the others; the writes are refused at both layers.
//
// The assignee must be an active user of the caller's tenant holding the
// auditor role (400 otherwise). The list also returns the candidates: the
// tenant's active auditors not yet on the period, which the page's picker
// offers. It is tenant-scoped in SQL, so a super-admin sees only the
// current tenant's users there (GET /v1/admin/users goes cross-tenant). Each change writes an
// audit_period_audit_log row in the same transaction (auditor.Store).

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/audit/auditor"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// AuditorsHandler serves the period-auditor routes over auditor.Store.
type AuditorsHandler struct {
	store *auditor.Store
}

// NewAuditors constructs an AuditorsHandler.
func NewAuditors(store *auditor.Store) *AuditorsHandler {
	return &AuditorsHandler{store: store}
}

type periodAuditorWire struct {
	UserID      string    `json:"user_id"`
	DisplayName string    `json:"display_name"`
	Email       string    `json:"email"`
	GrantedAt   time.Time `json:"granted_at"`
	GrantedBy   string    `json:"granted_by"`
}

// auditorCandidateWire is an active auditor of the tenant not yet on the
// period. It carries only the name and email GET /v1/admin/users already
// shows an admin.
type auditorCandidateWire struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
}

type assignAuditorReq struct {
	UserID string `json:"user_id"`
}

// List handles GET /v1/audit-periods/{id}/auditors.
func (h *AuditorsHandler) List(w http.ResponseWriter, r *http.Request) {
	periodID, _, ok := h.admit(w, r)
	if !ok {
		return
	}
	rows, candidates, err := h.store.ListForPeriod(r.Context(), periodID)
	if err != nil {
		writeAuditorErr(w, r, "list period auditors", err)
		return
	}
	out := make([]periodAuditorWire, len(rows))
	for i, a := range rows {
		out[i] = periodAuditorWire{
			UserID:      a.UserID,
			DisplayName: a.DisplayName,
			Email:       a.Email,
			GrantedAt:   a.GrantedAt,
			GrantedBy:   a.GrantedBy,
		}
	}
	cand := make([]auditorCandidateWire, len(candidates))
	for i, c := range candidates {
		cand[i] = auditorCandidateWire{UserID: c.UserID, DisplayName: c.DisplayName, Email: c.Email}
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"audit_period_id": periodID.String(),
		"auditors":        out,
		"count":           len(out),
		"candidates":      cand,
	})
}

// Assign handles POST /v1/audit-periods/{id}/auditors. 201 on a new
// grant, 200 when the user already held it.
func (h *AuditorsHandler) Assign(w http.ResponseWriter, r *http.Request) {
	periodID, actor, ok := h.admit(w, r)
	if !ok {
		return
	}
	var req assignAuditorReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 4*1024)).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "user_id must be a UUID")
		return
	}
	created, err := h.store.AssignToPeriod(r.Context(), periodID, userID, actor)
	if err != nil {
		writeAuditorErr(w, r, "assign auditor", err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpresp.WriteJSON(w, status, map[string]any{
		"audit_period_id": periodID.String(),
		"user_id":         userID.String(),
		"assigned":        true,
	})
}

// Remove handles DELETE /v1/audit-periods/{id}/auditors/{userID}. 404
// when the user held no grant on the period.
func (h *AuditorsHandler) Remove(w http.ResponseWriter, r *http.Request) {
	periodID, actor, ok := h.admit(w, r)
	if !ok {
		return
	}
	userID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "userID must be a UUID")
		return
	}
	removed, err := h.store.RemoveFromPeriod(r.Context(), periodID, userID.String(), actor)
	if err != nil {
		writeAuditorErr(w, r, "remove auditor", err)
		return
	}
	if !removed {
		httpresp.WriteError(w, http.StatusNotFound, "auditor is not assigned to this period")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// admit checks tenant context and that the caller administers the tenant
// (platform admin or the admin role), and parses {id}. It returns the
// period id and the actor recorded on the grant and the log row: the
// signed-in person (the JWT subject) when there is one, else the
// credential id, as adminusers records granted_by.
func (h *AuditorsHandler) admit(w http.ResponseWriter, r *http.Request) (uuid.UUID, string, bool) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || cred.TenantID == "" {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return uuid.Nil, "", false
	}
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return uuid.Nil, "", false
	}
	if !authz.IsTenantAdmin(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "admin role required")
		return uuid.Nil, "", false
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return uuid.Nil, "", false
	}
	actor := cred.UserID
	if actor == "" {
		actor = cred.ID
	}
	return id, actor, true
}

func writeAuditorErr(w http.ResponseWriter, r *http.Request, op string, err error) {
	switch {
	case errors.Is(err, auditor.ErrPeriodNotFound):
		httpresp.WriteError(w, http.StatusNotFound, "audit period not found")
	case errors.Is(err, auditor.ErrNotTenantAuditor):
		httpresp.WriteError(w, http.StatusBadRequest, "user_id is not an active auditor in this tenant")
	default:
		httperr.WriteInternal(w, r, op, err)
	}
}
