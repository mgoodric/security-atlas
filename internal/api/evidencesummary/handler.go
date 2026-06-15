// Package evidencesummary serves the slice-502 AI evidence-summarization v0
// read endpoint:
//
//	GET /v1/controls/{id}/evidence-summary
//
// It returns the DETERMINISTIC bounded evidence set ALWAYS, and a
// plain-language, cited, NON-BINDING AI summary of that evidence when the
// per-tenant inference client is available AND every citation resolves to a
// tenant-owned row. The summary is a comprehension aid in the operator's own
// control-detail view — it is never an audit artifact, has no
// approve/publish/export path (P0-502-3, AC-5), and is regenerated on demand
// (never persisted, P0-502-4).
//
// The endpoint sits behind the SAME control-read authz as the slice-064
// control-detail reads (it reuses the control-read role guard shape) — there is
// no new ingress beyond the authenticated control view (threat-model S/E). When
// the summary is unavailable or its citations fail to resolve, the handler still
// returns the evidence set with summary=null (graceful degradation, AC-7,
// P0-502-7).
package evidencesummary

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/evidencesummary"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// summarizer is the handler's read seam — exactly the one method the route
// needs. The production *evidencesummary.Service satisfies it; tests can inject
// a fake to drive the wire shape without a model.
type summarizer interface {
	Summarize(ctx context.Context, controlID uuid.UUID) (evidencesummary.Summary, error)
}

// Handler serves the single evidence-summary route over an
// evidencesummary.Service. It holds no write surface.
type Handler struct {
	svc summarizer
}

// New constructs a Handler over the evidence-summary service.
func New(svc *evidencesummary.Service) *Handler { return &Handler{svc: svc} }

// newHandlerWith constructs a Handler over an arbitrary summarizer seam. For
// tests only — unexported, not part of the public surface.
func newHandlerWith(svc summarizer) *Handler { return &Handler{svc: svc} }

// ===== wire shapes =====

// evidenceFactWire is one cited evidence excerpt in the deterministic set.
type evidenceFactWire struct {
	EvidenceID   string `json:"evidence_id"`
	EvidenceKind string `json:"evidence_kind"`
	Result       string `json:"result"`
	ObservedAt   string `json:"observed_at"`
}

// evidenceSetWire is the deterministic bounded evidence set — ALWAYS present
// (AC-7). `live_only` marks it as CURRENT LIVE evidence (not a frozen
// audit-period population — P0-502-5); `showing` / `total` carry the bound.
type evidenceSetWire struct {
	ControlID    string             `json:"control_id"`
	ControlTitle string             `json:"control_title"`
	Showing      int                `json:"showing"`
	Total        int                `json:"total"`
	LiveOnly     bool               `json:"live_only"`
	Records      []evidenceFactWire `json:"records"`
}

// citationWire is one resolved, tenant-owned citation the summary makes.
type citationWire struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// summaryWire is the NON-BINDING AI summary. It is null in the response when
// suppressed. It carries NO approve/publish/export field by construction (AC-5,
// P0-502-3); the only metadata is the model provenance disclosure (AC-6) and
// the non-binding marker.
type summaryWire struct {
	Text      string         `json:"text"`
	Citations []citationWire `json:"citations"`
	// Structured model provenance (slice-182 schema contract shape), so a
	// consumer (e.g. the slice-499 cloud-routing banner) reads the provider
	// without re-parsing prose. `model` is the human-friendly composed string.
	Model         string `json:"model"`
	ModelName     string `json:"model_name"`
	ModelVersion  string `json:"model_version"`
	ModelProvider string `json:"model_provider"`
	Binding       bool   `json:"binding"`    // ALWAYS false — non-binding disclosure (AC-5/AC-6).
	Disclosure    string `json:"disclosure"` // human-readable non-audit-artifact notice.
}

// EvidenceSummary handles GET /v1/controls/{id}/evidence-summary. The evidence
// set is always rendered; the summary is rendered only when present and
// non-suppressed. On suppression the response carries summary=null plus a fixed
// suppression reason so the frontend can show the evidence list alone (AC-7).
func (h *Handler) EvidenceSummary(w http.ResponseWriter, r *http.Request) {
	if !requireControlRead(w, r) {
		return
	}
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	controlID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "control id must be a uuid")
		return
	}

	sum, err := h.svc.Summarize(r.Context(), controlID)
	if err != nil {
		if errors.Is(err, evidencesummary.ErrNoControl) {
			httpresp.WriteError(w, http.StatusNotFound, "control not found")
			return
		}
		httperr.WriteInternal(w, r, "evidencesummary", err)
		return
	}

	// sum.Reason is the empty string whenever a non-suppressed summary is
	// present (it is only set on the suppression paths), so it is the
	// suppressed_reason in both arms. The fixed reason vocabulary is
	// safe-to-render (no model/backend detail — slice-367 leak discipline).
	body := map[string]any{
		"control_id":        controlID.String(),
		"evidence":          evidenceSetWireFrom(sum.EvidenceSet),
		"suppressed_reason": sum.Reason,
	}
	if sum.Suppressed || sum.Text == "" {
		// Graceful degradation: evidence list only, summary withheld (AC-7).
		body["summary"] = nil
	} else {
		body["summary"] = summaryWireFrom(sum)
	}
	httpresp.WriteJSON(w, http.StatusOK, body)
}

func evidenceSetWireFrom(set evidencesummary.EvidenceSet) evidenceSetWire {
	out := evidenceSetWire{
		ControlID:    set.ControlID.String(),
		ControlTitle: set.ControlTitle,
		Showing:      len(set.Records),
		Total:        set.TotalCount,
		LiveOnly:     true, // P0-502-5: current live evidence only, clearly labeled.
		Records:      make([]evidenceFactWire, 0, len(set.Records)),
	}
	for _, e := range set.Records {
		out.Records = append(out.Records, evidenceFactWire{
			EvidenceID:   e.EvidenceID.String(),
			EvidenceKind: e.EvidenceKind,
			Result:       e.Result,
			ObservedAt:   e.ObservedAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}

func summaryWireFrom(sum evidencesummary.Summary) summaryWire {
	cites := make([]citationWire, 0, len(sum.Citations))
	for _, c := range sum.Citations {
		cites = append(cites, citationWire{Kind: c.Kind, ID: c.ID})
	}
	model := sum.ModelName
	if sum.ModelVersion != "" {
		model = sum.ModelName + " " + sum.ModelVersion
	}
	return summaryWire{
		Text:          sum.Text,
		Citations:     cites,
		Model:         model,
		ModelName:     sum.ModelName,
		ModelVersion:  sum.ModelVersion,
		ModelProvider: sum.ModelProvider,
		Binding:       false,
		Disclosure:    "AI-generated summary (model " + model + ") — not an audit artifact.",
	}
}

// ===== authz: control-read role guard (defense-in-depth) =====

// hasControlRead mirrors controldetail.hasControlRead / gapexplain.hasControlRead
// — the control-read role set is admin (wildcard) OR grc_engineer (IsApprover)
// OR control_owner (OwnerRoles). The slice-035 OPA middleware is the primary
// gate in production; this handler-level guard is the testable enforcement point
// when OPA is not wired (unit/integration servers).
func hasControlRead(c credstore.Credential) bool {
	return c.IsAdmin || c.IsApprover || len(c.OwnerRoles) > 0
}

// requireControlRead is the guard the handler calls FIRST. It returns true when
// the request may proceed; on denial it writes a 403 and returns false.
func requireControlRead(w http.ResponseWriter, r *http.Request) bool {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || !hasControlRead(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "role does not grant control-read access")
		return false
	}
	return true
}
