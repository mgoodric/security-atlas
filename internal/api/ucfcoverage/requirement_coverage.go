package ucfcoverage

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
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
		writeServerErr(w, r, "lookup requirement", err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "requirement not found")
		return
	}

	scfFV := h.resolveSCFRelease(ctx, r)
	anchors, err := h.listAnchorsForRequirement(ctx, req.ID, scfFV)
	if err != nil {
		writeServerErr(w, r, "list anchors for requirement", err)
		return
	}

	anchorIDs := make([]pgtype.UUID, len(anchors))
	for i, a := range anchors {
		anchorIDs[i] = a.scfAnchorID
	}
	controls, err := h.listControlsForAnchors(ctx, anchorIDs)
	if err != nil {
		writeServerErr(w, r, "list controls for anchors", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"requirement": requirementWire{
			ID:    uuidStr(req.ID),
			Code:  req.Code,
			Title: req.Title,
			Body:  req.Body,
		},
		"anchors":  anchorWiresFromAnchors(anchors),
		"controls": controlWiresFromRows(controls),
	})
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
