// Package me serves the slice-025 /v1/me/audit-period(s) endpoints --
// the auditor's self-info surface (AC-5 + AC-6). Auditors hit these
// to discover which audit_period(s) they're assigned to and to switch
// between historical engagements.
//
// Routes (registered onto the platform root router by
// internal/api/httpserver.go):
//
//	GET /v1/me/audit-period    most-recent active assignment (AC-5)
//	GET /v1/me/audit-periods   full list of assignments (AC-6)
//
// Both endpoints scope to the caller's UserID -- the response is empty
// for non-auditor callers (who have zero auditor_assignments rows by
// construction). The handler does NOT gate on cred.OwnerRoles
// containing "auditor"; the upstream OPA middleware already enforces
// the auditor-role rule on /v1/me.
package me

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/audit/auditor"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Handler wires the /v1/me/audit-period(s) routes over a single
// auditor.Store.
type Handler struct {
	store *auditor.Store
}

// New constructs a Handler.
func New(store *auditor.Store) *Handler { return &Handler{store: store} }

// ----- wire shapes -----

type assignmentWire struct {
	AuditPeriodID      string  `json:"audit_period_id"`
	Name               string  `json:"name"`
	FrameworkVersionID string  `json:"framework_version_id"`
	PeriodStart        string  `json:"period_start"`
	PeriodEnd          string  `json:"period_end"`
	Status             string  `json:"status"`
	FrozenAt           *string `json:"frozen_at,omitempty"`
	GrantedAt          string  `json:"granted_at"`
	GrantedBy          string  `json:"granted_by"`
}

func assignmentWireFrom(a auditor.Assignment) assignmentWire {
	w := assignmentWire{
		AuditPeriodID:      a.AuditPeriodID.String(),
		Name:               a.Name,
		FrameworkVersionID: a.FrameworkVersionID.String(),
		PeriodStart:        a.PeriodStart.UTC().Format("2006-01-02"),
		PeriodEnd:          a.PeriodEnd.UTC().Format("2006-01-02"),
		Status:             a.Status,
		GrantedAt:          a.GrantedAt.UTC().Format(time.RFC3339Nano),
		GrantedBy:          a.GrantedBy,
	}
	if a.FrozenAt != nil {
		s := a.FrozenAt.UTC().Format(time.RFC3339Nano)
		w.FrozenAt = &s
	}
	return w
}

// ----- handlers -----

// AuditPeriod handles GET /v1/me/audit-period -- AC-5. Returns the
// caller's most-recently-started assignment as a single object. 404
// when the caller has no assignments.
func (h *Handler) AuditPeriod(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.authnContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if cred.UserID == "" {
		writeError(w, http.StatusUnauthorized, "user id missing on credential")
		return
	}
	rows, err := h.store.ListAssignmentsFor(ctx, cred.UserID)
	if err != nil {
		writeServerErr(w, r, "list assignments", err)
		return
	}
	if len(rows) == 0 {
		writeError(w, http.StatusNotFound, "no audit period assigned")
		return
	}
	// ListAssignmentsFor returns ORDER BY period_start DESC -- index 0 is
	// the most-recently-started assignment. AC-5 says "active period";
	// most-recent-start is the v1 interpretation.
	writeJSON(w, http.StatusOK, map[string]any{
		"audit_period": assignmentWireFrom(rows[0]),
	})
}

// AuditPeriods handles GET /v1/me/audit-periods -- AC-6. Returns the
// full list of assignments so an engagement covering multiple
// historical periods can be enumerated.
func (h *Handler) AuditPeriods(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.authnContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if cred.UserID == "" {
		writeError(w, http.StatusUnauthorized, "user id missing on credential")
		return
	}
	rows, err := h.store.ListAssignmentsFor(ctx, cred.UserID)
	if err != nil {
		writeServerErr(w, r, "list assignments", err)
		return
	}
	out := make([]assignmentWire, len(rows))
	for i, a := range rows {
		out[i] = assignmentWireFrom(a)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"audit_periods": out,
		"count":         len(out),
	})
}

// ----- helpers -----

// authnContext extracts the credential + tenant from the request
// context. Shared by every handler in this package.
func authnContext(r *http.Request) (context.Context, credstore.Credential, bool) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || cred.TenantID == "" {
		return nil, credstore.Credential{}, false
	}
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		return nil, credstore.Credential{}, false
	}
	return r.Context(), cred, true
}

// methodAuthnContext is kept as a method form for handlers that were
// originally written against `h.authnContext` (slice 025). Internally
// it delegates to the package-level authnContext.
func (h *Handler) authnContext(r *http.Request) (context.Context, credstore.Credential, bool) {
	return authnContext(r)
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
