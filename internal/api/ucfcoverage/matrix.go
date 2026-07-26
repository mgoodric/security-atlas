package ucfcoverage

import (
	"context"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

const (
	defaultMatrixLimit = 50
	maxMatrixLimit     = 100
)

// ===== /v1/coverage-strength/matrix =====

// CoverageStrengthMatrix handles GET /v1/coverage-strength/matrix.
//
// Rows are SCF anchors, columns are current framework versions, and each
// cell is the strongest slice-482 per-anchor contribution for that anchor
// into that framework:
//
//	cell = MAX over mapped requirements in the framework of
//	       (edge_strength × anchor_coverage_for_that_framework)
//
// The computation deliberately reuses slice 482's rollup primitives:
// `controlCoverageForFramework` computes the RLS-scoped anchor coverage
// term, and `rollupCoverageStrength` applies the edge-strength multiply
// plus confidence-band classification. No new evidence/control
// evaluation formula is introduced here.
func (h *Handler) CoverageStrengthMatrix(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	limit := boundedPositiveInt(r.URL.Query().Get("limit"), defaultMatrixLimit, maxMatrixLimit)
	offset := nonNegativeInt(r.URL.Query().Get("offset"))
	family := r.URL.Query().Get("family")

	frameworks, err := h.listMatrixFrameworks(ctx)
	if err != nil {
		httperr.WriteInternal(w, r, "list matrix frameworks", err)
		return
	}
	edges, err := h.listMatrixEdges(ctx, matrixEdgeFilter{
		Family: family,
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		httperr.WriteInternal(w, r, "list matrix edges", err)
		return
	}

	rows, err := h.assembleMatrixRows(ctx, frameworks, edges)
	if err != nil {
		httperr.WriteInternal(w, r, "assemble matrix", err)
		return
	}

	httpresp.WriteJSON(w, http.StatusOK, matrixWire{
		Axis: matrixAxisWire{
			Rows:        "scf_anchor",
			Columns:     "framework_current_version",
			CellValue:   "max_requirement_contribution",
			Aggregation: "max(edge_strength * anchor_coverage)",
		},
		Bands:      coverageBandTokenMappings(),
		Frameworks: frameworks,
		Rows:       rows,
		Limit:      limit,
		Offset:     offset,
	})
}

type matrixWire struct {
	Axis       matrixAxisWire        `json:"axis"`
	Bands      []bandTokenWire       `json:"bands"`
	Frameworks []matrixFrameworkWire `json:"frameworks"`
	Rows       []matrixRowWire       `json:"rows"`
	Limit      int                   `json:"limit"`
	Offset     int                   `json:"offset"`
}

type matrixAxisWire struct {
	Rows        string `json:"rows"`
	Columns     string `json:"columns"`
	CellValue   string `json:"cell_value"`
	Aggregation string `json:"aggregation"`
}

type matrixFrameworkWire struct {
	FrameworkVersionID string `json:"framework_version_id"`
	FrameworkSlug      string `json:"framework_slug"`
	FrameworkName      string `json:"framework_name"`
	Version            string `json:"version"`
	Status             string `json:"status"`
}

type matrixRowWire struct {
	Anchor anchorSummaryWire `json:"anchor"`
	Cells  []matrixCellWire  `json:"cells"`
}

type anchorSummaryWire struct {
	ID          string `json:"id"`
	SCFID       string `json:"scf_id"`
	Family      string `json:"family"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type matrixCellWire struct {
	FrameworkVersionID string  `json:"framework_version_id"`
	CoverageStrength   float64 `json:"coverage_strength"`
	ConfidenceBand     string  `json:"confidence_band"`
	BandToken          string  `json:"band_token"`
	RequirementCount   int     `json:"requirement_count"`
	Contributing       bool    `json:"contributing"`
}

type bandTokenWire struct {
	Band  string `json:"band"`
	Token string `json:"token"`
	Label string `json:"label"`
}

type matrixEdgeFilter struct {
	Family string
	Limit  int32
	Offset int32
}

type matrixEdgeRow struct {
	AnchorID             pgtype.UUID
	SCFID                string
	Family               string
	AnchorTitle          string
	AnchorDescription    string
	FrameworkVersionID   pgtype.UUID
	FrameworkSlug        string
	FrameworkName        string
	FrameworkVersion     string
	FrameworkStatus      string
	FrameworkRequirement pgtype.UUID
	RequirementCode      string
	EdgeStrength         float64
}

func (h *Handler) assembleMatrixRows(
	ctx context.Context,
	frameworks []matrixFrameworkWire,
	edges []matrixEdgeRow,
) ([]matrixRowWire, error) {
	if len(edges) == 0 {
		return []matrixRowWire{}, nil
	}

	anchorOrder := make([]string, 0)
	anchors := make(map[string]anchorSummaryWire)
	anchorUUIDs := make([]pgtype.UUID, 0)
	edgesByAnchorFramework := make(map[string]map[string][]matrixEdgeRow)
	for _, e := range edges {
		aid := uuidStr(e.AnchorID)
		if _, seen := anchors[aid]; !seen {
			anchorOrder = append(anchorOrder, aid)
			anchorUUIDs = append(anchorUUIDs, e.AnchorID)
			anchors[aid] = anchorSummaryWire{
				ID:          aid,
				SCFID:       e.SCFID,
				Family:      e.Family,
				Name:        e.AnchorTitle,
				Description: e.AnchorDescription,
			}
		}
		fvID := uuidStr(e.FrameworkVersionID)
		if edgesByAnchorFramework[aid] == nil {
			edgesByAnchorFramework[aid] = make(map[string][]matrixEdgeRow)
		}
		edgesByAnchorFramework[aid][fvID] = append(edgesByAnchorFramework[aid][fvID], e)
	}

	controls, err := h.listControlsForAnchors(ctx, anchorUUIDs)
	if err != nil {
		return nil, err
	}

	controlsByAnchor := make(map[string][]dbx.ListControlsForAnchorsRow)
	for _, c := range controls {
		aid := uuidStr(c.ScfAnchorID)
		controlsByAnchor[aid] = append(controlsByAnchor[aid], c)
	}

	rows := make([]matrixRowWire, 0, len(anchorOrder))
	for _, aid := range anchorOrder {
		row := matrixRowWire{
			Anchor: anchors[aid],
			Cells:  make([]matrixCellWire, 0, len(frameworks)),
		}
		for _, fw := range frameworks {
			cellEdges := edgesByAnchorFramework[aid][fw.FrameworkVersionID]
			cell := matrixCellWire{
				FrameworkVersionID: fw.FrameworkVersionID,
				RequirementCount:   len(cellEdges),
			}
			if len(cellEdges) > 0 {
				score, band := h.matrixCellRollup(ctx, controlsByAnchor[aid], fw.FrameworkVersionID, cellEdges)
				cell.CoverageStrength = score
				cell.ConfidenceBand = string(band)
				cell.BandToken = bandToken(band)
				cell.Contributing = band != BandUncovered
			} else {
				cell.ConfidenceBand = string(BandUncovered)
				cell.BandToken = bandToken(BandUncovered)
			}
			row.Cells = append(row.Cells, cell)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (h *Handler) matrixCellRollup(
	ctx context.Context,
	controls []dbx.ListControlsForAnchorsRow,
	frameworkVersionID string,
	edges []matrixEdgeRow,
) (float64, ConfidenceBand) {
	if h.engine == nil || h.scopeStore == nil || h.fwScopeStore == nil || len(controls) == 0 {
		return 0, BandUncovered
	}

	bestAnchorCover := 0.0
	hasAnchorCover := false
	for _, c := range controls {
		cover, ok, err := h.controlCoverageForFramework(ctx, c, frameworkVersionID)
		if err != nil || !ok {
			continue
		}
		if !hasAnchorCover || cover > bestAnchorCover {
			bestAnchorCover = cover
			hasAnchorCover = true
		}
	}

	strengths := make([]float64, 0, len(edges))
	for _, edge := range edges {
		strengths = append(strengths, edge.EdgeStrength)
	}
	return matrixCellScore(strengths, bestAnchorCover, hasAnchorCover)
}

func matrixCellScore(edgeStrengths []float64, anchorCover float64, hasAnchorCover bool) (float64, ConfidenceBand) {
	acs := make([]anchorCoverage, 0, len(edgeStrengths))
	for _, strength := range edgeStrengths {
		acs = append(acs, anchorCoverage{
			edgeStrength: strength,
			anchorCover:  anchorCover,
			hasCoverage:  hasAnchorCover,
		})
	}
	score, any := rollupCoverageStrength(acs)
	return score, classifyBand(score, any)
}

func (h *Handler) listMatrixFrameworks(ctx context.Context) ([]matrixFrameworkWire, error) {
	rows, err := h.pool.Query(ctx, `
		SELECT fv.id, f.slug, f.name, fv.version, fv.status
		  FROM framework_versions fv
		  JOIN frameworks f ON f.id = fv.framework_id
		 WHERE f.tenant_id IS NULL
		   AND fv.status = 'current'
		   AND f.slug <> 'scf'
		   AND EXISTS (
		         SELECT 1
		           FROM framework_requirements r
		           JOIN fw_to_scf_edges e ON e.framework_requirement_id = r.id
		          WHERE r.framework_version_id = fv.id
		            AND e.relationship_type <> 'no_relationship'
		       )
		 ORDER BY f.slug, fv.version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]matrixFrameworkWire, 0)
	for rows.Next() {
		var (
			id      pgtype.UUID
			slug    string
			name    string
			version string
			status  string
		)
		if err := rows.Scan(&id, &slug, &name, &version, &status); err != nil {
			return nil, err
		}
		out = append(out, matrixFrameworkWire{
			FrameworkVersionID: uuidStr(id),
			FrameworkSlug:      slug,
			FrameworkName:      name,
			Version:            version,
			Status:             status,
		})
	}
	return out, rows.Err()
}

func (h *Handler) listMatrixEdges(ctx context.Context, filter matrixEdgeFilter) ([]matrixEdgeRow, error) {
	rows, err := h.pool.Query(ctx, `
		WITH selected_anchors AS (
			SELECT DISTINCT a.id, a.scf_id, a.family
			  FROM scf_anchors a
			  JOIN fw_to_scf_edges e ON e.scf_anchor_id = a.id
			  JOIN framework_requirements r ON r.id = e.framework_requirement_id
			  JOIN framework_versions fv ON fv.id = r.framework_version_id
			  JOIN frameworks f ON f.id = fv.framework_id
			 WHERE e.relationship_type <> 'no_relationship'
			   AND fv.status = 'current'
			   AND f.tenant_id IS NULL
			   AND f.slug <> 'scf'
			   AND ($1::text = '' OR a.family = $1::text)
			 ORDER BY a.family, a.scf_id
			 LIMIT $2 OFFSET $3
		)
		SELECT
			a.id,
			a.scf_id,
			a.family,
			a.title,
			a.description,
			fv.id,
			f.slug,
			f.name,
			fv.version,
			fv.status,
			r.id,
			r.code,
			e.strength
		  FROM selected_anchors sa
		  JOIN scf_anchors a ON a.id = sa.id
		  JOIN fw_to_scf_edges e ON e.scf_anchor_id = a.id
		  JOIN framework_requirements r ON r.id = e.framework_requirement_id
		  JOIN framework_versions fv ON fv.id = r.framework_version_id
		  JOIN frameworks f ON f.id = fv.framework_id
		 WHERE e.relationship_type <> 'no_relationship'
		   AND fv.status = 'current'
		   AND f.tenant_id IS NULL
		   AND f.slug <> 'scf'
		 ORDER BY a.family, a.scf_id, f.slug, fv.version, r.code`,
		filter.Family, filter.Limit, filter.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]matrixEdgeRow, 0)
	for rows.Next() {
		var r matrixEdgeRow
		if err := rows.Scan(
			&r.AnchorID,
			&r.SCFID,
			&r.Family,
			&r.AnchorTitle,
			&r.AnchorDescription,
			&r.FrameworkVersionID,
			&r.FrameworkSlug,
			&r.FrameworkName,
			&r.FrameworkVersion,
			&r.FrameworkStatus,
			&r.FrameworkRequirement,
			&r.RequirementCode,
			&r.EdgeStrength,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func coverageBandTokenMappings() []bandTokenWire {
	return []bandTokenWire{
		{Band: string(BandStrong), Token: bandToken(BandStrong), Label: "Strong coverage"},
		{Band: string(BandPartial), Token: bandToken(BandPartial), Label: "Partial coverage"},
		{Band: string(BandWeak), Token: bandToken(BandWeak), Label: "Weak coverage"},
		{Band: string(BandUncovered), Token: bandToken(BandUncovered), Label: "Uncovered"},
	}
}

func bandToken(b ConfidenceBand) string {
	switch b {
	case BandStrong:
		return "pass"
	case BandPartial:
		return "warning"
	case BandWeak:
		return "critical"
	default:
		return "info"
	}
}

func boundedPositiveInt(raw string, def int, max int) int {
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

func nonNegativeInt(raw string) int {
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
