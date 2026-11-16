// Overlay-edge remap proposals over HTTP (#1804):
//
//	GET  /v1/mapping-overlays/remap-proposals                   list (?class, ?status)
//	POST /v1/mapping-overlays/remap-proposals/{id}/approve      {"scf_id"?, "reason"?}
//	POST /v1/mapping-overlays/remap-proposals/{id}/reject       {"reason"?}
//	POST /v1/mapping-overlays/remap-proposals/{id}/keep-legacy  {"reason"?}
//
// The proposals are tenant-owned, so a tenant writer (admin, grc_engineer)
// decides them, not a super-admin: OPA admits them on resource
// `mapping-overlays`, and requireOverlayWrite is the in-handler twin. On top
// of the role, a decision is a person's (#2231): a machine credential is
// refused, and the decider is recorded as credstore.PrincipalOf. RLS keeps
// every read and write to the caller's tenant, so another tenant's proposal
// id is a 404.

package mappingoverlays

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/approval"
	"github.com/mgoodric/security-atlas/internal/mappingoverlay"
)

// RegisterRemapRoutes mounts the remap proposal routes. The literal
// remap-proposals segment sits beside /v1/mapping-overlays/{id}/retire;
// chi prefers the literal.
func (h *Handler) RegisterRemapRoutes(r chi.Router) {
	r.Get("/v1/mapping-overlays/remap-proposals", h.ListRemap)
	r.Post("/v1/mapping-overlays/remap-proposals/{id}/approve", h.ApproveRemap)
	r.Post("/v1/mapping-overlays/remap-proposals/{id}/reject", h.RejectRemap)
	r.Post("/v1/mapping-overlays/remap-proposals/{id}/keep-legacy", h.KeepLegacyRemap)
}

// remapProposalWire is a proposal on the wire.
type remapProposalWire struct {
	ID               string    `json:"id"`
	OverlayEdgeID    string    `json:"overlay_edge_id"`
	Framework        string    `json:"framework"`
	FrameworkVersion string    `json:"framework_version"`
	RequirementCode  string    `json:"requirement_code"`
	RelationshipType string    `json:"relationship_type"`
	LegacySCFID      string    `json:"legacy_scf_id"`
	LegacyRelease    string    `json:"legacy_release"`
	TargetRelease    string    `json:"target_release"`
	Class            string    `json:"class"`
	Status           string    `json:"status"`
	SuccessorSCFIDs  []string  `json:"successor_scf_ids"`
	TargetSCFID      string    `json:"target_scf_id,omitempty"`
	NewOverlayEdgeID string    `json:"new_overlay_edge_id,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

func remapWireFrom(p mappingoverlay.RemapProposal) remapProposalWire {
	w := remapProposalWire{
		ID: p.ID.String(), OverlayEdgeID: p.OverlayEdgeID.String(), Framework: p.Framework,
		FrameworkVersion: p.FrameworkVersion, RequirementCode: p.RequirementCode,
		RelationshipType: p.RelationshipType, LegacySCFID: p.LegacySCFID, LegacyRelease: p.LegacyRelease,
		TargetRelease: p.TargetRelease, Class: p.Class, Status: p.Status, SuccessorSCFIDs: p.Successors,
		TargetSCFID: p.TargetSCFID, CreatedAt: p.CreatedAt,
	}
	if w.SuccessorSCFIDs == nil {
		w.SuccessorSCFIDs = []string{}
	}
	if p.NewOverlayEdgeID != nil {
		w.NewOverlayEdgeID = p.NewOverlayEdgeID.String()
	}
	return w
}

// ListRemap handles GET /v1/mapping-overlays/remap-proposals.
func (h *Handler) ListRemap(w http.ResponseWriter, r *http.Request) {
	if !requireTenant(w, r) {
		return
	}
	q := r.URL.Query()
	props, err := h.store.ListRemapProposals(r.Context(), mappingoverlay.RemapFilter{
		Class: q.Get("class"), Status: q.Get("status"),
	})
	if err != nil {
		writeErr(w, r, "list overlay remap proposals", err)
		return
	}
	out := make([]remapProposalWire, len(props))
	for i, p := range props {
		out[i] = remapWireFrom(p)
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{"proposals": out, "count": len(out)})
}

type remapDecisionReq struct {
	SCFID  string `json:"scf_id"`
	Reason string `json:"reason"`
}

// ApproveRemap handles POST /v1/mapping-overlays/remap-proposals/{id}/approve.
func (h *Handler) ApproveRemap(w http.ResponseWriter, r *http.Request) {
	h.decideRemap(w, r, mappingoverlay.RemapApproved)
}

// RejectRemap handles POST /v1/mapping-overlays/remap-proposals/{id}/reject.
func (h *Handler) RejectRemap(w http.ResponseWriter, r *http.Request) {
	h.decideRemap(w, r, mappingoverlay.RemapRejected)
}

// KeepLegacyRemap handles POST /v1/mapping-overlays/remap-proposals/{id}/keep-legacy.
func (h *Handler) KeepLegacyRemap(w http.ResponseWriter, r *http.Request) {
	h.decideRemap(w, r, mappingoverlay.RemapKeptLegacy)
}

func (h *Handler) decideRemap(w http.ResponseWriter, r *http.Request, decision string) {
	if !requireOverlayWrite(w, r) || !requireTenant(w, r) {
		return
	}
	actor, ok := actorFrom(w, r)
	if !ok {
		return
	}
	decider, ok := remapDecidingPerson(w, r)
	if !ok {
		return
	}
	var req remapDecisionReq
	if !decodeStrict(w, r, &req) {
		return
	}
	// Only an approval lands on an anchor.
	if decision != mappingoverlay.RemapApproved && req.SCFID != "" {
		httpresp.WriteError(w, http.StatusBadRequest, "scf_id applies to approve only")
		return
	}
	d, err := h.store.DecideRemap(r.Context(), mappingoverlay.RemapDecideInput{
		ProposalID: chi.URLParam(r, "id"), Decision: decision, SCFID: req.SCFID,
		Reason: req.Reason, Actor: actor, DecidedBy: decider,
	})
	if err != nil {
		writeErr(w, r, "decide overlay remap proposal", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, d)
}

// remapDecidingPerson returns PrincipalOf the caller when the caller is a
// person, as the control remap decisions do (#1776). A proposal has no
// requester (the import made it), so approval.Check applies only its
// people-only rule.
func remapDecidingPerson(w http.ResponseWriter, r *http.Request) (string, bool) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "missing credential")
		return "", false
	}
	if _, err := approval.Check("", approval.Approver{UserID: credstore.MinterUserID(cred), CredentialID: cred.ID}, false); err != nil {
		httpresp.WriteError(w, http.StatusForbidden,
			"a person must decide a remap proposal: sign in as a user, or use an API key issued to one; a machine credential cannot")
		return "", false
	}
	return credstore.PrincipalOf(cred), true
}
