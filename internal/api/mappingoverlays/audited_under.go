package mappingoverlays

// The "audited under" report (#1801):
//
//	GET /v1/mapping-overlays/audited-under         JSON (?framework=<slug>&limit=<n>)
//	GET /v1/mapping-overlays/audited-under/export  the same rows as CSV
//
// Read-only and tenant-scoped like the overlay list: any credential of the
// tenant may read it, and RLS binds every row.

import (
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"

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

type unresolvedWire struct {
	ControlID       string `json:"control_id"`
	ControlBundleID string `json:"control_bundle_id"`
	ControlTitle    string `json:"control_title"`
	System          string `json:"system"`
	Code            string `json:"code"`
	TestedUnderNote string `json:"tested_under_note,omitempty"`
}

// parseAuditedUnderLimit reads ?limit=. Empty is the cap; a non-integer or
// a value outside 1..AuditedUnderRowCap returns the 400 message.
func parseAuditedUnderLimit(v string) (int, string) {
	if v == "" {
		return mappingoverlay.AuditedUnderRowCap, ""
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > mappingoverlay.AuditedUnderRowCap {
		return 0, fmt.Sprintf("limit must be an integer from 1 to %d", mappingoverlay.AuditedUnderRowCap)
	}
	return n, ""
}

// auditedUnderReport reads the report for a request, writing the 400 or
// 500 itself; ok is false when it did.
func (h *Handler) auditedUnderReport(w http.ResponseWriter, r *http.Request, what string) (mappingoverlay.AuditedUnderReport, int, bool) {
	if !requireTenant(w, r) {
		return mappingoverlay.AuditedUnderReport{}, 0, false
	}
	limit, msg := parseAuditedUnderLimit(r.URL.Query().Get("limit"))
	if msg != "" {
		httpresp.WriteError(w, http.StatusBadRequest, msg)
		return mappingoverlay.AuditedUnderReport{}, 0, false
	}
	rep, err := h.store.AuditedUnder(r.Context(), r.URL.Query().Get("framework"), limit)
	if err != nil {
		httperr.WriteInternal(w, r, what, err)
		return mappingoverlay.AuditedUnderReport{}, 0, false
	}
	return rep, limit, true
}

// AuditedUnder handles GET /v1/mapping-overlays/audited-under.
// candidate_scf_release names the SCF release candidate anchors come from
// (the current one), or is null when no SCF release is current.
//
// #2324: rows and unresolved_references are each capped at ?limit=
// (default and maximum mappingoverlay.AuditedUnderRowCap). total and
// unresolved_count count every row before the cap, and truncated is true
// when either list was cut. unresolved_references are the tested-under
// references that name no requirement in the framework's current version.
func (h *Handler) AuditedUnder(w http.ResponseWriter, r *http.Request) {
	rep, limit, ok := h.auditedUnderReport(w, r, "audited-under report")
	if !ok {
		return
	}
	rows := make([]gapWire, len(rep.Gaps))
	for i, g := range rep.Gaps {
		rows[i] = gapWireFrom(g)
	}
	unresolved := make([]unresolvedWire, len(rep.Unresolved))
	for i, u := range rep.Unresolved {
		unresolved[i] = unresolvedWire{
			ControlID:       u.ControlID.String(),
			ControlBundleID: u.ControlBundleID,
			ControlTitle:    u.ControlTitle,
			System:          u.System,
			Code:            u.Code,
			TestedUnderNote: u.TestedUnderNote,
		}
	}
	var release *string
	if rep.SCFRelease != "" {
		release = &rep.SCFRelease
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"rows":                  rows,
		"count":                 len(rows),
		"total":                 rep.GapTotal,
		"limit":                 limit,
		"truncated":             rep.Truncated,
		"unresolved_references": unresolved,
		"unresolved_count":      rep.UnresolvedTotal,
		"candidate_scf_release": release,
	})
}

// AuditedUnderExport handles GET /v1/mapping-overlays/audited-under/export:
// the report's rows as CSV. Control titles and tested-under notes are
// organisation-entered text, so cells go through the shared encoder's
// formula neutralisation.
//
// #2324: the rows take the same ?limit= cap as the JSON report and are
// encoded straight onto the response, not buffered. X-Atlas-Truncated is
// "true" when rows past the cap were left out, and
// X-Atlas-Unresolved-References counts the references the report could not
// resolve (the JSON report lists them).
func (h *Handler) AuditedUnderExport(w http.ResponseWriter, r *http.Request) {
	rep, _, ok := h.auditedUnderReport(w, r, "audited-under export")
	if !ok {
		return
	}
	enc := export.NewCSVExporter()
	rows := func(yield func([]string) bool) {
		for _, g := range rep.Gaps {
			if !yield(mappingoverlay.AuditedUnderRow(g, rep.SCFRelease)) {
				return
			}
		}
	}
	w.Header().Set("Content-Type", enc.ContentType())
	w.Header().Set("Content-Disposition", `attachment; filename="`+export.BuildFilename("audited-under", enc.FileExt(), nil)+`"`)
	w.Header().Set("X-Atlas-Truncated", strconv.FormatBool(rep.Truncated))
	w.Header().Set("X-Atlas-Unresolved-References", strconv.Itoa(rep.UnresolvedTotal))
	w.WriteHeader(http.StatusOK)
	// The status is on the wire, so a write error past this point can only
	// be logged: the client sees a cut-off body.
	if err := enc.WriteRows(w, slices.Clone(mappingoverlay.AuditedUnderColumns), rows); err != nil {
		slog.WarnContext(r.Context(), "audited-under export: write", "error", err)
	}
}
