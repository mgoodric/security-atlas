// Package oscalexport is the slice-030 HTTP surface for the OSCAL
// audit-handoff export pipeline. It exposes the same capability as the
// `atlas-cli oscal-export` command — generate the SSP + AP/AR + POA&M
// bundle for a FROZEN AuditPeriod — over HTTP so the UI can trigger it
// (AC-8).
//
// The handler is a thin shell over internal/oscal.Exporter: it parses
// the request, runs the export under the request's tenant context, and
// streams the signed bundle back as a JSON object whose members are the
// canonical OSCAL documents. All constitutional enforcement (invariant
// 10's frozen-period gate, AC-5 signing, AC-6/AC-7 round-trip) lives in
// internal/oscal and is surfaced here as HTTP status codes:
//
//	409 Conflict            — period is not frozen (invariant 10)
//	404 Not Found           — period id does not resolve for the tenant
//	502 Bad Gateway         — oscal-bridge unavailable
//	422 Unprocessable       — compliance-trestle round-trip failed
//	500 Internal            — signing failed / unexpected
package oscalexport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/oscal"
)

// oscalExporter is the narrow surface the Handler actually uses. The
// production wiring passes a *oscal.Exporter, which satisfies this
// interface implicitly. Tests inject a fake to drive the error-mapping
// branches (ErrPeriodNotFrozen, ErrPeriodNotFound, ErrBridgeUnavailable,
// ErrRoundTripFailed, ErrSigningFailed) and the happy path without
// standing up Postgres + the Python oscal-bridge.
type oscalExporter interface {
	Export(ctx context.Context, in oscal.ExportInput) (*oscal.Bundle, error)
}

// Handler wires the OSCAL Exporter behind an HTTP route.
type Handler struct {
	exporter oscalExporter
}

// New constructs a Handler over a wired Exporter. The argument is the
// concrete *oscal.Exporter in production; the field type is an interface
// purely for unit-test injectability — the production call site at
// internal/api/httpserver.go does not change.
func New(exporter *oscal.Exporter) *Handler {
	return &Handler{exporter: exporter}
}

// exportRequest is the POST /v1/audit-periods/{id}/oscal-export body.
// The audit period id comes from the URL path; the body carries the SSP
// org-profile fields the platform does not yet store in a dedicated
// table.
type exportRequest struct {
	OrganizationName  string `json:"organization_name"`
	SystemName        string `json:"system_name"`
	SystemDescription string `json:"system_description"`
}

// bundleMemberJSON is one OSCAL document in the response.
type bundleMemberJSON struct {
	Filename  string          `json:"filename"`
	ModelType string          `json:"model_type"`
	SHA256    string          `json:"sha256"`
	OSCAL     json.RawMessage `json:"oscal"`
}

// exportResponse is the JSON body returned on a successful export. It
// mirrors the on-disk bundle: a manifest plus the four OSCAL members.
type exportResponse struct {
	AuditPeriodID string             `json:"audit_period_id"`
	FrozenAt      string             `json:"frozen_at"`
	OSCALVersion  string             `json:"oscal_version"`
	GeneratedAt   string             `json:"generated_at"`
	RequestedBy   string             `json:"requested_by"`
	Signature     oscal.Signature    `json:"signature"`
	Members       []bundleMemberJSON `json:"members"`
}

// Export handles POST /v1/audit-periods/{id}/oscal-export. The route is
// registered with the {id} URL param by the caller (httpserver.go).
func (h *Handler) Export(w http.ResponseWriter, r *http.Request) {
	periodID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "audit period id must be a UUID")
		return
	}

	var req exportRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}

	// RequestedBy: prefer the authenticated credential id from context,
	// fall back to a stable marker. The tenancy + authz middleware has
	// already run; the request context carries the tenant GUC the
	// Exporter's reads run under.
	requestedBy := "api"
	if cred, ok := authctx.CredentialFromContext(r.Context()); ok && cred.ID != "" {
		requestedBy = cred.ID
	}

	bundle, err := h.exporter.Export(r.Context(), oscal.ExportInput{
		AuditPeriodID:     periodID,
		OrganizationName:  req.OrganizationName,
		SystemName:        req.SystemName,
		SystemDescription: req.SystemDescription,
		RequestedBy:       requestedBy,
	})
	if err != nil {
		writeExportError(w, err)
		return
	}

	resp := exportResponse{
		AuditPeriodID: bundle.AuditPeriodID.String(),
		FrozenAt:      bundle.FrozenAt,
		OSCALVersion:  bundle.OSCALVersion,
		GeneratedAt:   bundle.GeneratedAt,
		RequestedBy:   bundle.RequestedBy,
		Signature:     bundle.Signature,
		Members:       make([]bundleMemberJSON, 0, len(bundle.Members)),
	}
	for _, m := range bundle.Members {
		resp.Members = append(resp.Members, bundleMemberJSON{
			Filename:  m.Filename,
			ModelType: m.ModelType,
			SHA256:    m.SHA256,
			OSCAL:     json.RawMessage(m.JSON),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// writeExportError maps an internal/oscal error to the right HTTP status.
func writeExportError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, oscal.ErrPeriodNotFrozen):
		// Constitutional invariant 10: a non-frozen period cannot be
		// exported. 409 Conflict — the resource is in the wrong state.
		httpresp.WriteError(w, http.StatusConflict,
			"audit period is not frozen; freeze it before exporting (invariant 10)")

	case errors.Is(err, oscal.ErrPeriodNotFound):
		httpresp.WriteError(w, http.StatusNotFound, "audit period not found")
	case errors.Is(err, oscal.ErrBridgeUnavailable):
		httpresp.WriteError(w, http.StatusBadGateway, "oscal-bridge service unavailable")
	case errors.Is(err, oscal.ErrRoundTripFailed):
		// AC-6/AC-7: the bundle failed compliance-trestle round-trip and
		// was NOT finalized.
		httpresp.WriteError(w, http.StatusUnprocessableEntity,
			"compliance-trestle round-trip validation failed; no bundle was produced")

	case errors.Is(err, oscal.ErrSigningFailed):
		// AC-5: signing failed and no unsigned bundle was produced.
		httpresp.WriteError(w, http.StatusInternalServerError,
			"bundle signing failed; no bundle was produced")

	default:
		httpresp.WriteError(w, http.StatusInternalServerError, "oscal export failed")
	}
}
