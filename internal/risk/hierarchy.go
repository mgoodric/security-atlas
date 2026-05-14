// Slice 067: risk-hierarchy read-model store methods. These back the
// backend read endpoints that slice 056 (hierarchical risk dashboard,
// merged gh#107) shipped binding empty-state placeholders for:
//
//   - RiskCountsByOrgUnit  — per-org-unit risk count by severity scalar
//     (GET /v1/org_units?include_risk_counts=true)
//   - ThemeOrgUnitHeatmap  — the themes × org_units aggregation grid
//     (GET /v1/risks/theme-heatmap)
//
// Both are pure reads over existing tenant-scoped tables (risks,
// org_themes) — no migration, no write surface. Each opens a transaction
// via inTx so the tenant GUC is applied and RLS fires on every underlying
// SELECT (constitutional invariant 6). The SQL does the GROUP BY in a
// single query (anti-criterion: no N+1) and resolves the severity scalar
// the same way the slice-019 heatmap does — likelihood × impact on the
// 5×5 grid, or the explicit `severity` field for aggregated parent risks.
package risk

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// OrgUnitRiskCounts is the per-org-unit severity rollup for AC-1. The
// Counts map is keyed by the severity scalar (the 5×5 grid product,
// 0..25); the value is the number of risks attributed to OrgUnitID at that
// severity. The map is sparse — a severity with no risks has no key. A
// risk whose inherent_score carries no numeric severity component lands
// under key 0 (counted, never hidden — constitutional invariant 9).
type OrgUnitRiskCounts struct {
	OrgUnitID uuid.UUID
	Counts    map[int]int
}

// RiskCountsByOrgUnit returns, for every org_unit in the active tenant
// that has at least one risk attributed to it, the count of those risks
// broken down by severity scalar. AC-1.
//
// It is ONE aggregation query (dbx.RiskCountsByOrgUnit) — the per-node
// rollup is the GROUP BY, not a query-per-node loop (anti-criterion: no
// N+1). Org_units with zero attributed risks simply do not appear in the
// returned slice; the API layer joins this to the full org-unit tree and
// renders an empty count map for the absent ones.
func (s *Store) RiskCountsByOrgUnit(ctx context.Context) ([]OrgUnitRiskCounts, error) {
	var out []OrgUnitRiskCounts
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.RiskCountsByOrgUnit(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("risk counts by org_unit: %w", err)
		}
		// Rows arrive ordered by (org_unit_id, severity), so consecutive
		// rows for the same org_unit are contiguous — fold them linearly
		// into one OrgUnitRiskCounts per org_unit, preserving first-seen
		// org_unit order.
		out = make([]OrgUnitRiskCounts, 0)
		for _, r := range rows {
			ou := uuid.UUID(r.OrgUnitID.Bytes)
			if len(out) == 0 || out[len(out)-1].OrgUnitID != ou {
				out = append(out, OrgUnitRiskCounts{OrgUnitID: ou, Counts: make(map[int]int)})
			}
			out[len(out)-1].Counts[int(r.Severity)] += int(r.RiskCount)
		}
		return nil
	})
	return out, err
}

// ThemeHeatmapCell is one populated cell of the themes × org_units aggregation.
// ThemeBuiltin is true when Theme is one of the canvas §6.5 built-in
// themes (org_themes.tenant_id IS NULL), false when it is a tenant-private
// theme — the API uses it to order built-in themes before tenant-private
// ones (slice 056 AC-3's rendering contract). AggregateSeverity is the
// MAX severity scalar across the cell's contributing risks (the canvas
// §6.6 default `max` severity function).
type ThemeHeatmapCell struct {
	Theme             string
	ThemeBuiltin      bool
	OrgUnitID         uuid.UUID
	RiskCount         int
	AggregateSeverity int
}

// ThemeOrgUnitHeatmap returns the populated cells of the themes ×
// org_units aggregation grid for the active tenant. AC-3.
//
// It is ONE aggregation query (dbx.RiskThemeOrgUnitGrid) — the whole grid
// in a single GROUP BY, never one query per cell (anti-criterion: no
// N+1). Cells with zero contributing risks are not returned; the API
// renders the full grid and treats an absent (theme, org_unit) pair as a
// zero cell. Rows arrive ordered built-in-themes-first, then theme name,
// then org_unit — the order the heatmap axis expects — and that order is
// preserved in the returned slice.
func (s *Store) ThemeOrgUnitHeatmap(ctx context.Context) ([]ThemeHeatmapCell, error) {
	var out []ThemeHeatmapCell
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.RiskThemeOrgUnitGrid(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("theme/org_unit heatmap: %w", err)
		}
		out = make([]ThemeHeatmapCell, len(rows))
		for i, r := range rows {
			out[i] = ThemeHeatmapCell{
				Theme:             r.Theme,
				ThemeBuiltin:      r.ThemeBuiltin,
				OrgUnitID:         uuid.UUID(r.OrgUnitID.Bytes),
				RiskCount:         int(r.RiskCount),
				AggregateSeverity: int(r.AggregateSeverity),
			}
		}
		return nil
	})
	return out, err
}
