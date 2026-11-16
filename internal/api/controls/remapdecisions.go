// Issue #1776 — reviewer decisions on SCF remap proposals.
//
//	POST /v1/controls/remap-proposals/{id}/approve      {"scf_id"?, "reason"?}
//	POST /v1/controls/remap-proposals/{id}/reject       {"reason"?}
//	POST /v1/controls/remap-proposals/{id}/keep-legacy  {"reason"?}
//	POST /v1/controls/remap-proposals/approve-unchanged (no body)
//
// Approval re-anchors the control as a superseding revision; scf_id is
// required for a split or no_successor proposal. Bulk approval applies to
// open unchanged proposals only. Reject leaves the proposal revisitable;
// keep-legacy is final.
//
// Authz: resource "controls". /approve resolves to action "approve", the
// others to "write"; both are admitted for admin and grc_engineer only
// (control_owner, viewer and auditor have no write on controls). On top of
// the role, a decision is a person's: a machine credential is refused
// (approval.Check), and the decider is recorded as credstore.PrincipalOf.
// RLS keeps every read and write to the caller's tenant, so another
// tenant's proposal id is a 404.

package controls

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/approval"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// maxRemapDecisionBody bounds a decision body.
const maxRemapDecisionBody = 16 * 1024

// remapDecider is the store seam the decision handler writes through; the
// control.Store satisfies it.
type remapDecider interface {
	ApproveRemap(ctx context.Context, in control.RemapApproveInput) (control.RemapDecision, error)
	RejectRemap(ctx context.Context, in control.RemapCloseInput) (control.RemapDecision, error)
	KeepLegacyRemap(ctx context.Context, in control.RemapCloseInput) (control.RemapDecision, error)
	BulkApproveUnchangedRemaps(ctx context.Context, decidedBy string) (control.RemapBulkResult, error)
}

// RemapDecisionsHandler serves the remap proposal decisions.
type RemapDecisionsHandler struct {
	store remapDecider
}

// NewRemapDecisionsHandler wires the handler over the control store.
func NewRemapDecisionsHandler(store remapDecider) *RemapDecisionsHandler {
	return &RemapDecisionsHandler{store: store}
}

// RegisterRoutes mounts the decision routes. The literal approve-unchanged
// segment and the {id}/... routes sit under /v1/controls/remap-proposals,
// ahead of the /v1/controls/{id}/... patterns.
func (h *RemapDecisionsHandler) RegisterRoutes(r chi.Router) {
	r.Post("/v1/controls/remap-proposals/approve-unchanged", h.ApproveUnchanged)
	r.Post("/v1/controls/remap-proposals/{id}/approve", h.Approve)
	r.Post("/v1/controls/remap-proposals/{id}/reject", h.Reject)
	r.Post("/v1/controls/remap-proposals/{id}/keep-legacy", h.KeepLegacy)
}

type remapApproveBody struct {
	SCFID  string `json:"scf_id"`
	Reason string `json:"reason"`
}

type remapCloseBody struct {
	Reason string `json:"reason"`
}

// Approve handles POST /v1/controls/remap-proposals/{id}/approve.
func (h *RemapDecisionsHandler) Approve(w http.ResponseWriter, r *http.Request) {
	decider, ok := remapDecidingPerson(w, r)
	if !ok {
		return
	}
	var body remapApproveBody
	if !decodeRemapBody(w, r, &body) {
		return
	}
	d, err := h.store.ApproveRemap(r.Context(), control.RemapApproveInput{
		ProposalID: chi.URLParam(r, "id"), SCFID: body.SCFID, Reason: body.Reason, DecidedBy: decider,
	})
	if err != nil {
		writeRemapError(w, r, err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, d)
}

// Reject handles POST /v1/controls/remap-proposals/{id}/reject.
func (h *RemapDecisionsHandler) Reject(w http.ResponseWriter, r *http.Request) {
	h.close(w, r, h.store.RejectRemap)
}

// KeepLegacy handles POST /v1/controls/remap-proposals/{id}/keep-legacy.
func (h *RemapDecisionsHandler) KeepLegacy(w http.ResponseWriter, r *http.Request) {
	h.close(w, r, h.store.KeepLegacyRemap)
}

func (h *RemapDecisionsHandler) close(w http.ResponseWriter, r *http.Request,
	fn func(context.Context, control.RemapCloseInput) (control.RemapDecision, error)) {
	decider, ok := remapDecidingPerson(w, r)
	if !ok {
		return
	}
	var body remapCloseBody
	if !decodeRemapBody(w, r, &body) {
		return
	}
	d, err := fn(r.Context(), control.RemapCloseInput{
		ProposalID: chi.URLParam(r, "id"), Reason: body.Reason, DecidedBy: decider,
	})
	if err != nil {
		writeRemapError(w, r, err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, d)
}

// ApproveUnchanged handles POST /v1/controls/remap-proposals/approve-unchanged.
func (h *RemapDecisionsHandler) ApproveUnchanged(w http.ResponseWriter, r *http.Request) {
	decider, ok := remapDecidingPerson(w, r)
	if !ok {
		return
	}
	res, err := h.store.BulkApproveUnchangedRemaps(r.Context(), decider)
	if err != nil {
		writeRemapError(w, r, err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, res)
}

// remapDecidingPerson returns PrincipalOf the caller when the caller is a
// person. A machine token, an unbound API key or the bootstrap credential
// is refused whatever its role. A proposal has no requester (the import
// made it), so approval.Check applies only its people-only rule.
func remapDecidingPerson(w http.ResponseWriter, r *http.Request) (string, bool) {
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return "", false
	}
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "missing credential")
		return "", false
	}
	if _, err := approval.Check("", approval.Approver{UserID: credstore.MinterUserID(cred), CredentialID: cred.ID}, false); err != nil {
		httpresp.WriteError(w, http.StatusForbidden,
			"a person must decide a remap proposal: sign in as a user, or use an API key issued to one; a machine credential cannot")
		return "", false
	}
	return credstore.PrincipalOf(cred), true
}

func decodeRemapBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxRemapDecisionBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil && !errors.Is(err, io.EOF) {
		httpresp.WriteError(w, http.StatusBadRequest, "body must be a JSON object: "+err.Error())
		return false
	}
	if dec.More() {
		httpresp.WriteError(w, http.StatusBadRequest, "body must be a single JSON object")
		return false
	}
	return true
}

func writeRemapError(w http.ResponseWriter, r *http.Request, err error) {
	msg := strings.TrimPrefix(err.Error(), "control remap: ")
	switch {
	case errors.Is(err, control.ErrRemapProposalNotFound):
		httpresp.WriteError(w, http.StatusNotFound, "remap proposal not found")
	case errors.Is(err, control.ErrRemapProposalDecided),
		errors.Is(err, control.ErrRemapControlChanged),
		errors.Is(err, control.ErrRemapBulkRunning):
		httpresp.WriteError(w, http.StatusConflict, msg)
	case errors.Is(err, control.ErrRemapAnchorRequired),
		errors.Is(err, control.ErrRemapAnchorNotInRelease),
		errors.Is(err, control.ErrRemapReasonTooLong):
		httpresp.WriteError(w, http.StatusBadRequest, msg)
	case errors.Is(err, control.ErrRemapDeciderRequired):
		httpresp.WriteError(w, http.StatusForbidden, msg)
	default:
		httperr.WriteInternal(w, r, "scf remap decision", err)
	}
}
