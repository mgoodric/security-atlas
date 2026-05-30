package ucfcoverage

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/frameworkscope"
)

// ===== /v1/controls/{id}/coverage =====

// ControlCoverage handles GET /v1/controls/{id}/coverage (AC-3).
//
// Path: UUID-only — controls are tenant-scoped so the natural-key
// approach used for requirements doesn't apply (the same bundle_id can
// exist in multiple tenants).
//
// Query parameters:
//   - ?framework_version=slug:version — pin to one framework version
//
// Returns:
//   - 200 { control, anchor, requirements[] } where anchor is null when
//     the control has no scf_anchor_id (not 404 — the control still
//     exists; it just isn't anchored to the canonical graph yet)
//   - 404 when the control id doesn't resolve in the caller's tenant
//   - 400 when the path segment isn't a UUID
//   - 401 when bearer auth is missing
//
// Tenant isolation: the control lookup runs inside a tenant-tx so RLS
// filters foreign rows. Cross-tenant traversal returns 404 because the
// caller cannot see the foreign control row at all.
func (h *Handler) ControlCoverage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := chi.URLParam(r, "id")
	cid, err := uuid.Parse(idStr)
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, "control id must be a UUID")
		return
	}

	var (
		controlOK bool
		ctrl      dbx.GetControlByIDRow
	)
	if err := h.inTenantTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		got, err := q.GetControlByID(ctx, dbx.GetControlByIDParams{
			TenantID: pgUUIDFromTenantCtx(ctx),
			ID:       pgtype.UUID{Bytes: cid, Valid: true},
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		ctrl = got
		controlOK = true
		return nil
	}); err != nil {
		httperr.WriteInternal(w, r, "lookup control", err)
		return
	}
	if !controlOK {
		httpresp.WriteError(w, http.StatusNotFound, "control not found")
		return
	}

	out := map[string]any{
		"control": controlWireFromControlRow(ctrl),
	}
	if !ctrl.ScfAnchorID.Valid {
		// Control exists but isn't anchored. Return 200 with null
		// anchor + empty requirements. The dashboard surfaces this as
		// "not yet mapped to the canonical graph."
		out["anchor"] = nil
		out["requirements"] = []requirementForAnchorWire{}
		httpresp.WriteJSON(w, http.StatusOK, out)
		return
	}

	// Anchor metadata: pull from scf_anchors directly (catalog read).
	anchor, err := h.q.GetSCFAnchorByID(ctx, ctrl.ScfAnchorID)
	if err != nil {
		httperr.WriteInternal(w, r, "lookup control anchor", err)
		return
	}
	out["anchor"] = anchorWireFromRow(anchor)

	var reqs []requirementForAnchorWire
	fvParam := r.URL.Query().Get("framework_version")
	if fvParam != "" {
		fv, ok := h.resolveFrameworkVersion(ctx, fvParam)
		if !ok {
			out["requirements"] = []requirementForAnchorWire{}
			httpresp.WriteJSON(w, http.StatusOK, out)
			return
		}
		got, err := h.q.ListRequirementsForAnchorByFrameworkVersion(ctx, dbx.ListRequirementsForAnchorByFrameworkVersionParams{
			ScfAnchorID:        ctrl.ScfAnchorID,
			FrameworkVersionID: fv.ID,
		})
		if err != nil {
			httperr.WriteInternal(w, r, "list requirements for control (pinned)", err)
			return
		}
		reqs = mapPinnedRequirements(got)
	} else {
		got, err := h.q.ListRequirementsForAnchor(ctx, ctrl.ScfAnchorID)
		if err != nil {
			httperr.WriteInternal(w, r, "list requirements for control", err)
			return
		}
		reqs = mapRequirements(got)
	}

	// Slice 256 — per-row weighted Coverage column.
	//
	// Coverage[i] = strength[i] × 30d_pass_rate     when the requirement's
	//                                                framework_version is in
	//                                                scope for this control
	//             = null                            when out of scope OR
	//                                                no effectiveness data
	//
	// The "no effectiveness data" rule (TotalCount == 0) maps to JSON null
	// per AC-2: callers must distinguish "no data yet" from "perfectly
	// failing" (0). Effectiveness is computed once per request — it's a
	// per-control rollup, not per-row — and reused across every
	// requirement. Out-of-scope determination mirrors the slice-018
	// /effective-scope endpoint: control.applicability ∩
	// framework_scope.predicate; empty intersection => out of scope =>
	// coverage null. Wiring is opt-in (h.engine/h.scopeStore/h.fwScopeStore
	// all non-nil); unit servers that didn't wire them leave the field
	// omitted entirely, preserving the slice-008 shape.
	if h.engine != nil && h.scopeStore != nil && h.fwScopeStore != nil && len(reqs) > 0 {
		if err := h.applyCoverage(ctx, cid, reqs); err != nil {
			httperr.WriteInternal(w, r, "compute coverage", err)
			return
		}
	}

	out["requirements"] = reqs
	httpresp.WriteJSON(w, http.StatusOK, out)
}

// applyCoverage fills the `coverage` field on each requirement in `reqs`.
// Computes 30-day effectiveness once, then resolves in-scope/out-of-scope
// per distinct framework_version_id (one /effective-scope intersection
// per fv), then assigns coverage = strength × pass_rate when in scope and
// effectiveness has any data, else nil.
//
// Errors propagate to the caller; this is on the request-serving path
// so a transient DB error must surface as a 500 rather than a silently
// zeroed Coverage column. The function mutates `reqs` in place.
func (h *Handler) applyCoverage(ctx context.Context, controlID uuid.UUID, reqs []requirementForAnchorWire) error {
	// 30-day pass rate for the whole control (canvas §6.2
	// operational_score). Computed once per request — every row in the
	// table shares the same multiplier.
	eff, err := h.engine.Effectiveness(ctx, controlID)
	if err != nil {
		return fmt.Errorf("effectiveness: %w", err)
	}
	hasEffectivenessData := eff.TotalCount > 0
	passRate := eff.PassRate

	// Resolve in-scope per distinct framework_version_id. The
	// frameworkscope.Store.Activated lookup is cheap (single-row SELECT)
	// and the scope intersection is in-memory over the control's
	// applicability set — but doing it once per fv (not once per row)
	// keeps the table O(rows + fvs) rather than O(rows × fvs).
	applicability, err := h.scopeStore.ControlApplicability(ctx, controlID)
	if err != nil {
		return fmt.Errorf("control applicability: %w", err)
	}
	inScopeByFV := make(map[string]bool, 4)
	for _, req := range reqs {
		fvIDStr := req.FrameworkVersionID
		if _, seen := inScopeByFV[fvIDStr]; seen {
			continue
		}
		fvID, perr := uuid.Parse(fvIDStr)
		if perr != nil {
			// A malformed UUID on a catalog row would be a slice-007 bug,
			// not a request-shape bug. Treat as out-of-scope so the row
			// renders n/a rather than 500-ing the whole response.
			inScopeByFV[fvIDStr] = false
			continue
		}
		activated, aerr := h.fwScopeStore.Activated(ctx, fvID)
		if aerr != nil {
			if errors.Is(aerr, frameworkscope.ErrNotFound) {
				// No activated framework_scope → no audit-bound predicate
				// → effectively out of scope (canvas §5.5; matches the
				// slice-018 EffectiveScope handler's behavior).
				inScopeByFV[fvIDStr] = false
				continue
			}
			return fmt.Errorf("activated framework_scope: %w", aerr)
		}
		cells, ierr := frameworkscope.EffectiveScope(ctx, applicability, activated.Predicate)
		if ierr != nil {
			return fmt.Errorf("intersect: %w", ierr)
		}
		inScopeByFV[fvIDStr] = len(cells) > 0
	}

	for i := range reqs {
		inScope := inScopeByFV[reqs[i].FrameworkVersionID]
		if !inScope || !hasEffectivenessData {
			reqs[i].Coverage = nil
			continue
		}
		v := reqs[i].Strength * passRate
		reqs[i].Coverage = &v
	}
	return nil
}

// controlWireFromControlRow maps a GetControlByIDRow to the controlWire
// JSON shape (ControlCoverage's `control` field).
func controlWireFromControlRow(c dbx.GetControlByIDRow) controlWire {
	w := controlWire{
		ID:                 uuidStr(c.ID),
		BundleID:           c.BundleID,
		Version:            c.Version,
		SCFAnchorID:        uuidStr(c.ScfAnchorID),
		Title:              c.Title,
		ControlFamily:      c.ControlFamily,
		ImplementationType: string(c.ImplementationType),
		OwnerRole:          c.OwnerRole,
		LifecycleState:     string(c.LifecycleState),
	}
	if c.ScfID != nil {
		w.SCFID = *c.ScfID
	}
	if c.FreshnessClass != nil {
		w.FreshnessClass = *c.FreshnessClass
	}
	return w
}
