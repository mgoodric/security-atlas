// Package gapexplain serves the slice-444 AI gap-explanation v0 read endpoint:
//
//	GET /v1/controls/{id}/gap-explanation
//
// It returns the DETERMINISTIC per-control gap rollup ALWAYS, and a
// plain-language, cited, NON-BINDING AI explanation of that rollup when local
// Ollama is available AND every citation resolves to a tenant-owned row. The
// explanation is a comprehension aid in the operator's own control-detail
// view — it is never an audit artifact, has no approve/publish/export path
// (P0-444-3, AC-5), and is regenerated on demand (never persisted, P0-444-4).
//
// The endpoint sits behind the SAME control-read authz as the slice-064
// control-detail reads (it reuses the control-read role guard shape) — there
// is no new ingress beyond the authenticated control view (threat-model S/E).
// When the explanation is unavailable or its citations fail to resolve, the
// handler still returns the rollup with explanation=null (graceful
// degradation, AC-7, P0-444-7).
package gapexplain

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/gapexplain"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// explainer is the handler's read seam — exactly the one method the route
// needs. The production *gapexplain.Service satisfies it; tests can inject a
// fake to drive the wire shape without a model.
type explainer interface {
	Explain(ctx context.Context, controlID uuid.UUID) (gapexplain.Explanation, error)
}

// Handler serves the single gap-explanation route over a gapexplain.Service.
// It holds no write surface.
type Handler struct {
	svc explainer
}

// New constructs a Handler over the gap-explanation service.
func New(svc *gapexplain.Service) *Handler { return &Handler{svc: svc} }

// newHandlerWith constructs a Handler over an arbitrary explainer seam. For
// tests only — unexported, not part of the public surface.
func newHandlerWith(svc explainer) *Handler { return &Handler{svc: svc} }

// ===== wire shapes =====

// evidenceFactWire is one cited evidence excerpt in the deterministic rollup.
type evidenceFactWire struct {
	EvidenceID   string `json:"evidence_id"`
	EvidenceKind string `json:"evidence_kind"`
	Result       string `json:"result"`
	ObservedAt   string `json:"observed_at"`
}

// rollupWire is the deterministic gap rollup — ALWAYS present (AC-7).
type rollupWire struct {
	ControlID        string             `json:"control_id"`
	ControlTitle     string             `json:"control_title"`
	FreshnessClass   string             `json:"freshness_class"`
	IsStale          bool               `json:"is_stale"`
	EvidenceCount    int                `json:"evidence_count"`
	LatestObservedAt *string            `json:"latest_observed_at"`
	ValidUntil       *string            `json:"valid_until"`
	Evidence         []evidenceFactWire `json:"evidence"`
}

// citationWire is one resolved, tenant-owned citation the explanation makes.
type citationWire struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// explanationWire is the NON-BINDING AI explanation. It is null in the
// response when suppressed. It carries NO approve/publish/export field by
// construction (AC-5, P0-444-3); the only metadata is the model provenance
// disclosure (AC-6) and the non-binding marker.
type explanationWire struct {
	Text      string         `json:"text"`
	Citations []citationWire `json:"citations"`
	// Structured model provenance (slice-182 schema contract shape), so a
	// future consumer (e.g. a cloud-routing banner) reads the provider without
	// re-parsing prose. `model` is the human-friendly composed string.
	Model         string `json:"model"`
	ModelName     string `json:"model_name"`
	ModelVersion  string `json:"model_version"`
	ModelProvider string `json:"model_provider"`
	Binding       bool   `json:"binding"`    // ALWAYS false — non-binding disclosure (AC-5/AC-6).
	Disclosure    string `json:"disclosure"` // human-readable non-audit-artifact notice.
}

// GapExplanation handles GET /v1/controls/{id}/gap-explanation. The rollup is
// always rendered; the explanation is rendered only when present and
// non-suppressed. On suppression the response carries explanation=null plus a
// fixed suppression reason so the frontend can show the rollup alone (AC-7).
func (h *Handler) GapExplanation(w http.ResponseWriter, r *http.Request) {
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

	exp, err := h.svc.Explain(r.Context(), controlID)
	if err != nil {
		httperr.WriteInternal(w, r, "gapexplain", err)
		return
	}

	// exp.Reason is the empty string whenever a non-suppressed explanation is
	// present (it is only set on the suppression paths), so it is the
	// suppressed_reason in both arms. The fixed reason vocabulary is
	// safe-to-render (no model/backend detail — slice-367 leak discipline).
	body := map[string]any{
		"control_id":        controlID.String(),
		"rollup":            rollupWireFrom(exp.Rollup),
		"suppressed_reason": exp.Reason,
	}
	if exp.Suppressed || exp.Text == "" {
		// Graceful degradation: rollup only, explanation withheld (AC-7).
		body["explanation"] = nil
	} else {
		body["explanation"] = explanationWireFrom(exp)
	}
	httpresp.WriteJSON(w, http.StatusOK, body)
}

func rollupWireFrom(rl gapexplain.Rollup) rollupWire {
	out := rollupWire{
		ControlID:      rl.ControlID.String(),
		ControlTitle:   rl.ControlTitle,
		FreshnessClass: rl.FreshnessClass,
		IsStale:        rl.IsStale,
		EvidenceCount:  rl.EvidenceCount,
		Evidence:       make([]evidenceFactWire, 0, len(rl.Evidence)),
	}
	if rl.LatestObservedAt != nil {
		s := rl.LatestObservedAt.UTC().Format(time.RFC3339)
		out.LatestObservedAt = &s
	}
	if rl.ValidUntil != nil {
		s := rl.ValidUntil.UTC().Format(time.RFC3339)
		out.ValidUntil = &s
	}
	for _, e := range rl.Evidence {
		out.Evidence = append(out.Evidence, evidenceFactWire{
			EvidenceID:   e.EvidenceID.String(),
			EvidenceKind: e.EvidenceKind,
			Result:       e.Result,
			ObservedAt:   e.ObservedAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}

func explanationWireFrom(exp gapexplain.Explanation) explanationWire {
	cites := make([]citationWire, 0, len(exp.Citations))
	for _, c := range exp.Citations {
		cites = append(cites, citationWire{Kind: c.Kind, ID: c.ID})
	}
	model := exp.ModelName
	if exp.ModelVersion != "" {
		model = exp.ModelName + " " + exp.ModelVersion
	}
	return explanationWire{
		Text:          exp.Text,
		Citations:     cites,
		Model:         model,
		ModelName:     exp.ModelName,
		ModelVersion:  exp.ModelVersion,
		ModelProvider: exp.ModelProvider,
		Binding:       false,
		Disclosure:    "AI-generated explanation (model " + model + ") — not an audit artifact.",
	}
}

// ===== authz: control-read role guard (defense-in-depth) =====

// hasControlRead mirrors controldetail.hasControlRead — the control-read role
// set is admin (wildcard) OR grc_engineer (IsApprover) OR control_owner
// (OwnerRoles). The slice-035 OPA middleware is the primary gate in
// production; this handler-level guard is the testable enforcement point when
// OPA is not wired (unit/integration servers).
func hasControlRead(c credstore.Credential) bool {
	return c.IsAdmin || c.IsApprover || len(c.OwnerRoles) > 0
}

// requireControlRead is the guard the handler calls FIRST. It returns true
// when the request may proceed; on denial it writes a 403 and returns false.
func requireControlRead(w http.ResponseWriter, r *http.Request) bool {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || !hasControlRead(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "role does not grant control-read access")
		return false
	}
	return true
}
