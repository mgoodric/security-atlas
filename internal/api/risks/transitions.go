package risks

// Risk treatment and acceptance transitions (#2105):
//
//	PATCH /v1/risks/{id}/treatment          {treatment, instrument_reference?}
//	PATCH /v1/risks/{id}/acceptance/propose {justification, proposed_until}
//	PATCH /v1/risks/{id}/acceptance/approve {accepted_until}
//	PATCH /v1/risks/{id}/acceptance/reject  {reason?}
//	PATCH /v1/risks/{id}/acceptance/revoke  {reason}
//
// Each returns {"risk": ...}. The state machine and the approval rule live
// in internal/risk/transitions.go; these handlers decode, gate approve,
// reject and revoke on the approver role (as exceptions and policies do),
// and map refusals: 400 for input, 403 for the role or the approval rule,
// 404 for an unknown risk, 409 for a transition the risk's state does not
// allow.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/approval"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/risk"
)

// acceptanceWire is the acceptance record on a risk (#2105). The approved
// expiry and approver stay at the risk's top level as accepted_until and
// accepter.
type acceptanceWire struct {
	Justification string     `json:"justification"`
	ProposedUntil *string    `json:"proposed_until,omitempty"`
	ProposedBy    string     `json:"proposed_by,omitempty"`
	ProposedAt    *time.Time `json:"proposed_at,omitempty"`
	ApprovedAt    *time.Time `json:"approved_at,omitempty"`
	SelfApproved  bool       `json:"self_approved"`
}

func acceptanceWireFrom(a risk.Acceptance) *acceptanceWire {
	w := &acceptanceWire{
		Justification: a.Justification,
		ProposedBy:    a.ProposedBy,
		ProposedAt:    a.ProposedAt,
		ApprovedAt:    a.ApprovedAt,
		SelfApproved:  a.SelfApproved,
	}
	if a.ProposedUntil != nil {
		s := a.ProposedUntil.Format(time.DateOnly)
		w.ProposedUntil = &s
	}
	return w
}

type changeTreatmentReq struct {
	Treatment           string  `json:"treatment"`
	InstrumentReference *string `json:"instrument_reference"`
}

type proposeAcceptanceReq struct {
	Justification string `json:"justification"`
	ProposedUntil string `json:"proposed_until"`
}

type approveAcceptanceReq struct {
	AcceptedUntil string `json:"accepted_until"`
}

type endAcceptanceReq struct {
	Reason string `json:"reason"`
}

// ChangeTreatment handles PATCH /v1/risks/{id}/treatment.
func (h *Handler) ChangeTreatment(w http.ResponseWriter, r *http.Request) {
	var req changeTreatmentReq
	h.runTransition(w, r, &req, false, func(ctx transitionCtx) (risk.Risk, error) {
		if req.Treatment == "" {
			return risk.Risk{}, inputErr("treatment is required")
		}
		return h.store.ChangeTreatment(ctx.r.Context(), ctx.id, risk.ChangeTreatmentInput{
			Treatment:           dbx.RiskTreatment(req.Treatment),
			InstrumentReference: req.InstrumentReference,
			Actor:               credstore.PrincipalOf(ctx.cred),
		})
	})
}

// ProposeAcceptance handles PATCH /v1/risks/{id}/acceptance/propose.
func (h *Handler) ProposeAcceptance(w http.ResponseWriter, r *http.Request) {
	var req proposeAcceptanceReq
	h.runTransition(w, r, &req, false, func(ctx transitionCtx) (risk.Risk, error) {
		until, err := parseDate("proposed_until", req.ProposedUntil)
		if err != nil {
			return risk.Risk{}, err
		}
		return h.store.ProposeAcceptance(ctx.r.Context(), ctx.id, risk.ProposeAcceptanceInput{
			Justification: req.Justification,
			ProposedUntil: until,
			Proposer:      credstore.PrincipalOf(ctx.cred),
		})
	})
}

// ApproveAcceptance handles PATCH /v1/risks/{id}/acceptance/approve.
func (h *Handler) ApproveAcceptance(w http.ResponseWriter, r *http.Request) {
	var req approveAcceptanceReq
	h.runTransition(w, r, &req, true, func(ctx transitionCtx) (risk.Risk, error) {
		until, err := parseDate("accepted_until", req.AcceptedUntil)
		if err != nil {
			return risk.Risk{}, err
		}
		// Only a person approves (#2101): MinterUserID is empty for an
		// unbound key, an OAuth client or the bootstrap admin.
		return h.store.ApproveAcceptance(ctx.r.Context(), ctx.id, risk.ApproveAcceptanceInput{
			AcceptedUntil: until,
			Approver: approval.Approver{
				UserID:       credstore.MinterUserID(ctx.cred),
				CredentialID: ctx.cred.ID,
			},
		})
	})
}

// RejectAcceptance handles PATCH /v1/risks/{id}/acceptance/reject.
func (h *Handler) RejectAcceptance(w http.ResponseWriter, r *http.Request) {
	var req endAcceptanceReq
	h.runTransition(w, r, &req, true, func(ctx transitionCtx) (risk.Risk, error) {
		return h.store.RejectAcceptance(ctx.r.Context(), ctx.id, risk.EndAcceptanceInput{
			Reason: req.Reason,
			Actor:  credstore.PrincipalOf(ctx.cred),
		})
	})
}

// RevokeAcceptance handles PATCH /v1/risks/{id}/acceptance/revoke.
func (h *Handler) RevokeAcceptance(w http.ResponseWriter, r *http.Request) {
	var req endAcceptanceReq
	h.runTransition(w, r, &req, true, func(ctx transitionCtx) (risk.Risk, error) {
		return h.store.RevokeAcceptance(ctx.r.Context(), ctx.id, risk.EndAcceptanceInput{
			Reason: req.Reason,
			Actor:  credstore.PrincipalOf(ctx.cred),
		})
	})
}

type transitionCtx struct {
	r    *http.Request
	id   uuid.UUID
	cred credstore.Credential
}

// runTransition is the shared shape of the five handlers: resolve the
// tenant and credential, gate on the approver role when needsApprover,
// parse the id and an optional JSON body into req, run fn and render.
func (h *Handler) runTransition(w http.ResponseWriter, r *http.Request, req any, needsApprover bool, fn func(transitionCtx) (risk.Risk, error)) {
	if _, ok := h.tenantContext(r); !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "credential missing")
		return
	}
	if needsApprover && !cred.IsApprover && !cred.IsAdmin {
		httpresp.WriteError(w, http.StatusForbidden, "approver role required")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	if r.ContentLength != 0 {
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(req); err != nil {
			httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
			return
		}
	}
	out, err := fn(transitionCtx{r: r, id: id, cred: cred})
	if err != nil {
		writeTransitionErr(w, r, err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"risk": riskWireFrom(out)})
}

func writeTransitionErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, risk.ErrNotFound):
		httpresp.WriteError(w, http.StatusNotFound, "risk not found")
	case risk.IsTransitionInput(err):
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
	case risk.IsApprovalRefusal(err):
		httpresp.WriteError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, risk.ErrTransitionState):
		httpresp.WriteError(w, http.StatusConflict, err.Error())
	default:
		httperr.WriteInternal(w, r, "risk transition", err)
	}
}

func inputErr(msg string) error {
	return &transitionInputErr{msg: msg}
}

type transitionInputErr struct{ msg string }

func (e *transitionInputErr) Error() string { return e.msg }
func (e *transitionInputErr) Unwrap() error { return risk.ErrTransitionInput }

// parseDate reads a YYYY-MM-DD field; empty is left to the store, which
// reports the field as required.
func parseDate(field, raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.DateOnly, raw)
	if err != nil {
		return time.Time{}, inputErr(field + " must be YYYY-MM-DD")
	}
	return t, nil
}
