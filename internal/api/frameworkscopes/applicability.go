package frameworkscopes

// Applicability decisions (issue #1787, Statement of Applicability #1744).
// Routes:
//
//	GET /v1/framework-scopes/{id}/applicability                    — list a revision's decisions
//	PUT /v1/framework-scopes/{id}/applicability/{requirement_id}   — set one decision
//	GET /v1/framework-scopes/applicability?framework_version=…&as_of=… — decisions in force at as_of
//
// Every requirement of the framework version is listed; one with no decision
// is `undecided` (which counts as applicable) and is tallied in counts.
// Writes use the same gate as the predicate PATCH: any tenant credential may
// edit a draft, and an edit on a revision in review or approved sends it back
// to draft (approval_invalidated: true).

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/frameworkscope"
)

type setDecisionReq struct {
	Decision          string `json:"decision"`
	Justification     string `json:"justification"`
	ImplementationRef string `json:"implementation_ref"`
}

type decisionWire struct {
	RequirementID     string    `json:"requirement_id"`
	Decision          string    `json:"decision"`
	Justification     string    `json:"justification"`
	ImplementationRef string    `json:"implementation_ref"`
	UpdatedBy         string    `json:"updated_by"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type requirementWire struct {
	RequirementID     string     `json:"requirement_id"`
	Code              string     `json:"code"`
	Title             string     `json:"title"`
	Decision          *string    `json:"decision"`
	Undecided         bool       `json:"undecided"`
	Justification     string     `json:"justification"`
	ImplementationRef string     `json:"implementation_ref"`
	UpdatedBy         *string    `json:"updated_by,omitempty"`
	UpdatedAt         *time.Time `json:"updated_at,omitempty"`
}

type countsWire struct {
	Total      int `json:"total"`
	Applicable int `json:"applicable"`
	Excluded   int `json:"excluded"`
	Undecided  int `json:"undecided"`
}

func toDecisionWire(d frameworkscope.Decision) decisionWire {
	return decisionWire{
		RequirementID:     d.RequirementID.String(),
		Decision:          d.Decision,
		Justification:     d.Justification,
		ImplementationRef: d.ImplementationRef,
		UpdatedBy:         d.UpdatedBy,
		UpdatedAt:         d.UpdatedAt,
	}
}

func applicabilityBody(a frameworkscope.Applicability, frameworkVersionID uuid.UUID) map[string]any {
	reqs := make([]requirementWire, len(a.Requirements))
	for i, r := range a.Requirements {
		w := requirementWire{
			RequirementID: r.RequirementID.String(),
			Code:          r.Code,
			Title:         r.Title,
			Undecided:     r.Decision == nil,
		}
		if d := r.Decision; d != nil {
			dec, by, at := d.Decision, d.UpdatedBy, d.UpdatedAt
			w.Decision = &dec
			w.Justification = d.Justification
			w.ImplementationRef = d.ImplementationRef
			w.UpdatedBy = &by
			w.UpdatedAt = &at
		}
		reqs[i] = w
	}
	var scopeW any
	if a.Scope != nil {
		scopeW = toWire(*a.Scope)
	}
	return map[string]any{
		"framework_version_id": frameworkVersionID.String(),
		"framework_scope":      scopeW,
		"requirements":         reqs,
		"counts": countsWire{
			Total:      a.Counts.Total,
			Applicable: a.Counts.Applicable,
			Excluded:   a.Counts.Excluded,
			Undecided:  a.Counts.Undecided,
		},
	}
}

// ListApplicability — GET /v1/framework-scopes/{id}/applicability.
func (h *Handler) ListApplicability(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	out, err := h.store.ListDecisions(ctx, id)
	if err != nil {
		if errors.Is(err, frameworkscope.ErrNotFound) {
			httpresp.WriteError(w, http.StatusNotFound, "framework_scope not found")
			return
		}
		httperr.WriteInternal(w, r, "list applicability", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, applicabilityBody(out, out.Scope.FrameworkVersionID))
}

// ApplicabilityAsOf — GET /v1/framework-scopes/applicability?framework_version=…&as_of=….
// Returns the decisions of the revision active at as_of; framework_scope is
// null (and every requirement undecided) when none was.
func (h *Handler) ApplicabilityAsOf(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	fvID, err := uuid.Parse(strings.TrimSpace(r.URL.Query().Get("framework_version")))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "framework_version must be a UUID")
		return
	}
	asOf := strings.TrimSpace(r.URL.Query().Get("as_of"))
	if asOf == "" {
		httpresp.WriteError(w, http.StatusBadRequest, "as_of is required")
		return
	}
	t, err := time.Parse(time.RFC3339, asOf)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "as_of must be RFC3339: "+err.Error())
		return
	}
	out, err := h.store.DecisionsAsOf(ctx, fvID, t)
	if err != nil {
		httperr.WriteInternal(w, r, "applicability as_of", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, applicabilityBody(out, fvID))
}

// SetApplicability — PUT /v1/framework-scopes/{id}/applicability/{requirement_id}.
func (h *Handler) SetApplicability(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	reqID, err := uuid.Parse(chi.URLParam(r, "requirement_id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "requirement_id must be a UUID")
		return
	}
	var req setDecisionReq
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	d, sc, invalidated, err := h.store.SetDecision(ctx, frameworkscope.SetDecisionRequest{
		ScopeID:           id,
		RequirementID:     reqID,
		Decision:          req.Decision,
		Justification:     req.Justification,
		ImplementationRef: req.ImplementationRef,
		Actor:             actorOf(r),
	})
	if err != nil {
		switch {
		case errors.Is(err, frameworkscope.ErrInvalidDecision),
			errors.Is(err, frameworkscope.ErrJustificationRequired):
			httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, frameworkscope.ErrNotFound):
			httpresp.WriteError(w, http.StatusNotFound, "framework_scope not found")
		case errors.Is(err, frameworkscope.ErrRequirementNotFound):
			httpresp.WriteError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, frameworkscope.ErrDecisionsFrozen):
			httpresp.WriteError(w, http.StatusConflict, err.Error())
		default:
			httperr.WriteInternal(w, r, "set applicability", err)
		}
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"decision":             toDecisionWire(d),
		"framework_scope":      toWire(sc),
		"approval_invalidated": invalidated,
	})
}
