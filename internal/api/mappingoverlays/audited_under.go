package mappingoverlays

// The "audited under" report (#1801):
//
//	GET /v1/mapping-overlays/audited-under         JSON (?framework=<slug>)
//	GET /v1/mapping-overlays/audited-under/export  the same rows as CSV
//
// Read-only and tenant-scoped like the overlay list: any credential of the
// tenant may read it, and RLS binds every row.

import (
	"bytes"
	"net/http"
	"slices"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/export"
	"github.com/mgoodric/security-atlas/internal/mappingoverlay"
)

type candidateWire struct {
	AnchorID         string  `json:"anchor_id"`
	AnchorCode       string  `json:"anchor_code"`
	AnchorTitle      string  `json:"anchor_title"`
	RelationshipType string  `json:"relationship_type"`
	Strength         float64 `json:"strength"`
}

type gapWire struct {
	ControlID        string          `json:"control_id"`
	ControlBundleID  string          `json:"control_bundle_id"`
	ControlTitle     string          `json:"control_title"`
	AnchorID         *string         `json:"anchor_id"`
	AnchorCode       string          `json:"anchor_code,omitempty"`
	AnchorSCFRelease string          `json:"anchor_scf_release,omitempty"`
	AnchorFit        string          `json:"anchor_fit"`
	AnchorFitNote    string          `json:"anchor_fit_note,omitempty"`
	Framework        string          `json:"framework"`
	FrameworkVersion string          `json:"framework_version"`
	RequirementID    string          `json:"requirement_id"`
	RequirementCode  string          `json:"requirement_code"`
	RequirementTitle string          `json:"requirement_title"`
	TestedUnderNote  string          `json:"tested_under_note,omitempty"`
	CandidateAnchors []candidateWire `json:"candidate_anchors"`
}

func gapWireFrom(g mappingoverlay.Gap) gapWire {
	w := gapWire{
		ControlID:        g.ControlID.String(),
		ControlBundleID:  g.ControlBundleID,
		ControlTitle:     g.ControlTitle,
		AnchorCode:       g.AnchorCode,
		AnchorSCFRelease: g.AnchorSCFRelease,
		AnchorFit:        g.AnchorFit,
		AnchorFitNote:    g.AnchorFitNote,
		Framework:        g.Framework,
		FrameworkVersion: g.FrameworkVersion,
		RequirementID:    g.RequirementID.String(),
		RequirementCode:  g.RequirementCode,
		RequirementTitle: g.RequirementTitle,
		TestedUnderNote:  g.TestedUnderNote,
		CandidateAnchors: make([]candidateWire, len(g.Candidates)),
	}
	if g.AnchorID != nil {
		s := g.AnchorID.String()
		w.AnchorID = &s
	}
	for i, c := range g.Candidates {
		w.CandidateAnchors[i] = candidateWire{
			AnchorID:         c.AnchorID.String(),
			AnchorCode:       c.AnchorCode,
			AnchorTitle:      c.AnchorTitle,
			RelationshipType: c.RelationshipType,
			Strength:         c.Strength,
		}
	}
	return w
}

// AuditedUnder handles GET /v1/mapping-overlays/audited-under.
// candidate_scf_release names the SCF release candidate anchors come from
// (the current one), or is null when no SCF release is current.
func (h *Handler) AuditedUnder(w http.ResponseWriter, r *http.Request) {
	if !requireTenant(w, r) {
		return
	}
	rep, err := h.store.AuditedUnder(r.Context(), r.URL.Query().Get("framework"))
	if err != nil {
		httperr.WriteInternal(w, r, "audited-under report", err)
		return
	}
	rows := make([]gapWire, len(rep.Gaps))
	for i, g := range rep.Gaps {
		rows[i] = gapWireFrom(g)
	}
	var release *string
	if rep.SCFRelease != "" {
		release = &rep.SCFRelease
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"rows":                  rows,
		"count":                 len(rows),
		"candidate_scf_release": release,
	})
}

// AuditedUnderExport handles GET /v1/mapping-overlays/audited-under/export:
// the report's rows as CSV. Control titles and tested-under notes are
// organisation-entered text, so cells go through the shared encoder's
// formula neutralisation.
func (h *Handler) AuditedUnderExport(w http.ResponseWriter, r *http.Request) {
	if !requireTenant(w, r) {
		return
	}
	rep, err := h.store.AuditedUnder(r.Context(), r.URL.Query().Get("framework"))
	if err != nil {
		httperr.WriteInternal(w, r, "audited-under export", err)
		return
	}
	enc := export.NewCSVExporter()
	var buf bytes.Buffer
	rows := func(yield func([]string) bool) {
		for _, g := range rep.Gaps {
			if !yield(mappingoverlay.AuditedUnderRow(g, rep.SCFRelease)) {
				return
			}
		}
	}
	if err := enc.WriteRows(&buf, slices.Clone(mappingoverlay.AuditedUnderColumns), rows); err != nil {
		httperr.WriteInternal(w, r, "encode audited-under export", err)
		return
	}
	w.Header().Set("Content-Type", enc.ContentType())
	w.Header().Set("Content-Disposition", `attachment; filename="`+export.BuildFilename("audited-under", enc.FileExt(), nil)+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}
