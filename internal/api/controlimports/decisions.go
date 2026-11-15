package controlimports

import (
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
	"github.com/mgoodric/security-atlas/internal/controlimport"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// maxDecisionBody bounds an approve or reject body.
const maxDecisionBody = 16 * 1024

// WithDecisions serves approve and reject of staged rows (#1782).
func (h *Handler) WithDecisions(d controlimport.DecisionStore) *Handler {
	h.decisions = d
	return h
}

type approveBody struct {
	CandidateRank      int            `json:"candidate_rank"`
	SCFID              string         `json:"scf_id"`
	ImplementationType string         `json:"implementation_type"`
	ApplicabilityExpr  map[string]any `json:"applicability_expr"`
}

type rejectBody struct {
	Reason string `json:"reason"`
}

// duplicateResponse is the 409 body of an approval that found the row's
// organisation control ID already on a control: the error plus the
// recorded decision.
type duplicateResponse struct {
	Error string `json:"error"`
	controlimport.RowDecision
}

// Approve approves a staged row with one of its candidates
// ({"candidate_rank": n}) or any anchor of the batch's SCF release
// ({"scf_id": "IAC-06"}), creating a control in the caller's tenant.
func (h *Handler) Approve(w http.ResponseWriter, r *http.Request) {
	approver, ok := decidingPerson(w, r)
	if !ok {
		return
	}
	var body approveBody
	if !decodeDecision(w, r, &body) {
		return
	}
	d, err := h.decisions.ApproveRow(r.Context(), controlimport.ApproveRowInput{
		BatchID:            chi.URLParam(r, "id"),
		RowID:              chi.URLParam(r, "row"),
		Approver:           approver,
		CandidateRank:      body.CandidateRank,
		SCFID:              body.SCFID,
		ImplementationType: body.ImplementationType,
		ApplicabilityExpr:  body.ApplicabilityExpr,
	})
	if err != nil {
		writeDecisionError(w, r, err)
		return
	}
	if d.Status == controlimport.RowStatusDuplicate {
		httpresp.WriteJSON(w, http.StatusConflict, duplicateResponse{
			Error:       "organisation control ID " + d.ExternalID + " is already on control " + d.DuplicateOf + "; the row is marked duplicate",
			RowDecision: d,
		})
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, d)
}

// Reject rejects a staged row with a reason ({"reason": "..."}).
func (h *Handler) Reject(w http.ResponseWriter, r *http.Request) {
	approver, ok := decidingPerson(w, r)
	if !ok {
		return
	}
	var body rejectBody
	if !decodeDecision(w, r, &body) {
		return
	}
	d, err := h.decisions.RejectRow(r.Context(), controlimport.RejectRowInput{
		BatchID:  chi.URLParam(r, "id"),
		RowID:    chi.URLParam(r, "row"),
		Approver: approver,
		Reason:   body.Reason,
	})
	if err != nil {
		writeDecisionError(w, r, err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, d)
}

// decidingPerson resolves the caller to the users(id) who decides. Only a
// person approves or rejects: a machine token, an unbound API key or the
// bootstrap credential is refused, whatever its role.
func decidingPerson(w http.ResponseWriter, r *http.Request) (string, bool) {
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return "", false
	}
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "missing credential")
		return "", false
	}
	user, ok := credstore.CallerUserID(cred)
	if !ok {
		httpresp.WriteError(w, http.StatusForbidden,
			"a person must approve or reject: sign in as a user, or use an API key issued to one; a machine credential cannot decide an import row")
		return "", false
	}
	return user.String(), true
}

func decodeDecision(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxDecisionBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil && !errors.Is(err, io.EOF) {
		httpresp.WriteError(w, http.StatusBadRequest, "body must be a JSON object: "+err.Error())
		return false
	}
	return true
}

func writeDecisionError(w http.ResponseWriter, r *http.Request, err error) {
	msg := strings.TrimPrefix(err.Error(), "controlimport: ")
	switch {
	case errors.Is(err, controlimport.ErrRowNotFound), errors.Is(err, controlimport.ErrBatchNotFound):
		httpresp.WriteError(w, http.StatusNotFound, "staged row not found in this import batch")
	case errors.Is(err, controlimport.ErrRowDecided),
		errors.Is(err, controlimport.ErrBundleIDTaken),
		errors.Is(err, controlimport.ErrNoBatchRelease):
		httpresp.WriteError(w, http.StatusConflict, msg)
	case errors.Is(err, controlimport.ErrApproverRequired):
		httpresp.WriteError(w, http.StatusForbidden, msg)
	case errors.Is(err, controlimport.ErrAnchorChoice),
		errors.Is(err, controlimport.ErrCandidateNotFound),
		errors.Is(err, controlimport.ErrAnchorNotInRelease),
		errors.Is(err, controlimport.ErrInvalidApproval),
		errors.Is(err, controlimport.ErrReasonRequired):
		httpresp.WriteError(w, http.StatusBadRequest, msg)
	default:
		httperr.WriteInternal(w, r, "controlimports", err)
	}
}

// ApproveClear approves every pending row of the batch whose top candidate
// is crosswalk-derived and reaches all of the row's tested criteria, with
// that candidate. It takes no body. The response reports the rows approved,
// marked duplicate and skipped with their reasons; every other row is left
// pending.
func (h *Handler) ApproveClear(w http.ResponseWriter, r *http.Request) {
	approver, ok := decidingPerson(w, r)
	if !ok {
		return
	}
	res, err := h.decisions.ApproveClear(r.Context(), controlimport.ApproveClearInput{
		BatchID:  chi.URLParam(r, "id"),
		Approver: approver,
	})
	if err != nil {
		if errors.Is(err, controlimport.ErrBatchNotFound) {
			httpresp.WriteError(w, http.StatusNotFound, "import batch not found")
			return
		}
		writeDecisionError(w, r, err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, res)
}
