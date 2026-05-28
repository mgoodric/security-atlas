// Slice 151 — GET /v1/controls list endpoint.
//
// Surfaces the active (non-superseded) controls for the bearer's tenant.
// Driven by the slice-151 risk-create form's control-link multi-select:
// the form needs a list of tenant `controls.id` UUIDs (FK target of
// `risk_control_links.control_id`), not the SCF anchor IDs that the
// pre-existing `/v1/anchors?include=state` route returns. The two
// surfaces are deliberately separate:
//
//   - `/v1/anchors` is the SCF catalog read model (slice 098 + 104).
//   - `/v1/controls` is the tenant control list (this slice).
//
// The BFF at `/api/controls` (slice 098) continues to proxy `/v1/anchors`
// for the existing `/controls` page; the slice-151 BFF at
// `/api/controls-list` proxies THIS endpoint for the risk-create form.
// That asymmetry is intentional and documented in `D-151-3` of the slice
// 151 PR body — preserving the existing controls-page contract while
// adding the missing tenant control list.
//
// Pagination: v1 returns all active controls in a single response. Active
// control counts per-tenant are bounded by the canvas §2.2 erDiagram (a
// 50-150-person security-product startup runs O(50-300) controls), so a
// flat list is sound for v1. Server-side pagination is a v2 concern when
// catalog scale calls for it.
//
// Empty-set robustness: returns `{"controls": [], "count": 0}` for a
// tenant with zero controls — never `null`, never a 500. Mirrors slice
// 150's empty-set convention.

package controls

import (
	"net/http"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ListHandler exposes GET /v1/controls. Separate type from the slice-009
// upload Handler so the dependencies stay scoped to what each route
// actually needs.
type ListHandler struct {
	store *control.Store
}

// NewListHandler wires a ListHandler over the same control.Store the
// upload handler uses.
func NewListHandler(store *control.Store) *ListHandler {
	return &ListHandler{store: store}
}

// activeControlWire is the on-the-wire shape of a single row. Field
// selection matches the slice-151 risk-create multi-select's render
// needs: caller wants a stable id, a human-readable title, and enough
// metadata to disambiguate when titles collide (family + SCF code).
//
// `lifecycle_state` is included so future consumers can dim or filter
// non-`active` rows; the slice-151 form treats every returned row as a
// valid pick (the sqlc query already filters superseded rows).
type activeControlWire struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	ControlFamily  string `json:"control_family"`
	SCFID          string `json:"scf_id"`
	LifecycleState string `json:"lifecycle_state"`
	BundleID       string `json:"bundle_id"`
}

type listResp struct {
	Controls []activeControlWire `json:"controls"`
	Count    int                 `json:"count"`
}

// List handles GET /v1/controls — returns every active control for the
// bearer's tenant. Always 200 with an envelope; an empty tenant returns
// `{"controls": [], "count": 0}` (slice 150 convention).
func (h *ListHandler) List(w http.ResponseWriter, r *http.Request) {
	// Slice 033: the tenancy middleware (mounted after bearer-auth) has
	// already lifted the credential's tenant id into r.Context() via
	// tenancy.WithTenant. We confirm; a missing tenant means the route
	// was reached without a credential (misconfig) — 401 keeps the
	// shape consistent with the other bearer-auth'd handlers.
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		writeError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}

	rows, err := h.store.List(r.Context())
	if err != nil {
		httperr.WriteInternal(w, r, "list controls", err)
		return
	}

	out := make([]activeControlWire, len(rows))
	for i, c := range rows {
		out[i] = activeControlWire{
			ID:             c.ID.String(),
			Title:          c.Title,
			ControlFamily:  c.ControlFamily,
			SCFID:          c.SCFID,
			LifecycleState: c.LifecycleState,
			BundleID:       c.BundleID,
		}
	}
	writeJSON(w, http.StatusOK, listResp{Controls: out, Count: len(out)})
}
