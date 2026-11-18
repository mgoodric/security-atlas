// Issue #2358 — stock controls whose shipped update the boot seed holds
// back.
//
//	GET /v1/admin/controls/held-back
//
// The boot seed keeps a person's reviewed lineage of a stock control (an
// approved SCF re-anchor, or an edit on top of one) rather than apply the
// shipped manifest, and a later shipped fix to that control is held back
// with it, on every boot (docs/audit-log/1776-*, D1a). This route lists
// those controls with the approved revision and the shipped manifest the
// tenant is missing, so the hold is not visible only in a boot log.
//
// Authz: tenant admin only. BuildInput derives read on admin/controls;
// admin.rego admits a tenant admin, and the rego bundle's
// tenant_admin_only_resource carve-out refuses grc_engineer, whose blanket
// read would otherwise admit it. The handler checks authz.IsTenantAdmin, the
// live-role gate, as the second leg. RLS keeps the list to the caller's
// tenant.

package controls

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/control"
)

// heldBackLister is the store seam; the control.Store satisfies it.
type heldBackLister interface {
	ListHeldBackStockControls(ctx context.Context) ([]control.HeldBackStockControl, error)
}

// HeldBackHandler serves the held-back stock control list.
type HeldBackHandler struct {
	store heldBackLister
}

// NewHeldBackHandler wires the handler over the control store.
func NewHeldBackHandler(store heldBackLister) *HeldBackHandler {
	return &HeldBackHandler{store: store}
}

// RegisterRoutes mounts the route.
func (h *HeldBackHandler) RegisterRoutes(r chi.Router) {
	r.Get("/v1/admin/controls/held-back", h.List)
}

// HeldBackResponse is the GET body.
type HeldBackResponse struct {
	Controls []control.HeldBackStockControl `json:"controls"`
}

// List handles GET /v1/admin/controls/held-back.
func (h *HeldBackHandler) List(w http.ResponseWriter, r *http.Request) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "missing credential")
		return
	}
	if !authz.IsTenantAdmin(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "tenant-admin role required")
		return
	}
	cs, err := h.store.ListHeldBackStockControls(r.Context())
	if err != nil {
		httperr.WriteInternal(w, r, "list held-back stock controls", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, HeldBackResponse{Controls: cs})
}
