package evidencesummary

// Slice 750 — portfolio / multi-control evidence-summary read endpoint:
//
//	GET /v1/evidence-summary/portfolio?framework_version_id=...&family=...
//
// It is the cross-control sibling of the slice-502 single-control
// /v1/controls/{id}/evidence-summary surface. For a FILTERED control SET (by
// control-family OR by framework version; no filter = the whole program) it
// returns the DETERMINISTIC TWO-LEVEL bounded cross-control evidence rollup
// ALWAYS (cap controls-per-summary AND records-per-control — P0-750-2), plus a
// plain-language, cited, NON-BINDING AI summary of that rollup when the per-tenant
// inference client is available AND every citation resolves to a tenant-owned row
// in the cross-control grounding set (AC-2) AND every numeric claim matches the
// deterministic rollup (AC-3).
//
// The summary is a comprehension aid on the DASHBOARD — never an audit artifact,
// no approve/publish/export path (P0-502-3, AC-5), regenerated on demand (never
// persisted, P0-502-4), current live evidence only (P0-502-5). Both bounds are
// labeled honestly (AC-5). The endpoint sits behind the SAME program/control-read
// authz as the dashboard reads — no new role. When the summary is unavailable or
// its citations/numbers fail, the handler still returns the deterministic rollup
// with summary=null (graceful degradation, AC-7).

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/evidencesummary"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// portfolioSummarizer is the portfolio handler's read seam — exactly the one
// method the route needs. The production *evidencesummary.PortfolioService
// satisfies it; tests inject a fake to drive the wire shape without a model.
type portfolioSummarizer interface {
	PortfolioSummarize(ctx context.Context, filter evidencesummary.PortfolioFilter) (evidencesummary.PortfolioSummary, error)
}

// PortfolioHandler serves the single portfolio evidence-summary route over an
// evidencesummary.PortfolioService. It holds no write surface.
type PortfolioHandler struct {
	svc portfolioSummarizer
}

// NewPortfolio constructs a PortfolioHandler over the portfolio evidence-summary
// service.
func NewPortfolio(svc *evidencesummary.PortfolioService) *PortfolioHandler {
	return &PortfolioHandler{svc: svc}
}

// newPortfolioHandlerWith constructs a PortfolioHandler over an arbitrary
// portfolioSummarizer seam. For tests only — unexported, not part of the public
// surface.
func newPortfolioHandlerWith(svc portfolioSummarizer) *PortfolioHandler {
	return &PortfolioHandler{svc: svc}
}

// portfolioControlWire is one control's slice of the deterministic cross-control
// rollup — ALWAYS present (AC-7). `showing` / `total` carry the per-control bound.
type portfolioControlWire struct {
	ControlID    string             `json:"control_id"`
	ControlTitle string             `json:"control_title"`
	Showing      int                `json:"showing"`
	Total        int                `json:"total"`
	Records      []evidenceFactWire `json:"records"`
}

// portfolioRollupWire is the deterministic portfolio rollup the summary's numeric
// claims were verified against (AC-3). Surfaced so the UI can render the counts
// from ground truth, never from the model.
type portfolioRollupWire struct {
	ControlsInSummary       int `json:"controls_in_summary"`
	TotalMatched            int `json:"total_matched"`
	ControlsWithEvidence    int `json:"controls_with_evidence"`
	ControlsWithoutEvidence int `json:"controls_without_evidence"`
	TotalRecords            int `json:"total_records"`
}

// portfolioSetWire is the deterministic TWO-LEVEL bounded cross-control rollup —
// ALWAYS present (AC-7). `mode` names the filter dimension; `live_only` marks it
// CURRENT LIVE evidence (not frozen — P0-502-5); `controls_per_summary` /
// `records_per_control` expose BOTH bounds for honest UI labeling (AC-5).
type portfolioSetWire struct {
	Mode               string                 `json:"mode"`
	Family             string                 `json:"family,omitempty"`
	FrameworkLabel     string                 `json:"framework_label,omitempty"`
	LiveOnly           bool                   `json:"live_only"`
	ControlsPerSummary int                    `json:"controls_per_summary"`
	RecordsPerControl  int                    `json:"records_per_control"`
	Rollup             portfolioRollupWire    `json:"rollup"`
	Controls           []portfolioControlWire `json:"controls"`
}

// PortfolioEvidenceSummary handles GET /v1/evidence-summary/portfolio. The
// deterministic rollup is always rendered; the summary is rendered only when
// present and non-suppressed. On suppression the response carries summary=null
// plus a fixed suppression reason so the frontend can show the rollup alone
// (AC-7).
func (h *PortfolioHandler) PortfolioEvidenceSummary(w http.ResponseWriter, r *http.Request) {
	// Dashboard surface: the program/control-read role set guards it (no new
	// role) — reuse the same defense-in-depth guard the single-control surface
	// uses (admin/approver/owner).
	if !requireControlRead(w, r) {
		return
	}
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}

	filter, ok := parsePortfolioFilter(w, r)
	if !ok {
		return
	}

	sum, err := h.svc.PortfolioSummarize(r.Context(), filter)
	if err != nil {
		httperr.WriteInternal(w, r, "portfolio-evidencesummary", err)
		return
	}

	body := map[string]any{
		"evidence":          portfolioSetWireFrom(sum),
		"suppressed_reason": sum.Reason,
	}
	if sum.Suppressed || sum.Text == "" {
		// Graceful degradation: deterministic rollup only, summary withheld (AC-7).
		body["summary"] = nil
	} else {
		body["summary"] = summaryWireFrom(sum.Summary)
	}
	httpresp.WriteJSON(w, http.StatusOK, body)
}

// parsePortfolioFilter reads the OPTIONAL filter query params (at most one filter
// dimension in v1). `family` is a control-family string; `framework_version_id`
// is a UUID resolved to SCF anchors server-side. A request with neither is the
// whole-program rollup. Returns ok=false (after writing a 4xx) on a malformed
// param.
func parsePortfolioFilter(w http.ResponseWriter, r *http.Request) (evidencesummary.PortfolioFilter, bool) {
	var filter evidencesummary.PortfolioFilter

	if fvRaw := strings.TrimSpace(r.URL.Query().Get("framework_version_id")); fvRaw != "" {
		fv, err := uuid.Parse(fvRaw)
		if err != nil {
			httpresp.WriteError(w, http.StatusBadRequest, "framework_version_id must be a uuid")
			return filter, false
		}
		filter.FrameworkVersionID = fv
		// A human-readable label is optional; the caller may pass framework= for
		// the UI/prompt label without it being a grounding input.
		filter.FrameworkLabel = strings.TrimSpace(r.URL.Query().Get("framework"))
		if filter.FrameworkLabel == "" {
			filter.FrameworkLabel = fvRaw
		}
		return filter, true
	}

	if fam := strings.TrimSpace(r.URL.Query().Get("family")); fam != "" {
		filter.Family = fam
		return filter, true
	}

	// No filter => whole-program rollup.
	return filter, true
}

func portfolioSetWireFrom(sum evidencesummary.PortfolioSummary) portfolioSetWire {
	set := sum.PortfolioSet
	out := portfolioSetWire{
		Mode:               set.Filter.Mode(),
		Family:             set.Filter.Family,
		FrameworkLabel:     set.Filter.FrameworkLabel,
		LiveOnly:           true, // P0-502-5: current live evidence only, clearly labeled.
		ControlsPerSummary: evidencesummary.MaxControlsPerSummary,
		RecordsPerControl:  evidencesummary.MaxRecordsPerControl,
		Rollup: portfolioRollupWire{
			ControlsInSummary:       sum.Rollup.ControlsInSummary,
			TotalMatched:            sum.Rollup.TotalMatched,
			ControlsWithEvidence:    sum.Rollup.ControlsWithEvidence,
			ControlsWithoutEvidence: sum.Rollup.ControlsWithoutEvidence,
			TotalRecords:            sum.Rollup.TotalRecords,
		},
		Controls: make([]portfolioControlWire, 0, len(set.Controls)),
	}
	for _, c := range set.Controls {
		cw := portfolioControlWire{
			ControlID:    c.ControlID.String(),
			ControlTitle: c.ControlTitle,
			Showing:      len(c.Records),
			Total:        c.TotalCount,
			Records:      make([]evidenceFactWire, 0, len(c.Records)),
		}
		for _, e := range c.Records {
			cw.Records = append(cw.Records, evidenceFactWire{
				EvidenceID:   e.EvidenceID.String(),
				EvidenceKind: e.EvidenceKind,
				Result:       e.Result,
				ObservedAt:   e.ObservedAt.UTC().Format(time.RFC3339),
			})
		}
		out.Controls = append(out.Controls, cw)
	}
	return out
}
