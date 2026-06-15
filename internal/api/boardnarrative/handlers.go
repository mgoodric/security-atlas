// Package boardnarrative wires the slice-440 board-narrative AI v0 HTTP
// surface: generate a cited, numeric-verified, shape-and-tone-enforced DRAFT of
// the control-coverage-summary section, and approve it one click at a time.
//
// Routes (registered onto the platform root chi router by httpserver.go via the
// Mount-append convention — chi rejects two Mounts at "/"):
//
//	POST /v1/board/narrative/generate   generate the coverage-section DRAFT
//	POST /v1/board/narrative/approve    one-click approve a section (records approver)
//
// Both routes are ROLE-GATED: board narratives are an admin / grc_engineer
// (IsApprover) capability (AC-11, threat-model S). A bare read role cannot
// generate a draft that becomes a board-facing artifact, nor approve one.
//
// The constitutional invariants (no fabricated coverage, no fabricated numbers,
// no cross-tenant bleed, one-click approval, local-only) are enforced by the
// boardnarrative.Service + the DB layer; this handler is the thin role-gated
// HTTP edge.
package boardnarrative

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/boardnarrative"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Handler serves the board-narrative AI routes over a boardnarrative.Service.
type Handler struct {
	svc *boardnarrative.Service
}

// New constructs a Handler. A nil service is tolerated (the routes return 503)
// so a deployment without AI-assist enabled still mounts cleanly.
func New(svc *boardnarrative.Service) *Handler {
	return &Handler{svc: svc}
}

// RegisterRoutes appends the board-narrative AI routes onto the supplied chi
// router (the Mount-append convention — chi panics on duplicate Mount at "/").
func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Post("/v1/board/narrative/generate", h.Generate)
	r.Post("/v1/board/narrative/approve", h.Approve)
}

type generateRequest struct {
	PeriodEnd string `json:"period_end"`
}

// Generate produces a validated, cited DRAFT of the control-coverage-summary
// section. The draft is persisted ai_assisted=TRUE, human_approved=FALSE (NOT a
// board-binding artifact; not approved). The four guardrail gates (citation /
// numeric / shape / tone) are enforced server-side BEFORE this returns a draft;
// a suppressed result carries a fixed reason and persists nothing (P0-440-4).
func (h *Handler) Generate(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.tenantCred(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if !cred.IsApprover && !cred.IsAdmin {
		httpresp.WriteError(w, http.StatusForbidden, "grc_engineer role required to generate a board-narrative section")
		return
	}
	if h.svc == nil {
		httpresp.WriteError(w, http.StatusServiceUnavailable, "board-narrative AI is not enabled on this deployment")
		return
	}
	var req generateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "request body must be JSON")
		return
	}
	if req.PeriodEnd == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "period_end is required (YYYY-MM-DD)")
		return
	}
	out, err := h.svc.Generate(ctx, boardnarrative.GenerateParams{
		PeriodEnd:  req.PeriodEnd,
		AuthoredBy: cred.ID,
	})
	if err != nil {
		if errors.Is(err, boardnarrative.ErrNoBriefData) {
			httpresp.WriteError(w, http.StatusUnprocessableEntity, "no program posture data to summarize for this period")
			return
		}
		httperr.WriteInternal(w, r, "board-narrative", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, out)
}

type approveRequest struct {
	RecordID  string `json:"record_id"`
	FinalText string `json:"final_text"`
}

// Approve is the one-click human approval of a section draft (guardrail 2 —
// per section). It records the approver + flips human_approved=TRUE; the
// operator's edited final text is what ships into the board pack. There is NO
// auto-approve path — this endpoint is the ONLY way an AI-drafted section
// becomes approved. The DB CHECK makes human_approved=TRUE without a
// human_approver impossible (P0-440-2).
func (h *Handler) Approve(w http.ResponseWriter, r *http.Request) {
	ctx, cred, ok := h.tenantCred(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	if !cred.IsApprover && !cred.IsAdmin {
		httpresp.WriteError(w, http.StatusForbidden, "grc_engineer role required to approve a board-narrative section")
		return
	}
	if h.svc == nil {
		httpresp.WriteError(w, http.StatusServiceUnavailable, "board-narrative AI is not enabled on this deployment")
		return
	}
	var req approveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "request body must be JSON")
		return
	}
	recordID, err := uuid.Parse(req.RecordID)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "record_id must be a uuid")
		return
	}
	approved, err := h.svc.Approve(ctx, boardnarrative.ApproveParams{
		RecordID:  recordID,
		FinalText: req.FinalText,
		// The approver is the authenticated credential — NEVER client-supplied
		// (a caller cannot approve "as" someone else).
		Approver: cred.ID,
	})
	if err != nil {
		switch {
		case errors.Is(err, boardnarrative.ErrApproverRequired):
			httpresp.WriteError(w, http.StatusBadRequest, "approver is required")
		case errors.Is(err, boardnarrative.ErrRecordNotFound):
			httpresp.WriteError(w, http.StatusNotFound, "board-narrative section not found")
		default:
			httperr.WriteInternal(w, r, "board-narrative", err)
		}
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, approved)
}

// tenantCred resolves the tenant context + the authenticated credential. Both
// must be present for the role-gated AI routes. Mirrors
// questionnaires.Handler.tenantCred.
func (h *Handler) tenantCred(r *http.Request) (context.Context, credstore.Credential, bool) {
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		return nil, credstore.Credential{}, false
	}
	c, found := authctx.CredentialFromContext(r.Context())
	if !found || c.TenantID == "" {
		return nil, credstore.Credential{}, false
	}
	return r.Context(), c, true
}
