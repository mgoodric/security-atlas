package ucfcoverage

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/frameworkscope"
)

// ===== /v1/requirements/{id}/coverage =====

// RequirementCoverage handles GET /v1/requirements/{id}/coverage.
//
// Path forms accepted (matches slice-007 convention):
//   - UUID — direct framework_requirements.id lookup
//   - `{slug}:{version}:{code}` — natural key, e.g. `soc2:2017:CC6.6`
//   - `{slug}::{code}` — convenience, resolves against the framework's
//     "current" version, e.g. `soc2::CC6.6`
//
// Query parameters:
//   - ?framework_version=slug:version — pin the SCF release to a
//     specific framework_version_id (AC-4). When the slug:version pair
//     doesn't resolve, the route still returns 200 with anchors+controls
//     from the unpinned view; slice 012 will tighten the contract.
//   - ?as-of=<RFC3339> — accepted no-op; slice 012 will wire evidence
//     filtering. Caller-friendly so dashboards can start sending the
//     param ahead of slice 012's eval engine.
//   - ?scf_release=<version> — accepted no-op until multiple SCF
//     releases are importable in the same DB.
//
// Returns:
//   - 200 { requirement, anchors[], controls[] } on success
//   - 404 when the requirement id doesn't resolve in any of the three
//     accepted forms
//   - 401 when bearer auth is missing (middleware-handled)
//
// Cross-tenant note: the `requirement` and `anchors` fields are global
// catalog data and visible to any authenticated tenant. The `controls`
// array is RLS-scoped — a tenant traversing a foreign control's
// requirement sees an empty controls list, which is the correct shape
// (canvas §3.5).
func (h *Handler) RequirementCoverage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	req, ok, err := h.lookupRequirement(ctx, chi.URLParam(r, "id"))
	if err != nil {
		httperr.WriteInternal(w, r, "lookup requirement", err)
		return
	}
	if !ok {
		httpresp.WriteError(w, http.StatusNotFound, "requirement not found")
		return
	}

	scfFV := h.resolveSCFRelease(ctx, r)
	anchors, err := h.listAnchorsForRequirement(ctx, req.ID, scfFV)
	if err != nil {
		httperr.WriteInternal(w, r, "list anchors for requirement", err)
		return
	}

	anchorIDs := make([]pgtype.UUID, len(anchors))
	for i, a := range anchors {
		anchorIDs[i] = a.scfAnchorID
	}
	controls, err := h.listControlsForAnchors(ctx, anchorIDs)
	if err != nil {
		httperr.WriteInternal(w, r, "list controls for anchors", err)
		return
	}

	// Slice 482 — per-requirement coverage-strength rollup. Computed
	// server-side (P0-482-4: never client-supplied) over the per-anchor
	// edge strength × the tenant's evaluated coverage state, and ONLY
	// over the RLS-scoped `controls` rows above (P0-482-3 + AC-2: a
	// foreign tenant's controls are invisible at the DB layer, so its
	// rollup reflects its own state or resolves to uncovered — never
	// tenant A's). When the eval/scope stores aren't wired (unit servers
	// built via New without AttachCoverage) the rollup defaults to the
	// uncovered band rather than 500-ing, preserving the slice-008 shape
	// plus the additive fields.
	score, band := h.requirementRollup(ctx, req, anchors, controls)

	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"requirement": requirementWire{
			ID:    uuidStr(req.ID),
			Code:  req.Code,
			Title: req.Title,
			Body:  req.Body,
		},
		"anchors":           anchorWiresFromAnchors(anchors),
		"controls":          controlWiresFromRows(controls),
		"coverage_strength": score,
		"confidence_band":   string(band),
	})

}

// requirementRollup computes the additive coverage_strength + confidence
// band for one requirement (slice 482, AC-1/AC-2/AC-3).
//
// For each RLS-scoped control on the requirement's anchors it computes
// the same per-control coverage slice 256 surfaces on
// /v1/controls/{id}/coverage — strength-independent 30-day effectiveness
// pass rate, gated by whether the requirement's framework_version is in
// FrameworkScope for that control. The per-anchor coverage is the BEST
// (max) such pass rate over the controls on that anchor; the
// requirement score is the best-satisfying-path over anchors:
// max(edge_strength × anchor_coverage). See rollup.go for the formula
// rationale and docs/audit-log/482-coverage-strength-rollup-decisions.md
// for the JUDGMENT calls.
//
// Defensive default: any error computing the tenant-evaluated state, or
// an unwired Handler (engine/scope/fwScope nil), yields the uncovered
// band. The rollup is a display value, not an audit-binding artifact
// (threat-model R) — degrading to "uncovered" on a transient eval error
// is safer than 500-ing the whole coverage read, and never over-reports.
func (h *Handler) requirementRollup(
	ctx context.Context,
	req dbx.FrameworkRequirement,
	anchors []anchorEdge,
	controls []dbx.ListControlsForAnchorsRow,
) (float64, ConfidenceBand) {
	if h.engine == nil || h.scopeStore == nil || h.fwScopeStore == nil || len(controls) == 0 {
		return 0, BandUncovered
	}

	// Best evaluated coverage per anchor id, over the tenant's controls.
	bestCover := make(map[string]float64, len(anchors))
	hasCover := make(map[string]bool, len(anchors))
	reqFVID := uuidStr(req.FrameworkVersionID)

	for _, c := range controls {
		cover, ok, err := h.controlCoverageForFramework(ctx, c, reqFVID)
		if err != nil {
			// A transient eval/scope error on one control degrades that
			// control's contribution to "no coverage" rather than failing
			// the whole rollup; the score never over-reports.
			continue
		}
		if !ok {
			continue
		}
		aid := uuidStr(c.ScfAnchorID)
		if !hasCover[aid] || cover > bestCover[aid] {
			bestCover[aid] = cover
			hasCover[aid] = true
		}
	}

	acs := make([]anchorCoverage, 0, len(anchors))
	for _, a := range anchors {
		aid := uuidStr(a.scfAnchorID)
		acs = append(acs, anchorCoverage{
			edgeStrength: a.strength,
			anchorCover:  bestCover[aid],
			hasCoverage:  hasCover[aid],
		})
	}

	score, any := rollupCoverageStrength(acs)
	return score, classifyBand(score, any)
}

// controlCoverageForFramework returns the tenant-evaluated coverage of
// one control AS IT APPLIES to the requirement's framework_version:
// the 30-day effectiveness pass rate when (a) the control has evaluation
// data in the window AND (b) the requirement's framework_version is in
// the control's FrameworkScope. Returns (_, false, nil) when out of
// scope or when the control has no effectiveness data — both map to "no
// contribution" so the anchor isn't credited with phantom coverage
// (mirrors slice 256 applyCoverage's null contract, lifted to the
// rollup). The strength multiply happens in rollupCoverageStrength, not
// here — this returns the anchor-coverage term only.
func (h *Handler) controlCoverageForFramework(
	ctx context.Context,
	c dbx.ListControlsForAnchorsRow,
	reqFVID string,
) (float64, bool, error) {
	controlID, err := uuid.Parse(uuidStr(c.ID))
	if err != nil {
		return 0, false, nil
	}

	eff, err := h.engine.Effectiveness(ctx, controlID)
	if err != nil {
		return 0, false, err
	}
	if eff.TotalCount == 0 {
		// No effectiveness data — distinct from "0% effective". Does not
		// contribute coverage (slice 256 P0-256-1, lifted to the rollup).
		return 0, false, nil
	}

	if reqFVID == "" {
		return 0, false, nil
	}
	fvID, perr := uuid.Parse(reqFVID)
	if perr != nil {
		return 0, false, nil
	}
	activated, aerr := h.fwScopeStore.Activated(ctx, fvID)
	if aerr != nil {
		if errors.Is(aerr, frameworkscope.ErrNotFound) {
			// No activated framework_scope → effectively out of scope
			// (canvas §5.5; matches slice 256 applyCoverage).
			return 0, false, nil
		}
		return 0, false, aerr
	}
	applicability, err := h.scopeStore.ControlApplicability(ctx, controlID)
	if err != nil {
		return 0, false, err
	}
	cells, ierr := frameworkscope.EffectiveScope(ctx, applicability, activated.Predicate)
	if ierr != nil {
		return 0, false, ierr
	}
	if len(cells) == 0 {
		return 0, false, nil // out of scope
	}
	return eff.PassRate, true, nil
}

// anchorEdge is the internal in-memory shape of "one SCF anchor with
// the STRM edge metadata from a specific requirement." Used to keep
// RequirementCoverage's two code paths (pinned vs unpinned) producing
// the same wire shape without duplicating the JSON struct.
type anchorEdge struct {
	edgeID            pgtype.UUID
	scfAnchorID       pgtype.UUID
	scfID             string
	family            string
	anchorTitle       string
	anchorDescription string
	relationshipType  string
	strength          float64
	sourceAttribution string
	mappingTier       string
	rationale         string
}

// listAnchorsForRequirement runs either the unpinned or pinned variant
// based on whether an SCF framework_version filter was supplied.
func (h *Handler) listAnchorsForRequirement(ctx context.Context, reqID pgtype.UUID, scfFV *dbx.FrameworkVersion) ([]anchorEdge, error) {
	if scfFV == nil {
		rows, err := h.q.ListAnchorsForRequirementWithEdges(ctx, reqID)
		if err != nil {
			return nil, err
		}
		out := make([]anchorEdge, len(rows))
		for i, r := range rows {
			out[i] = anchorEdge{
				edgeID:            r.EdgeID,
				scfAnchorID:       r.ScfAnchorID,
				scfID:             r.ScfID,
				family:            r.Family,
				anchorTitle:       r.AnchorTitle,
				anchorDescription: r.AnchorDescription,
				relationshipType:  string(r.RelationshipType),
				strength:          r.Strength,
				sourceAttribution: string(r.SourceAttribution),
				mappingTier:       string(r.MappingTier),
				rationale:         r.Rationale,
			}
		}
		return out, nil
	}
	rows, err := h.q.ListAnchorsForRequirementWithEdgesByFrameworkVersion(ctx, dbx.ListAnchorsForRequirementWithEdgesByFrameworkVersionParams{
		FrameworkRequirementID: reqID,
		FrameworkVersionID:     scfFV.ID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]anchorEdge, len(rows))
	for i, r := range rows {
		out[i] = anchorEdge{
			edgeID:            r.EdgeID,
			scfAnchorID:       r.ScfAnchorID,
			scfID:             r.ScfID,
			family:            r.Family,
			anchorTitle:       r.AnchorTitle,
			anchorDescription: r.AnchorDescription,
			relationshipType:  string(r.RelationshipType),
			strength:          r.Strength,
			sourceAttribution: string(r.SourceAttribution),
			rationale:         r.Rationale,
		}
	}
	return out, nil
}

// listControlsForAnchors runs the tenant-scoped controls lookup inside
// the request's `app.current_tenant` GUC (set by tenancy.Middleware via
// tenancy.WithTenant). RLS does the filtering — no `WHERE tenant_id`
// clause is present in the SQL.
func (h *Handler) listControlsForAnchors(ctx context.Context, anchorIDs []pgtype.UUID) ([]dbx.ListControlsForAnchorsRow, error) {
	if len(anchorIDs) == 0 {
		return nil, nil
	}
	var out []dbx.ListControlsForAnchorsRow
	if err := h.inTenantTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		got, err := q.ListControlsForAnchors(ctx, anchorIDs)
		if err != nil {
			return err
		}
		out = got
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// resolveSCFRelease parses ?scf_release=<version> into a framework_version
// row for the SCF framework. Returns nil on absence (no filter) or
// when the version doesn't resolve. Distinct from
// resolveFrameworkVersion because the slug is fixed to "scf".
func (h *Handler) resolveSCFRelease(ctx context.Context, r *http.Request) *dbx.FrameworkVersion {
	v := r.URL.Query().Get("scf_release")
	if v == "" {
		return nil
	}
	row, err := h.q.GetFrameworkVersionBySlugAndVersion(ctx, dbx.GetFrameworkVersionBySlugAndVersionParams{
		Slug:    "scf",
		Version: v,
	})
	if err != nil {
		return nil
	}
	return &row
}

// anchorWiresFromAnchors maps the internal anchorEdge slice to the
// anchorWire JSON shape (RequirementCoverage's `anchors` field).
func anchorWiresFromAnchors(anchors []anchorEdge) []anchorWire {
	out := make([]anchorWire, 0, len(anchors))
	for _, a := range anchors {
		out = append(out, anchorWire{
			ID:                uuidStr(a.scfAnchorID),
			SCFID:             a.scfID,
			Family:            a.family,
			Name:              a.anchorTitle,
			Description:       a.anchorDescription,
			EdgeID:            uuidStr(a.edgeID),
			RelationshipType:  a.relationshipType,
			Strength:          a.strength,
			SourceAttribution: a.sourceAttribution,
			MappingTier:       a.mappingTier,
			Rationale:         a.rationale,
		})
	}
	return out
}

// controlWiresFromRows maps the tenant-scoped controls lookup rows to the
// controlWire JSON shape (RequirementCoverage's `controls` field).
func controlWiresFromRows(rows []dbx.ListControlsForAnchorsRow) []controlWire {
	out := make([]controlWire, 0, len(rows))
	for _, c := range rows {
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
		out = append(out, w)
	}
	return out
}
