package frameworkscopes

// Bulk and CSV-import writes of applicability decisions (#1790, Statement of
// Applicability #1744). Routes:
//
//	POST /v1/framework-scopes/{id}/applicability/bulk    — one decision on many requirements
//	POST /v1/framework-scopes/{id}/applicability/import  — decisions from a CSV body
//
// Both apply their decisions as one change, with the same draft reset and
// workflow-log rows as the single PUT, behind the same write gate.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/frameworkscope"
)

// maxImportBytes bounds the CSV body. An SoA is hundreds of rows at most.
const maxImportBytes = 4 << 20

// maxBulkBytes bounds the bulk JSON body: a list of requirement ids plus one
// justification. 1 MiB is thousands of ids.
const maxBulkBytes = 1 << 20

type bulkDecisionReq struct {
	RequirementIDs    []string `json:"requirement_ids"`
	Decision          string   `json:"decision"`
	Justification     string   `json:"justification"`
	ImplementationRef string   `json:"implementation_ref"`
}

type importRejectionWire struct {
	Line            int    `json:"line"`
	RequirementCode string `json:"requirement_code"`
	Reason          string `json:"reason"`
}

// writeDecisionError maps the store's decision-write errors to a response.
func writeDecisionError(w http.ResponseWriter, r *http.Request, what string, err error) {
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
		httperr.WriteInternal(w, r, what, err)
	}
}

// BulkSetApplicability — POST /v1/framework-scopes/{id}/applicability/bulk.
// Sets one decision, justification and implementation reference on every
// listed requirement. All or nothing: an invalid decision is a 400 and an
// unknown requirement a 404, and either writes nothing.
func (h *Handler) BulkSetApplicability(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var req bulkDecisionReq
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBulkBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpresp.WriteError(w, http.StatusRequestEntityTooLarge, "bulk body exceeds 1 MiB")
			return
		}
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	// Case-folded and trimmed, as the CSV import treats its decision cell.
	req.Decision = strings.ToLower(strings.TrimSpace(req.Decision))
	if len(req.RequirementIDs) == 0 {
		httpresp.WriteError(w, http.StatusBadRequest, "requirement_ids must list at least one requirement")
		return
	}
	seen := map[uuid.UUID]bool{}
	items := make([]frameworkscope.DecisionInput, 0, len(req.RequirementIDs))
	for _, raw := range req.RequirementIDs {
		reqID, err := uuid.Parse(raw)
		if err != nil {
			httpresp.WriteError(w, http.StatusBadRequest, "requirement_ids must be UUIDs")
			return
		}
		if seen[reqID] {
			continue
		}
		seen[reqID] = true
		items = append(items, frameworkscope.DecisionInput{
			RequirementID:     reqID,
			Decision:          req.Decision,
			Justification:     req.Justification,
			ImplementationRef: req.ImplementationRef,
		})
	}
	decs, sc, invalidated, err := h.store.SetDecisions(ctx, id, items, actorOf(r))
	if err != nil {
		writeDecisionError(w, r, "bulk set applicability", err)
		return
	}
	out := make([]decisionWire, len(decs))
	for i, d := range decs {
		out[i] = toDecisionWire(d)
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"decisions":            out,
		"framework_scope":      toWire(sc),
		"approval_invalidated": invalidated,
	})
}

// ImportApplicability — POST /v1/framework-scopes/{id}/applicability/import.
// The body is a CSV file whose header row names requirement_code and
// decision, and optionally justification and implementation_ref. Valid rows
// are applied as one change and bad rows are reported with their line and
// reason: 200 when at least one row applied, 422 (nothing written) when
// none did.
func (h *Handler) ImportApplicability(w http.ResponseWriter, r *http.Request) {
	ctx, ok := h.tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxImportBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpresp.WriteError(w, http.StatusRequestEntityTooLarge, "import body exceeds 4 MiB")
			return
		}
		httpresp.WriteError(w, http.StatusBadRequest, "could not read body")
		return
	}
	rows, parseRejected, err := frameworkscope.ParseDecisionCSV(bytes.NewReader(body))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := h.store.ImportDecisions(ctx, id, rows, parseRejected, actorOf(r))
	if err != nil {
		writeDecisionError(w, r, "import applicability", err)
		return
	}
	rejected := make([]importRejectionWire, len(res.Rejected))
	for i, rj := range res.Rejected {
		rejected[i] = importRejectionWire{Line: rj.Line, RequirementCode: rj.RequirementCode, Reason: rj.Reason}
	}
	status := http.StatusOK
	if res.Applied == 0 {
		status = http.StatusUnprocessableEntity
	}
	httpresp.WriteJSON(w, status, map[string]any{
		"applied":              res.Applied,
		"rejected":             rejected,
		"framework_scope":      toWire(res.Scope),
		"approval_invalidated": res.Invalidated,
	})
}
