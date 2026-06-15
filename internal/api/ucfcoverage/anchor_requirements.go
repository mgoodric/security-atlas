package ucfcoverage

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// ===== /v1/anchors/{id}/requirements =====

// AnchorRequirements handles GET /v1/anchors/{id}/requirements (AC-2 /
// reverse traversal). Replaces the slice-006 in-memory placeholder on
// the same path with DB-backed traversal.
//
// Path forms accepted:
//   - UUID — direct scf_anchors.id lookup
//   - scf_id (e.g., "IAC-06") — natural key lookup
//
// Query parameters:
//   - ?framework_version=slug:version — pin the response to one
//     framework_version (e.g. `soc2:2017`). When the param is absent,
//     every framework version is included.
//
// Returns:
//   - 200 { anchor, requirements[] }
//   - 404 when the anchor id doesn't resolve
//   - 401 when bearer auth is missing
//
// Backwards-compat: the response key `requirements` matches the
// slice-006 in-memory shape, so the slice-007
// TestRequirementsForAnchor_StillReturnsMappings test still asserts a
// non-empty list against this route. Individual row fields are
// supersets of the in-memory shape.
func (h *Handler) AnchorRequirements(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	anchor, ok, err := h.lookupAnchor(ctx, chi.URLParam(r, "id"))
	if err != nil {
		httperr.WriteInternal(w, r, "lookup anchor", err)
		return
	}
	if !ok {
		httpresp.WriteError(w, http.StatusNotFound, "anchor not found")
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
			})

			return
		}
		got, err := h.q.ListRequirementsForAnchorByFrameworkVersion(ctx, dbx.ListRequirementsForAnchorByFrameworkVersionParams{
			ScfAnchorID:        anchor.ID,
			FrameworkVersionID: fv.ID,
		})
		if err != nil {
			httperr.WriteInternal(w, r, "list requirements for anchor (pinned)", err)
			return
		}
		out = mapPinnedRequirements(got)
	} else {
		// slice 484 (AC-5 / ADR 0019 §4): absent a pin, default to each
		// framework's CURRENT version — never bleed a legacy/superseded
		// version's requirements (P0-484-5).
		got, err := h.q.ListRequirementsForAnchorCurrentVersions(ctx, anchor.ID)
		if err != nil {
			httperr.WriteInternal(w, r, "list requirements for anchor (current)", err)
			return
		}
		out = mapCurrentVersionRequirements(got)
	}

	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"anchor":       anchorWireFromRow(anchor),
		"requirements": out,
	})

}

// lookupAnchor resolves the {id} path segment to a scf_anchors row,
// supporting UUID and bare scf_id forms.
func (h *Handler) lookupAnchor(ctx context.Context, idOrSCFID string) (dbx.ScfAnchor, bool, error) {
	if uid, err := uuid.Parse(idOrSCFID); err == nil {
		row, err := h.q.GetSCFAnchorByID(ctx, pgtype.UUID{Bytes: uid, Valid: true})
		if errors.Is(err, pgx.ErrNoRows) {
			return dbx.ScfAnchor{}, false, nil
		}
		if err != nil {
			return dbx.ScfAnchor{}, false, err
		}
		return row, true, nil
	}
	row, err := h.q.GetSCFAnchorBySCFID(ctx, idOrSCFID)
	if errors.Is(err, pgx.ErrNoRows) {
		return dbx.ScfAnchor{}, false, nil
	}
	if err != nil {
		return dbx.ScfAnchor{}, false, err
	}
	return row, true, nil
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
			Rationale:              x.Rationale,
		})
	}
	return out
}
