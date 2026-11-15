// Issue #1775 — GET /v1/controls/remap-proposals.
//
// When `atlas-cli catalog import-scf` makes a new SCF release current, every
// non-pinned control left on an older release's anchor gets one remap
// proposal (scfimport.RebindControls), classified from the new release's
// legacy IDs as unchanged, renamed, split, reused_id or no_successor. A
// proposal moves nothing; its status says whether the #1971 D3 upgrade
// re-bound the control in place ("rebound") or left it for a person
// ("open"). This endpoint lists the bearer tenant's proposals, filterable
// by ?class= and ?status=.
//
// Authz: the path resolves to resource "controls", action "read", the same
// OPA admit as GET /v1/controls. RLS and the query's explicit tenant id
// keep the read to the bearer's tenant.

package controls

import (
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// remapProposalLister is the store seam the handler reads through; the
// control.Store satisfies it.
type remapProposalLister interface {
	ListRemapProposals(ctx context.Context, class, status *string) ([]dbx.ListSCFRemapProposalsRow, error)
}

// RemapProposalsHandler exposes GET /v1/controls/remap-proposals.
type RemapProposalsHandler struct {
	store remapProposalLister
}

// NewRemapProposalsHandler wires the handler over the control store.
func NewRemapProposalsHandler(store remapProposalLister) *RemapProposalsHandler {
	return &RemapProposalsHandler{store: store}
}

type remapProposalWire struct {
	ID              string   `json:"id"`
	ControlID       string   `json:"control_id"`
	BundleID        string   `json:"bundle_id"`
	Title           string   `json:"title"`
	LegacySCFID     string   `json:"legacy_scf_id"`
	LegacyRelease   string   `json:"legacy_release"`
	TargetRelease   string   `json:"target_release"`
	Class           string   `json:"class"`
	Status          string   `json:"status"`
	SuccessorSCFIDs []string `json:"successor_scf_ids"`
	CreatedAt       string   `json:"created_at"`
}

type remapProposalsResp struct {
	Proposals []remapProposalWire `json:"proposals"`
	Count     int                 `json:"count"`
}

var remapStatuses = []string{scfimport.ProposalOpen, scfimport.ProposalRebound}

// filterParam reads an optional query filter; an empty value means any, and
// a value outside allowed is rejected.
func filterParam(r *http.Request, name string, allowed []string) (*string, bool) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return nil, true
	}
	if !slices.Contains(allowed, v) {
		return nil, false
	}
	return &v, true
}

// List handles GET /v1/controls/remap-proposals. Always 200 with an
// envelope; 400 on an unknown class or status.
func (h *RemapProposalsHandler) List(w http.ResponseWriter, r *http.Request) {
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	classes := make([]string, len(scfimport.RemapClasses))
	for i, c := range scfimport.RemapClasses {
		classes[i] = string(c)
	}
	class, ok := filterParam(r, "class", classes)
	if !ok {
		httpresp.WriteError(w, http.StatusBadRequest, "class must be one of unchanged, renamed, split, reused_id, no_successor")
		return
	}
	status, ok := filterParam(r, "status", remapStatuses)
	if !ok {
		httpresp.WriteError(w, http.StatusBadRequest, "status must be one of open, rebound")
		return
	}
	rows, err := h.store.ListRemapProposals(r.Context(), class, status)
	if err != nil {
		httperr.WriteInternal(w, r, "list scf remap proposals", err)
		return
	}
	out := make([]remapProposalWire, len(rows))
	for i, p := range rows {
		out[i] = remapProposalWire{
			ID:              uuid.UUID(p.ID.Bytes).String(),
			ControlID:       uuid.UUID(p.ControlID.Bytes).String(),
			BundleID:        p.BundleID,
			Title:           p.ControlTitle,
			LegacySCFID:     p.LegacyScfID,
			LegacyRelease:   p.LegacyRelease,
			TargetRelease:   p.TargetRelease,
			Class:           p.Class,
			Status:          p.Status,
			SuccessorSCFIDs: p.SuccessorScfIds,
			CreatedAt:       p.CreatedAt.Time.UTC().Format(time.RFC3339),
		}
		if out[i].SuccessorSCFIDs == nil {
			out[i].SuccessorSCFIDs = []string{}
		}
	}
	httpresp.WriteJSON(w, http.StatusOK, remapProposalsResp{Proposals: out, Count: len(out)})
}
