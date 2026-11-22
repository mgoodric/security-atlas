// link.go — slice 020 risk-control linkage HTTP surface.
//
// Routes (appended onto the platform root router by httpserver.go):
//
//	POST   /v1/risks/{id}/controls               AC-1: link a control to a risk
//	DELETE /v1/risks/{id}/controls/{control_id}  #2106: unlink one
//
// Both refuse a closed risk (409) and write a risk_audit_log row (#2106).
// Unlinking the last control of a mitigate risk with no live action plan
// is refused with a 400 (treatment validation).
//
// AC-2's residual + effectiveness breakdown is served by the slice-019
// GET /v1/risks/{id} handler, extended in handlers.go to call the
// ResidualDeriver when one is attached.
package risks

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/risk"
)

// linkControlReq is the POST /v1/risks/{id}/controls body. ControlID is
// required; the three weights + design_score are optional — when omitted the
// migration `_029` column DEFAULTs apply (design 0.5, weights 0.3/0.5/0.2).
type linkControlReq struct {
	ControlID       string   `json:"control_id"`
	DesignScore     *float64 `json:"design_score,omitempty"`
	WeightDesign    *float64 `json:"weight_design,omitempty"`
	WeightOperation *float64 `json:"weight_operation,omitempty"`
	WeightCoverage  *float64 `json:"weight_coverage,omitempty"`
}

// LinkControl handles POST /v1/risks/{id}/controls (AC-1). On success it
// returns the risk with its updated linked_control_ids[] and, when the
// deriver is wired, the freshly-recomputed residual.
func (h *Handler) LinkControl(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "credential missing")
		return
	}
	riskID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	var req linkControlReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	controlID, err := uuid.Parse(req.ControlID)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "control_id must be a UUID")
		return
	}

	in := risk.LinkControlInput{
		RiskID:    riskID,
		ControlID: controlID,
		Actor:     credstore.PrincipalOf(cred),
	}
	if !applyWeight(w, "design_score", req.DesignScore, &in.DesignScore, &in.DesignScoreSet) {
		return
	}
	if !applyWeight(w, "weight_design", req.WeightDesign, &in.WeightDesign, &in.WeightsSet) {
		return
	}
	if !applyWeight(w, "weight_operation", req.WeightOperation, &in.WeightOperation, &in.WeightsSet) {
		return
	}
	if !applyWeight(w, "weight_coverage", req.WeightCoverage, &in.WeightCoverage, &in.WeightsSet) {
		return
	}

	if err := h.store.LinkControl(ctx, in); err != nil {
		switch {
		case errors.Is(err, risk.ErrNotFound):
			// AC-1: linking on an unknown risk -> 404.
			httpresp.WriteError(w, http.StatusNotFound, "risk not found")
		case errors.Is(err, risk.ErrControlNotFound):
			// AC-1: linking an unknown control -> 404.
			httpresp.WriteError(w, http.StatusNotFound, "control not found")
		case errors.Is(err, risk.ErrLinkWeightOutOfRange), risk.IsTransitionInput(err):
			httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, risk.ErrTransitionState):
			httpresp.WriteError(w, http.StatusConflict, err.Error())
		default:
			httperr.WriteInternal(w, r, "link control", err)
		}
		return
	}
	h.writeRiskAfterLinkChange(w, r, riskID)
}

// UnlinkControl handles DELETE /v1/risks/{id}/controls/{control_id}
// (#2106). It returns the risk as LinkControl does: the remaining
// linked_control_ids[] and, when the deriver is wired, the recomputed
// residual.
func (h *Handler) UnlinkControl(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "credential missing")
		return
	}
	riskID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	controlID, err := uuid.Parse(chi.URLParam(r, "control_id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "control_id must be a UUID")
		return
	}
	if err := h.store.UnlinkControl(ctx, risk.UnlinkControlInput{
		RiskID:    riskID,
		ControlID: controlID,
		Actor:     credstore.PrincipalOf(cred),
	}); err != nil {
		switch {
		case errors.Is(err, risk.ErrNotFound):
			httpresp.WriteError(w, http.StatusNotFound, "risk not found")
		case errors.Is(err, risk.ErrControlNotLinked):
			httpresp.WriteError(w, http.StatusNotFound, "control is not linked to this risk")
		case risk.IsTransitionInput(err):
			httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, risk.ErrTransitionState):
			httpresp.WriteError(w, http.StatusConflict, err.Error())
		default:
			httperr.WriteInternal(w, r, "unlink control", err)
		}
		return
	}
	h.writeRiskAfterLinkChange(w, r, riskID)
}

// writeRiskAfterLinkChange renders the risk after a link or unlink.
func (h *Handler) writeRiskAfterLinkChange(w http.ResponseWriter, r *http.Request, riskID uuid.UUID) {
	ctx, _ := h.tenantContext(r)
	// Re-read the risk so linked_control_ids[] reflects the change (AC-1).
	rk, err := h.store.Get(ctx, riskID)
	if err != nil {
		httperr.WriteInternal(w, r, "get risk after link", err)
		return
	}
	body := map[string]any{"risk": riskWireFrom(rk)}
	// When the deriver is wired, recompute + persist the residual now so the
	// link response already carries the up-to-date residual (the NATS
	// subscriber is the steady-state path; this is the immediate one).
	if h.deriver != nil {
		res, derr := h.deriver.DeriveAndPersist(ctx, riskID, false)
		if derr != nil {
			httperr.WriteInternal(w, r, "derive residual after link", derr)
			return
		}
		body["residual"] = residualWireFrom(res)
	}
	httpresp.WriteJSON(w, http.StatusOK, body)
}

// applyWeight validates an optional [0,1] weight from the request body and
// copies it into the store input. Returns false (after writing a 400) when
// the value is out of range. `set` is flipped true when the value is present.
func applyWeight(w http.ResponseWriter, field string, val *float64, dst *float64, set *bool) bool {
	if val == nil {
		return true
	}
	if *val < 0 || *val > 1 {
		httpresp.WriteError(w, http.StatusBadRequest, field+" must be between 0 and 1")
		return false
	}
	*dst = *val
	*set = true
	return true
}

// ----- residual wire shapes (AC-2) -----

type controlBreakdownWire struct {
	ControlID            string  `json:"control_id"`
	DesignScore          float64 `json:"design_score"`
	OperationalScore     float64 `json:"operational_score"`
	CoverageScore        float64 `json:"coverage_score"`
	WeightDesign         float64 `json:"weight_design"`
	WeightOperation      float64 `json:"weight_operation"`
	WeightCoverage       float64 `json:"weight_coverage"`
	ControlEffectiveness float64 `json:"control_effectiveness"`
	OperationalNoData    bool    `json:"operational_no_data"`
}

// residualWire is the derived residual. An unscored risk (#2511) has
// Unscored true and null inherent and residual scores (#2867).
type residualWire struct {
	InherentScore                *float64               `json:"inherent_score"`
	ResidualScore                *float64               `json:"residual_score"`
	WeightedControlEffectiveness float64                `json:"weighted_control_effectiveness"`
	Breakdown                    []controlBreakdownWire `json:"effectiveness_breakdown"`
	Warning                      string                 `json:"warning,omitempty"`
	Unscored                     bool                   `json:"unscored,omitempty"`
}

func residualWireFrom(res risk.ResidualResult) residualWire {
	bd := make([]controlBreakdownWire, len(res.Breakdown))
	for i, b := range res.Breakdown {
		bd[i] = controlBreakdownWire{
			ControlID:            b.ControlID.String(),
			DesignScore:          b.DesignScore,
			OperationalScore:     b.OperationalScore,
			CoverageScore:        b.CoverageScore,
			WeightDesign:         b.WeightDesign,
			WeightOperation:      b.WeightOperation,
			WeightCoverage:       b.WeightCoverage,
			ControlEffectiveness: b.ControlEffectiveness,
			OperationalNoData:    b.OperationalNoData,
		}
	}
	wire := residualWire{
		WeightedControlEffectiveness: res.WeightedControlEffectiveness,
		Breakdown:                    bd,
		Warning:                      res.Warning,
		Unscored:                     res.Unscored,
	}
	if !res.Unscored {
		wire.InherentScore, wire.ResidualScore = &res.InherentScore, &res.ResidualScore
	}
	return wire
}
