// Slice 053 manual-aggregation handlers, appended to the existing slice-019
// risks router. Two routes:
//
//	POST /v1/risks/aggregate         create parent + link children
//	GET  /v1/risks/{id}/aggregation  live recompute of severity
//
// Tenant from app.current_tenant (slice-033 middleware). No app-level
// tenant_id filters.

package risks

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/risk"
)

type aggregateReqParent struct {
	Title            string  `json:"title"`
	Level            string  `json:"level"`
	OrgUnitID        *string `json:"org_unit_id,omitempty"`
	SeverityFunction string  `json:"severity_function"`
}

type aggregateReq struct {
	Parent       aggregateReqParent `json:"parent"`
	ChildRiskIDs []string           `json:"child_risk_ids"`
}

type aggregationWire struct {
	ParentID         string   `json:"parent_id"`
	ParentTitle      string   `json:"parent_title"`
	ParentLevel      string   `json:"parent_level"`
	SeverityFunction string   `json:"severity_function"`
	Severity         int      `json:"severity"`
	Likelihood       int      `json:"likelihood"`
	Impact           int      `json:"impact"`
	ChildCount       int      `json:"child_count"`
	LinkedChildren   []string `json:"linked_children"`
	AggregationKey   string   `json:"aggregation_key"`
	Themes           []string `json:"themes"`
}

// Aggregate handles POST /v1/risks/aggregate (AC-5, AC-6, AC-7, AC-10).
func (h *Handler) Aggregate(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	var req aggregateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Parent.Title == "" {
		writeError(w, http.StatusBadRequest, "parent.title is required")
		return
	}
	childIDs, err := parseUUIDs(req.ChildRiskIDs)
	if err != nil {
		writeError(w, http.StatusBadRequest, "child_risk_ids: "+err.Error())
		return
	}
	if len(childIDs) == 0 {
		writeError(w, http.StatusBadRequest, "child_risk_ids must contain at least one UUID")
		return
	}
	var orgUnitID *uuid.UUID
	if req.Parent.OrgUnitID != nil && *req.Parent.OrgUnitID != "" {
		u, perr := uuid.Parse(*req.Parent.OrgUnitID)
		if perr != nil {
			writeError(w, http.StatusBadRequest, "parent.org_unit_id must be a UUID")
			return
		}
		orgUnitID = &u
	}
	in := risk.AggregateInput{
		ParentTitle:      req.Parent.Title,
		ParentLevel:      dbx.RiskLevel(req.Parent.Level),
		ParentOrgUnitID:  orgUnitID,
		SeverityFunction: risk.SeverityFunction(req.Parent.SeverityFunction),
		ChildRiskIDs:     childIDs,
	}
	res, err := h.store.Aggregate(ctx, in)
	if err != nil {
		switch {
		case errors.Is(err, risk.ErrChildrenNotFound):
			// AC-10: cross-tenant child id (or missing id) -> 404 with a
			// non-enumerating message.
			writeError(w, http.StatusNotFound, "one or more child risks not found")
		case errors.Is(err, risk.ErrEmptyChildren),
			errors.Is(err, risk.ErrUnknownSeverityFunction),
			errors.Is(err, risk.ErrIncompatibleMethodology),
			errors.Is(err, risk.ErrInvalidLevel):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			writeServerErr(w, "aggregate", err)
		}
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"aggregation": aggregationWireFrom(res),
		"risk":        riskWireFrom(res.Parent),
	})
}

// LiveAggregation handles GET /v1/risks/{id}/aggregation (AC-8, AC-9).
// Recomputes the parent severity from the current set of child risks. The
// stored value on the parent's inherent_score is the historical/initial
// severity; the live recompute is the truth after children open/close.
func (h *Handler) LiveAggregation(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	res, err := h.store.LiveAggregation(ctx, id)
	if err != nil {
		if errors.Is(err, risk.ErrNotFound) {
			writeError(w, http.StatusNotFound, "aggregation parent not found")
			return
		}
		writeServerErr(w, "live aggregation", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"aggregation": aggregationWireFrom(res),
	})
}

func aggregationWireFrom(res risk.AggregateResult) aggregationWire {
	linked := make([]string, len(res.LinkedChildren))
	for i, c := range res.LinkedChildren {
		linked[i] = c.String()
	}
	return aggregationWire{
		ParentID:         res.Parent.ID.String(),
		ParentTitle:      res.Parent.Title,
		ParentLevel:      string(res.Parent.Level),
		SeverityFunction: string(res.SeverityFunction),
		Severity:         res.Severity,
		Likelihood:       res.Likelihood,
		Impact:           res.Impact,
		ChildCount:       res.ChildCount,
		LinkedChildren:   linked,
		AggregationKey:   res.AggregationKey,
		Themes:           append([]string(nil), res.Parent.Themes...),
	}
}
