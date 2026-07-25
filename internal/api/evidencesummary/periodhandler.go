package evidencesummary

// Slice 749 — period-scoped evidence-summary read endpoint:
//
//	GET /v1/audit-periods/{id}/controls/{controlID}/evidence-summary
//
// It is the audit-workspace sibling of the slice-502 control-detail
// /v1/controls/{id}/evidence-summary surface served above. For ONE control
// WITHIN one FROZEN audit period it returns the DETERMINISTIC bounded
// FROZEN-population evidence set ALWAYS (observed_at <= frozen_at — invariant
// #10, P0-749-1), plus a plain-language, cited, NON-BINDING AI summary of that
// frozen evidence when the per-tenant inference client is available AND every
// citation resolves to a tenant-owned row WITHIN the frozen population (AC-2).
//
// The summary is a comprehension aid in the AUDIT WORKSPACE — it is a
// comprehension aid OVER the frozen sample, never a new sample-population source,
// never an audit-binding artifact: no approve/publish/export path (P0-502-3,
// AC-4), regenerated on demand (never persisted, P0-502-4). It is clearly labeled
// period-scoped + frozen-as-of `frozen_at`. The endpoint sits behind the SAME
// audit-workspace authz as the slice-028 control-state read (admin or
// grc_engineer) — no new role. When the summary is unavailable or its citations
// fail to resolve, the handler still returns the frozen evidence set with
// summary=null (graceful degradation, AC-7, P0-502-7).

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/evidencesummary"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// periodSummarizer is the period handler's read seam — exactly the one method
// the route needs. The production *evidencesummary.PeriodService satisfies it;
// tests inject a fake to drive the wire shape without a model.
type periodSummarizer interface {
	PeriodSummarize(ctx context.Context, controlID, auditPeriodID uuid.UUID) (evidencesummary.PeriodSummary, error)
}

// PeriodHandler serves the single period-scoped evidence-summary route over an
// evidencesummary.PeriodService. It holds no write surface.
type PeriodHandler struct {
	svc periodSummarizer
}

// NewPeriod constructs a PeriodHandler over the period-scoped evidence-summary
// service.
func NewPeriod(svc *evidencesummary.PeriodService) *PeriodHandler { return &PeriodHandler{svc: svc} }

// newPeriodHandlerWith constructs a PeriodHandler over an arbitrary
// periodSummarizer seam. For tests only — unexported, not part of the public
// surface.
func newPeriodHandlerWith(svc periodSummarizer) *PeriodHandler { return &PeriodHandler{svc: svc} }

// periodEvidenceSetWire is the deterministic bounded FROZEN-population evidence
// set — ALWAYS present (AC-7). `frozen` marks it as a frozen audit-period
// population (NOT current live evidence); `frozen_at` is the freeze horizon the
// corpus is bounded by (observed_at <= frozen_at — invariant #10), surfaced for
// the period-scoped + frozen-as-of UI label (AC-4). `showing` / `total` carry the
// bound.
type periodEvidenceSetWire struct {
	ControlID     string             `json:"control_id"`
	ControlTitle  string             `json:"control_title"`
	AuditPeriodID string             `json:"audit_period_id"`
	FrozenAt      string             `json:"frozen_at"`
	Frozen        bool               `json:"frozen"`
	Showing       int                `json:"showing"`
	Total         int                `json:"total"`
	Records       []evidenceFactWire `json:"records"`
}

// PeriodEvidenceSummary handles
// GET /v1/audit-periods/{id}/controls/{controlID}/evidence-summary. The frozen
// evidence set is always rendered; the summary is rendered only when present and
// non-suppressed. On suppression the response carries summary=null plus a fixed
// suppression reason so the frontend can show the frozen evidence list alone
// (AC-7).
func (h *PeriodHandler) PeriodEvidenceSummary(w http.ResponseWriter, r *http.Request) {
	if !requireAuditWorkspaceRead(w, r) {
		return
	}
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	periodID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "audit period id must be a uuid")
		return
	}
	controlID, err := uuid.Parse(chi.URLParam(r, "controlID"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "control id must be a uuid")
		return
	}

	sum, err := h.svc.PeriodSummarize(r.Context(), controlID, periodID)
	if err != nil {
		switch {
		case errors.Is(err, evidencesummary.ErrNoPeriod):
			httpresp.WriteError(w, http.StatusNotFound, "audit period not found")
		case errors.Is(err, evidencesummary.ErrNoControl):
			httpresp.WriteError(w, http.StatusNotFound, "control not found")
		case errors.Is(err, evidencesummary.ErrPeriodNotFrozen):
			// An open period has no frozen population to summarize; the operator
			// should use the live control-detail summary instead (P0-749-1: never
			// mix live + frozen). 409 Conflict mirrors the freeze-state-mismatch
			// shape used by the freeze endpoint.
			httpresp.WriteError(w, http.StatusConflict, "audit period is not frozen")
		default:
			httperr.WriteInternal(w, r, "period-evidencesummary", err)
		}
		return
	}

	body := map[string]any{
		"audit_period_id":   periodID.String(),
		"control_id":        controlID.String(),
		"evidence":          periodEvidenceSetWireFrom(sum),
		"suppressed_reason": sum.Reason,
	}
	if sum.Suppressed || sum.Text == "" {
		// Graceful degradation: frozen evidence list only, summary withheld (AC-7).
		body["summary"] = nil
	} else {
		body["summary"] = summaryWireFrom(sum.Summary)
	}
	httpresp.WriteJSON(w, http.StatusOK, body)
}

func periodEvidenceSetWireFrom(sum evidencesummary.PeriodSummary) periodEvidenceSetWire {
	set := sum.EvidenceSet
	out := periodEvidenceSetWire{
		ControlID:     set.ControlID.String(),
		ControlTitle:  set.ControlTitle,
		AuditPeriodID: sum.AuditPeriodID.String(),
		FrozenAt:      sum.FrozenAt.UTC().Format(time.RFC3339),
		Frozen:        true, // P0-749-1: frozen audit-period population, clearly labeled.
		Showing:       len(set.Records),
		Total:         set.TotalCount,
		Records:       make([]evidenceFactWire, 0, len(set.Records)),
	}
	for _, e := range set.Records {
		out.Records = append(out.Records, evidenceFactWire{
			EvidenceID:   e.EvidenceID.String(),
			EvidenceKind: e.EvidenceKind,
			Result:       e.Result,
			ObservedAt:   e.ObservedAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}

// ===== authz: audit-workspace read guard (defense-in-depth) =====

// hasAuditWorkspaceRead mirrors auditperiods.canWrite — the audit-workspace role
// set is admin (wildcard) OR grc_engineer (an owner role). The slice-035 OPA
// middleware is the primary gate in production; this handler-level guard is the
// testable enforcement point when OPA is not wired (unit/integration servers).
// The period-scoped summary reuses the SAME audit-workspace authz as the
// slice-028 control-state read — no new role (AC-4 scope discipline).
func hasAuditWorkspaceRead(c credstore.Credential) bool {
	if c.IsAdmin {
		return true
	}
	for _, role := range c.OwnerRoles {
		if role == "grc_engineer" {
			return true
		}
	}
	return false
}

// requireAuditWorkspaceRead is the guard the period handler calls FIRST. It
// returns true when the request may proceed; on denial it writes a 403 and
// returns false.
func requireAuditWorkspaceRead(w http.ResponseWriter, r *http.Request) bool {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || !hasAuditWorkspaceRead(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "role does not grant audit-workspace access")
		return false
	}
	return true
}
