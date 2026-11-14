package frameworkscopes

// Carry-over of applicability decisions to a new framework version (#1793,
// Statement of Applicability #1744). Route:
//
//	POST /v1/framework-scopes/{id}/applicability:carry-over
//
// {id} is the tenant's draft revision on framework version N+1. The handler
// copies the tenant's decisions on N for every requirement whose carry-over
// a super_admin approved in the version-migration queue (ADR 0019). Approving
// a queue row decides what may be carried; it does not write any tenant's
// decisions. This call is the tenant's own write, under its RLS, behind the
// same write gate as the bulk set.

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/frameworkscope"
)

type carryOverItemWire struct {
	RequirementCode   string  `json:"requirement_code"`
	FromRequirementID *string `json:"from_requirement_id"`
	ToRequirementID   *string `json:"to_requirement_id"`
	Reason            string  `json:"reason"`
}

func toCarryOverItemsWire(items []frameworkscope.CarryOverItem) []carryOverItemWire {
	out := make([]carryOverItemWire, len(items))
	for i, it := range items {
		out[i] = carryOverItemWire{
			RequirementCode:   it.RequirementCode,
			FromRequirementID: uuidString(it.FromRequirementID),
			ToRequirementID:   uuidString(it.ToRequirementID),
			Reason:            it.Reason,
		}
	}
	return out
}

func uuidString(u *uuid.UUID) *string {
	if u == nil {
		return nil
	}
	s := u.String()
	return &s
}

// CarryOverApplicability — POST /v1/framework-scopes/{id}/applicability:carry-over.
// 200 with the copied decisions, the rows skipped (already decided in the
// draft, or undecided on the source) and the rows flagged for review (new,
// removed, pending or rejected). 409 when the revision is not a draft or no
// single prior version leads into its framework version. Re-running writes
// nothing new.
func (h *Handler) CarryOverApplicability(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	res, err := h.store.CarryOverDecisions(ctx, id, actorOf(r))
	if err != nil {
		switch {
		case errors.Is(err, frameworkscope.ErrCarryOverNotDraft),
			errors.Is(err, frameworkscope.ErrNoCarryOverSource),
			errors.Is(err, frameworkscope.ErrAmbiguousCarryOverSource):
			httpresp.WriteError(w, http.StatusConflict, err.Error())
		default:
			writeDecisionError(w, r, "carry over applicability", err)
		}
		return
	}
	copied := make([]decisionWire, len(res.Copied))
	for i, d := range res.Copied {
		copied[i] = toDecisionWire(d)
	}
	var sourceScopeID *string
	if res.SourceScope != nil {
		s := res.SourceScope.ID.String()
		sourceScopeID = &s
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"framework_scope":             toWire(res.Scope),
		"source_framework_version_id": res.SourceVersionID.String(),
		"source_framework_version":    res.SourceVersion,
		"source_framework_scope_id":   sourceScopeID,
		"copied":                      copied,
		"skipped":                     toCarryOverItemsWire(res.Skipped),
		"flagged":                     toCarryOverItemsWire(res.Flagged),
	})
}
