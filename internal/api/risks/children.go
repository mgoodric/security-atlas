// children.go — risk hierarchy links (#1748). Routes:
//
//	GET    /v1/risks/{id}/children             the parent's children + roll-up
//	POST   /v1/risks/{id}/children             attach existing risks as children
//	DELETE /v1/risks/{id}/children/{child_id}  detach one child
//	GET    /v1/risks/{id}/parents              the parents a risk rolls up to
//
// Linking never changes the parent's stored score or treatment. The
// roll-up is computed on read (risk.ComputeRollUp) and sits beside the
// assessed score, also on GET /v1/risks/{id}.
//
// Status codes: 400 for a level or methodology violation, 409 for a link
// that would close a cycle, 404 for a risk that does not resolve in the
// tenant (RLS hides another tenant's rows) or a child that is not linked.
package risks

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/risk"
)

type linkChildrenReq struct {
	ChildRiskIDs []string `json:"child_risk_ids"`
}

// linkedRiskWire is one entry of a children or parents listing.
type linkedRiskWire struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Level     string  `json:"level"`
	OrgUnitID *string `json:"org_unit_id,omitempty"`
	Severity  int     `json:"severity"`
	// LinkSource is manual (made by hand) or rule (made by an
	// aggregation rule, named by RuleID).
	LinkSource string    `json:"link_source"`
	RuleID     *string   `json:"rule_id,omitempty"`
	LinkedAt   time.Time `json:"linked_at"`
}

// rollUpWire is a parent's severity computed from its children, beside its
// own assessed score.
type rollUpWire struct {
	Severity         int    `json:"severity"`
	SeverityFunction string `json:"severity_function"`
	ChildCount       int    `json:"child_count"`
	AssessedSeverity int    `json:"assessed_severity"`
	ExceedsAssessed  bool   `json:"exceeds_assessed"`
}

// ListChildren handles GET /v1/risks/{id}/children.
func (h *Handler) ListChildren(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.tenantContext(r); !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	h.writeChildren(w, r, id, http.StatusOK)
}

// LinkChildren handles POST /v1/risks/{id}/children. Re-attaching a child
// that is already linked is a no-op. The response is the parent's full
// children listing after the link.
func (h *Handler) LinkChildren(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	var req linkChildrenReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	childIDs, err := parseUUIDs(req.ChildRiskIDs)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "child_risk_ids: "+err.Error())
		return
	}
	if len(childIDs) == 0 {
		httpresp.WriteError(w, http.StatusBadRequest, "child_risk_ids must contain at least one UUID")
		return
	}
	if err := h.store.LinkChildren(ctx, id, childIDs); err != nil {
		writeLinkError(w, r, err)
		return
	}
	h.writeChildren(w, r, id, http.StatusOK)
}

// UnlinkChild handles DELETE /v1/risks/{id}/children/{child_id}.
func (h *Handler) UnlinkChild(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	childID, err := uuid.Parse(chi.URLParam(r, "child_id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "child_id must be a UUID")
		return
	}
	if err := h.store.UnlinkChild(ctx, id, childID); err != nil {
		writeLinkError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListParents handles GET /v1/risks/{id}/parents.
func (h *Handler) ListParents(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	parents, err := h.store.ListParents(ctx, id)
	if err != nil {
		writeLinkError(w, r, err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"parents": linkedRiskWires(parents),
		"count":   len(parents),
	})
}

func (h *Handler) writeChildren(w http.ResponseWriter, r *http.Request, id uuid.UUID, status int) {
	children, rollUp, err := h.store.ListChildren(r.Context(), id)
	if err != nil {
		writeLinkError(w, r, err)
		return
	}
	body := map[string]any{
		"children": linkedRiskWires(children),
		"count":    len(children),
	}
	if rollUp != nil {
		body["rollup"] = rollUpWireFrom(*rollUp)
	}
	httpresp.WriteJSON(w, status, body)
}

func writeLinkError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, risk.ErrNotFound):
		httpresp.WriteError(w, http.StatusNotFound, "risk not found")
	case errors.Is(err, risk.ErrChildrenNotFound):
		// Non-enumerating, as POST /v1/risks/aggregate: a foreign id is
		// invisible under RLS and must not be confirmed to exist.
		httpresp.WriteError(w, http.StatusNotFound, "one or more child risks not found")
	case errors.Is(err, risk.ErrLinkNotFound):
		httpresp.WriteError(w, http.StatusNotFound, "child risk is not linked to this parent")
	case errors.Is(err, risk.ErrAggregationCycle):
		httpresp.WriteError(w, http.StatusConflict, err.Error())
	case risk.IsChildLinkValidation(err):
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
	default:
		httperr.WriteInternal(w, r, "risk hierarchy link", err)
	}
}

func linkedRiskWires(in []risk.LinkedRisk) []linkedRiskWire {
	out := make([]linkedRiskWire, len(in))
	for i, l := range in {
		lw := linkedRiskWire{
			ID:         l.ID.String(),
			Title:      l.Title,
			Level:      string(l.Level),
			Severity:   l.Severity,
			LinkSource: l.Source(),
			LinkedAt:   l.LinkedAt,
		}
		if l.OrgUnitID != nil {
			s := l.OrgUnitID.String()
			lw.OrgUnitID = &s
		}
		if l.RuleID != nil {
			s := l.RuleID.String()
			lw.RuleID = &s
		}
		out[i] = lw
	}
	return out
}

func rollUpWireFrom(r risk.RollUp) rollUpWire {
	return rollUpWire{
		Severity:         r.Severity,
		SeverityFunction: string(r.Function),
		ChildCount:       r.ChildCount,
		AssessedSeverity: r.AssessedSeverity,
		ExceedsAssessed:  r.ExceedsAssessed,
	}
}
