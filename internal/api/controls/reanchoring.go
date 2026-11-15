// Issue #2176 — GET /v1/controls/needs-reanchoring.
//
// After a new SCF release, a control left on the previous release's anchor
// row earns no framework credit (coverage joins controls to crosswalk edges
// by anchor row). #1971 D3 moves a control to the current release only when
// the same code keeps the same title, or, for a stock control, when only the
// title changed (#2321); every other control is flagged for a person to
// re-anchor. This endpoint lists the flagged controls of the
// bearer's tenant, each with its remap note, for the dashboard warning.
//
// The classification is scfimport.Classify, the same function the upgrade
// output uses, so the two cannot disagree. Pinned controls (a declared
// scf_release, or an anchor named by UUID — #1770) are bound to their
// release on purpose and are left out, not listed separately.
//
// Authz: the path resolves to resource "controls", action "read", the same
// OPA admit as GET /v1/controls. RLS and the query's explicit tenant id
// keep the read to the bearer's tenant.

package controls

import (
	"context"
	"net/http"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// legacyAnchorLister is the store seam the handler reads through; the
// control.Store satisfies it.
type legacyAnchorLister interface {
	ListOnLegacySCFAnchors(ctx context.Context) ([]dbx.ListControlsOnLegacySCFAnchorsRow, error)
}

// ReanchoringHandler exposes GET /v1/controls/needs-reanchoring.
type ReanchoringHandler struct {
	store legacyAnchorLister
}

// NewReanchoringHandler wires the handler over the control store.
func NewReanchoringHandler(store legacyAnchorLister) *ReanchoringHandler {
	return &ReanchoringHandler{store: store}
}

type reanchoringControlWire struct {
	ID                string `json:"id"`
	BundleID          string `json:"bundle_id"`
	Title             string `json:"title"`
	LegacySCFID       string `json:"legacy_scf_id"`
	LegacyAnchorTitle string `json:"legacy_anchor_title"`
	LegacyRelease     string `json:"legacy_release"`
	CurrentRelease    string `json:"current_release"`
	// Note says where the legacy anchor's concept went (data/scfremap,
	// then SCF's legacy ID, then a changed title, then a removed code).
	Note string `json:"note"`
}

type reanchoringResp struct {
	Controls []reanchoringControlWire `json:"controls"`
	Count    int                      `json:"count"`
}

// List handles GET /v1/controls/needs-reanchoring. Always 200 with an
// envelope; a tenant with nothing to re-anchor gets
// `{"controls": [], "count": 0}`.
func (h *ReanchoringHandler) List(w http.ResponseWriter, r *http.Request) {
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	rows, err := h.store.ListOnLegacySCFAnchors(r.Context())
	if err != nil {
		httperr.WriteInternal(w, r, "list controls needing re-anchoring", err)
		return
	}
	flagged, err := scfimport.NeedsReanchoring(rows)
	if err != nil {
		httperr.WriteInternal(w, r, "classify controls needing re-anchoring", err)
		return
	}
	out := make([]reanchoringControlWire, len(flagged))
	for i, c := range flagged {
		out[i] = reanchoringControlWire{
			ID:                c.ControlID.String(),
			BundleID:          c.BundleID,
			Title:             c.ControlTitle,
			LegacySCFID:       c.LegacySCFID,
			LegacyAnchorTitle: c.LegacyTitle,
			LegacyRelease:     c.LegacyRelease,
			CurrentRelease:    c.CurrentRelease,
			Note:              c.Note,
		}
	}
	httpresp.WriteJSON(w, http.StatusOK, reanchoringResp{Controls: out, Count: len(out)})
}
