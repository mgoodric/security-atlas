// Package admincrosswalktier is the admin HTTP surface for the slice-483
// crosswalk-mapping verified-tier governance: POST
// /v1/admin/crosswalk-edges/{id}/tier transitions a mapping's trust tier.
//
// The route requires an ADMIN atlas credential (cred.IsAdmin) — promotion to
// verified is the trust act and a privileged catalog-write capability (ADR 0018
// §3 / threat-model E / AC-4). A non-admin caller gets 403; this is the
// load-bearing E mitigation, asserted in the integration test. ADR 0018 chose
// "any admin/maintainer role" over super_admin-only, so this reuses the same
// cred.IsAdmin gate the slice-509 admingroupmappings surface uses.
//
// fw_to_scf_edges is a CATALOG table (no tenant_id, no RLS): the trust gate is
// this admin-role authz check plus the append-only audit trail the store writes
// in the same transaction — NOT the tenant-RLS pattern.
package admincrosswalktier

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
	"github.com/mgoodric/security-atlas/internal/crosswalktier"
)

const maxBody = 16 * 1024

// Handler owns the admin crosswalk-tier transition route.
type Handler struct {
	store *crosswalktier.Store
}

// New constructs a Handler over the transition store.
func New(store *crosswalktier.Store) *Handler { return &Handler{store: store} }

// TransitionRequest is the POST body. tier is the target trust tier; note is
// the reviewer's free-text rationale (optional).
type TransitionRequest struct {
	Tier string `json:"tier"`
	Note string `json:"note,omitempty"`
}

// TransitionResponse is the success shape. Reviewer identity IS returned here
// because this is the admin surface (not the public /anchors payload); the
// caller is the admin who just performed the act.
type TransitionResponse struct {
	EdgeID     string `json:"edge_id"`
	FromTier   string `json:"from_tier"`
	ToTier     string `json:"to_tier"`
	ReviewerID string `json:"reviewer_id"`
	Note       string `json:"note,omitempty"`
	CreatedAt  string `json:"created_at"`
}

// Transition handles POST /v1/admin/crosswalk-edges/{id}/tier (AC-4).
func (h *Handler) Transition(w http.ResponseWriter, r *http.Request) {
	cred, ok := requireAdmin(w, r)
	if !ok {
		return
	}

	edgeID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid edge id")
		return
	}

	var req TransitionRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&req); err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	to, err := crosswalktier.ParseTier(req.Tier)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "tier must be one of draft, under_review, verified, rejected")
		return
	}

	reviewer, err := uuid.Parse(jwtmw.SubjectUserID(cred.UserID))
	if err != nil {
		// A verified admin JWT always carries a parseable user subject; if it
		// does not, fail closed rather than write an audit row with a nil
		// reviewer.
		httpresp.WriteError(w, http.StatusForbidden, "admin credential lacks a resolvable user id")
		return
	}

	t, err := h.store.Transition(r.Context(), crosswalktier.TransitionInput{
		EdgeID:     edgeID,
		ToTier:     to,
		ReviewerID: reviewer,
		Note:       req.Note,
	})
	switch {
	case errors.Is(err, crosswalktier.ErrEdgeNotFound):
		httpresp.WriteError(w, http.StatusNotFound, "unknown crosswalk edge id")
		return
	case errors.Is(err, crosswalktier.ErrUnknownTier):
		httpresp.WriteError(w, http.StatusBadRequest, "tier must be one of draft, under_review, verified, rejected")
		return
	case errors.Is(err, crosswalktier.ErrIllegalTransition):
		httpresp.WriteError(w, http.StatusUnprocessableEntity, "illegal tier transition")
		return
	case err != nil:
		httperr.WriteInternal(w, r, "transition crosswalk tier", err)
		return
	}

	httpresp.WriteJSON(w, http.StatusOK, TransitionResponse{
		EdgeID:     t.EdgeID.String(),
		FromTier:   string(t.FromTier),
		ToTier:     string(t.ToTier),
		ReviewerID: t.ReviewerID.String(),
		Note:       t.Note,
		CreatedAt:  t.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	})
}

// --- helpers ---

type adminCred struct {
	TenantID string
	UserID   string
}

// requireAdmin enforces the admin gate (AC-4 / threat-model E). A missing
// credential is 401; a non-admin credential is 403. Authority is enforced
// server-side, never in the UI.
func requireAdmin(w http.ResponseWriter, r *http.Request) (adminCred, bool) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "missing credential")
		return adminCred{}, false
	}
	if !cred.IsAdmin {
		httpresp.WriteError(w, http.StatusForbidden, "admin credential required")
		return adminCred{}, false
	}
	return adminCred{TenantID: cred.TenantID, UserID: cred.UserID}, true
}
