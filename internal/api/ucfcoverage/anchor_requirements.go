package ucfcoverage

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/scfrelease"
)

// ===== /v1/anchors/{id}/requirements =====

// AnchorRequirements handles GET /v1/anchors/{id}/requirements (AC-2 /
// reverse traversal). Replaces the slice-006 in-memory placeholder on
// the same path with DB-backed traversal.
//
// Path forms accepted:
//   - UUID — direct scf_anchors.id lookup
//   - scf_id (e.g., "IAC-37") — natural key lookup
//
// Query parameters:
//   - ?framework_version=slug:version — pin the response to one
//     framework_version (e.g. `soc2:2017`). When the param is absent,
//     every framework version is included.
//   - ?scf_release=<label> — resolve an scf_id within that SCF release
//     (#1770). Absent, an scf_id resolves in the current release and a
//     UUID in whichever release holds it. `scf_release` in the response
//     names the release the anchor was read from.
//
// Returns:
//   - 200 { anchor, requirements[], scf_release }
//   - 404 when the anchor id doesn't resolve, the scf_release is not
//     loaded, or the scf_id is not in that release
//   - 401 when bearer auth is missing
//
// Backwards-compat: the response key `requirements` matches the
// slice-006 in-memory shape, so the slice-007
// TestRequirementsForAnchor_StillReturnsMappings test still asserts a
// non-empty list against this route. Individual row fields are
// supersets of the in-memory shape.
func (h *Handler) AnchorRequirements(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	anchor, rel, err := scfrelease.Resolve(ctx, h.q, r.URL.Query().Get("scf_release"), chi.URLParam(r, "id"))
	switch {
	case errors.Is(err, scfrelease.ErrUnknownRelease), errors.Is(err, scfrelease.ErrCodeNotInRelease):
		httpresp.WriteError(w, http.StatusNotFound, err.Error())
		return
	case errors.Is(err, scfrelease.ErrNoCurrentRelease):
		httpresp.WriteError(w, http.StatusNotFound, "anchor not found")
		return
	case err != nil:
		httperr.WriteInternal(w, r, "lookup anchor", err)
		return
	}

	var out []requirementForAnchorWire
	if fvParam := r.URL.Query().Get("framework_version"); fvParam != "" {
		fv, ok := h.resolveFrameworkVersion(ctx, fvParam)
		if !ok {
			// Pin resolves to nothing: empty list, not 404 — the anchor
			// exists; only the pin found no matches.
			httpresp.WriteJSON(w, http.StatusOK, map[string]any{
				"anchor":       anchorWireFromRow(anchor),
				"requirements": []requirementForAnchorWire{},
				"scf_release":  rel.Version,
			})

			return
		}
		got, err := h.listRequirementsForAnchor(ctx, anchor.ID, &fv.ID)
		if err != nil {
			httperr.WriteInternal(w, r, "list requirements for anchor (pinned)", err)
			return
		}
		out = got
	} else {
		got, err := h.listRequirementsForAnchor(ctx, anchor.ID, nil)
		if err != nil {
			httperr.WriteInternal(w, r, "list requirements for anchor (current)", err)
			return
		}
		out = got
	}

	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"anchor":       anchorWireFromRow(anchor),
		"requirements": out,
		"scf_release":  rel.Version,
	})

}

// anchorWireFromRow maps a bare scf_anchors row to the anchorWire JSON
// shape (no STRM edge metadata — that's only present in the
// RequirementCoverage anchors list).
func anchorWireFromRow(a dbx.ScfAnchor) anchorWire {
	return anchorWire{
		ID:          uuidStr(a.ID),
		SCFID:       a.ScfID,
		Family:      a.Family,
		Name:        a.Title,
		Description: a.Description,
	}
}

// listRequirementsForAnchor is the reverse traversal both
// /v1/anchors/{id}/requirements and /v1/controls/{id}/coverage read: every
// requirement the anchor satisfies, in one framework version when
// frameworkVersionID is set, else in each framework's CURRENT version
// (slice 484, AC-5 / ADR 0019 §4: absent a pin, never bleed a
// legacy/superseded version's requirements, P0-484-5).
//
// It reads inside the request's tenant GUC: effective_fw_to_scf_edges
// carries the caller tenant's active overlay edges (#1798), which RLS
// hides without it.
func (h *Handler) listRequirementsForAnchor(ctx context.Context, anchorID pgtype.UUID, frameworkVersionID *pgtype.UUID) ([]requirementForAnchorWire, error) {
	var out []requirementForAnchorWire
	err := h.inTenantTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		if frameworkVersionID != nil {
			got, err := q.ListRequirementsForAnchorByFrameworkVersion(ctx, dbx.ListRequirementsForAnchorByFrameworkVersionParams{
				ScfAnchorID:        anchorID,
				FrameworkVersionID: *frameworkVersionID,
			})
			out = mapPinnedRequirements(got)
			return err
		}
		got, err := q.ListRequirementsForAnchorCurrentVersions(ctx, anchorID)
		out = mapCurrentVersionRequirements(got)
		return err
	})
	return out, err
}

// mapCurrentVersionRequirements maps the slice-484 default (current-version-only)
// reverse-traversal rows. The row shape mirrors ListRequirementsForAnchorRow
// field-for-field; sqlc emits a distinct named type per query, so this is a thin
// structural copy rather than a shared mapper.
func mapCurrentVersionRequirements(rows []dbx.ListRequirementsForAnchorCurrentVersionsRow) []requirementForAnchorWire {
	out := make([]requirementForAnchorWire, 0, len(rows))
	for _, x := range rows {
		out = append(out, requirementForAnchorWire{
			EdgeID:                 uuidStr(x.EdgeID),
			RequirementID:          uuidStr(x.FrameworkRequirementID),
			Code:                   x.Code,
			Title:                  x.RequirementTitle,
			Body:                   x.RequirementBody,
			FrameworkSlug:          x.FrameworkSlug,
			FrameworkName:          x.FrameworkName,
			FrameworkVersion:       x.FrameworkVersion,
			FrameworkVersionID:     uuidStr(x.FrameworkVersionID),
			FrameworkVersionStatus: string(x.FrameworkVersionStatus),
			RelationshipType:       string(x.RelationshipType),
			Strength:               x.Strength,
			SourceAttribution:      string(x.SourceAttribution),
			MappingTier:            string(x.MappingTier),
			Origin:                 x.Origin,
			Rationale:              x.Rationale,
		})
	}
	return out
}

func mapPinnedRequirements(rows []dbx.ListRequirementsForAnchorByFrameworkVersionRow) []requirementForAnchorWire {
	out := make([]requirementForAnchorWire, 0, len(rows))
	for _, x := range rows {
		out = append(out, requirementForAnchorWire{
			EdgeID:                 uuidStr(x.EdgeID),
			RequirementID:          uuidStr(x.FrameworkRequirementID),
			Code:                   x.Code,
			Title:                  x.RequirementTitle,
			Body:                   x.RequirementBody,
			FrameworkSlug:          x.FrameworkSlug,
			FrameworkName:          x.FrameworkName,
			FrameworkVersion:       x.FrameworkVersion,
			FrameworkVersionID:     uuidStr(x.FrameworkVersionID),
			FrameworkVersionStatus: string(x.FrameworkVersionStatus),
			RelationshipType:       string(x.RelationshipType),
			Strength:               x.Strength,
			SourceAttribution:      string(x.SourceAttribution),
			MappingTier:            string(x.MappingTier),
			Origin:                 x.Origin,
			Rationale:              x.Rationale,
		})
	}
	return out
}
