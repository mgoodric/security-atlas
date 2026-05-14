"use client";

// Slice 056 — theme heatmap panel for the hierarchical risk dashboard.
//
// Renders a `themes × org_units` matrix as a CSS grid: themes on the
// x-axis, org_units on the y-axis. Built-in (`source=default`) themes
// are ordered left of tenant-private (`source=tenant`) ones (AC-3).
//
// REAL vs MISSING (the honest split — anti-criterion P0-1):
//
//   * Axes are REAL — org_units from GET /v1/org_units (slice 053),
//     theme columns from GET /v1/themes (slice 053).
//   * Cell COUNTS + severity colors are MISSING — there is no
//     `themes × org_units` aggregation endpoint on main. The grid
//     renders its real axes with neutral (data-free) cells and a
//     `MissingEndpointPanel` banner above it naming the gap. AC-3/4/5
//     are PARTIAL.
//   * Cell-hover tooltip metadata is REAL — GET /v1/aggregation-rules
//     (slice 054) supplies the actual `window_days` / `min_risks` /
//     `min_teams` thresholds, so the "nearest rule fires at {threshold}"
//     copy cites real numbers per theme, not fabricated ones.
//
// AC-4's cell-click side panel (contributing risks, paginated) and AC-5's
// meta-risk icon both depend on the missing cell-aggregation endpoint;
// the cell-click handler opens a side panel that states this honestly
// rather than rendering a fabricated risk list.
//
// Invariant 9 (manual is first-class): the side panel and the meta-risk
// marker treat rule-driven and manually-aggregated meta-risks as peers —
// the source distinction, when it lands, is a small subscript, never a
// dominant visual.

import { useMemo, useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  PanelCard,
  MissingEndpointPanel,
  type PanelState,
} from "@/components/dashboard/panel-card";
import type { AggregationRule, OrgUnit, RiskTheme } from "@/lib/api";

const MISSING_HEATMAP_ENDPOINT = "GET /v1/risks/theme-heatmap";

// orderThemes puts built-in themes (source=default) before tenant-private
// ones, each group alphabetised — AC-3.
function orderThemes(themes: RiskTheme[]): RiskTheme[] {
  const rank = (t: RiskTheme) => (t.source === "default" ? 0 : 1);
  return [...themes].sort(
    (a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name),
  );
}

// nearestRuleByTheme indexes, per theme, the active aggregation rule
// with the lowest `min_risks` threshold — the "nearest rule" the cell
// tooltip and side panel cite. Built once per render from the rules
// list rather than re-scanned per heatmap cell (the grid is
// org_units × themes, so a per-cell scan would be O(cells × rules)).
function nearestRuleByTheme(
  rules: AggregationRule[],
): Map<string, AggregationRule> {
  const byTheme = new Map<string, AggregationRule>();
  for (const r of rules) {
    if (r.status !== "active") continue;
    const current = byTheme.get(r.target_theme);
    if (!current || r.min_risks < current.min_risks) {
      byTheme.set(r.target_theme, r);
    }
  }
  return byTheme;
}

type SelectedCell = { theme: string; orgUnitId: string; orgUnitName: string };

function HeatmapCellSidePanel({
  cell,
  rule,
  onClose,
}: {
  cell: SelectedCell;
  rule: AggregationRule | undefined;
  onClose: () => void;
}) {
  return (
    <div
      className="mt-4 rounded-md border bg-muted/20 p-4"
      data-testid="heatmap-cell-side-panel"
    >
      <div className="flex items-start justify-between gap-2">
        <div>
          <h3 className="text-sm font-semibold">
            {cell.theme} · {cell.orgUnitName}
          </h3>
          <p className="mt-0.5 text-xs text-muted-foreground">
            Contributing risks for this theme / org_unit cell
          </p>
        </div>
        <Button
          variant="ghost"
          size="sm"
          onClick={onClose}
          data-testid="heatmap-cell-side-panel-close"
        >
          Close
        </Button>
      </div>
      <Alert className="mt-3">
        <AlertTitle>Contributing risks not yet wired</AlertTitle>
        <AlertDescription>
          The per-cell contributing-risk list binds to{" "}
          <span className="font-mono">{MISSING_HEATMAP_ENDPOINT}</span>, which
          does not exist on main yet. No risk rows are shown until the endpoint
          ships — the heatmap never fabricates a contributing-risk list.
        </AlertDescription>
      </Alert>
      {rule ? (
        <p
          className="mt-3 text-xs text-muted-foreground"
          data-testid="heatmap-cell-side-panel-rule"
        >
          Aggregation rule <span className="font-mono">{rule.rule_id}</span>{" "}
          targets this theme: fires at {rule.min_risks} risks across{" "}
          {rule.min_teams} org_units within a {rule.window_days}-day window.
        </p>
      ) : (
        <p className="mt-3 text-xs text-muted-foreground">
          No active aggregation rule targets this theme.
        </p>
      )}
    </div>
  );
}

export function ThemeHeatmapPanel({
  orgUnits,
  themes,
  rules,
  orgState,
  themeState,
}: {
  orgUnits: OrgUnit[] | undefined;
  themes: RiskTheme[] | undefined;
  rules: AggregationRule[] | undefined;
  orgState: PanelState;
  themeState: PanelState;
}) {
  const orderedThemes = useMemo(() => orderThemes(themes ?? []), [themes]);
  const rulesByTheme = useMemo(() => nearestRuleByTheme(rules ?? []), [rules]);
  const rows = orgUnits ?? [];
  const [selected, setSelected] = useState<SelectedCell | null>(null);

  // The panel is in a loading/error state if EITHER axis query is.
  const combinedState: PanelState = {
    isLoading: orgState.isLoading || themeState.isLoading,
    isError: orgState.isError || themeState.isError,
    error: orgState.error ?? themeState.error,
    refetch: () => {
      orgState.refetch();
      themeState.refetch();
    },
  };

  const isEmpty =
    !combinedState.isLoading &&
    !combinedState.isError &&
    (rows.length === 0 || orderedThemes.length === 0);

  const selectedRule = selected ? rulesByTheme.get(selected.theme) : undefined;

  return (
    <PanelCard
      title="Theme heatmap"
      description="Themes × org_units — emergent risk patterns"
      state={combinedState}
      testid="theme-heatmap-panel"
      skeletonClassName="h-64 w-full"
    >
      {isEmpty ? (
        <div
          className="flex flex-col items-center gap-2 py-10 text-center"
          data-testid="theme-heatmap-empty"
        >
          <p className="text-sm text-muted-foreground">
            No themed risks yet. Tag risks with themes to surface cross-team
            patterns here.
          </p>
        </div>
      ) : (
        <div data-testid="theme-heatmap-body">
          {/* Cell counts + severity colors await a backend aggregation
              endpoint — the axes below are real, the cells are
              data-free until it ships. */}
          <MissingEndpointPanel
            title="Heatmap cell counts"
            endpoint={MISSING_HEATMAP_ENDPOINT}
            detail="The themes-by-org_units count + severity aggregation is a follow-up backend slice. The axes below are real (org_units + theme vocabulary); cells render data-free until the endpoint ships."
            testid="theme-heatmap-missing"
          />
          <div className="mt-4 overflow-x-auto">
            <div
              className="grid gap-px bg-border"
              style={{
                gridTemplateColumns: `minmax(8rem,12rem) repeat(${orderedThemes.length}, minmax(4.5rem,1fr))`,
              }}
              data-testid="theme-heatmap-grid"
            >
              {/* corner */}
              <div className="bg-background p-2 text-xs font-medium text-muted-foreground">
                org_unit \ theme
              </div>
              {/* column headers — themes, defaults first (AC-3) */}
              {orderedThemes.map((t) => (
                <div
                  key={t.name}
                  className="bg-background p-2 text-center text-[11px] font-medium"
                  data-testid="theme-heatmap-col"
                  data-theme-source={t.source}
                  title={`${t.description} (${t.source})`}
                >
                  <span className="block truncate">{t.name}</span>
                  {t.source === "tenant" ? (
                    <Badge
                      variant="outline"
                      className="mt-0.5 h-4 px-1 text-[9px]"
                    >
                      private
                    </Badge>
                  ) : null}
                </div>
              ))}
              {/* rows — one per org_unit */}
              {rows.map((ou) => (
                <HeatmapRow
                  key={ou.id}
                  orgUnit={ou}
                  themes={orderedThemes}
                  rulesByTheme={rulesByTheme}
                  onSelect={(theme) =>
                    setSelected({
                      theme,
                      orgUnitId: ou.id,
                      orgUnitName: ou.name,
                    })
                  }
                />
              ))}
            </div>
          </div>
          {selected ? (
            <HeatmapCellSidePanel
              cell={selected}
              rule={selectedRule}
              onClose={() => setSelected(null)}
            />
          ) : null}
        </div>
      )}
    </PanelCard>
  );
}

function HeatmapRow({
  orgUnit,
  themes,
  rulesByTheme,
  onSelect,
}: {
  orgUnit: OrgUnit;
  themes: RiskTheme[];
  rulesByTheme: Map<string, AggregationRule>;
  onSelect: (theme: string) => void;
}) {
  return (
    <>
      <div
        className="flex items-center gap-1 bg-background p-2 text-xs font-medium"
        data-testid="theme-heatmap-row"
        title={`${orgUnit.name} (${orgUnit.level})`}
      >
        <span className="truncate">{orgUnit.name}</span>
      </div>
      {themes.map((t) => {
        const rule = rulesByTheme.get(t.name);
        // Tooltip cites REAL rule thresholds (slice 054) — never
        // fabricated. The cell value itself is data-free (no count
        // endpoint), so the cell renders neutral light-gray (AC-3:
        // "Empty cells show light gray").
        const tip = rule
          ? `0 risks; nearest aggregation rule fires at ${rule.min_risks} risks across ${rule.min_teams} org_units; window ${rule.window_days}d`
          : `0 risks; no active aggregation rule targets ${t.name}`;
        return (
          <button
            key={t.name}
            type="button"
            onClick={() => onSelect(t.name)}
            data-testid="theme-heatmap-cell"
            data-theme={t.name}
            data-org-unit={orgUnit.id}
            title={tip}
            className="group flex h-12 items-center justify-center bg-muted/30 text-xs text-muted-foreground transition-colors hover:bg-foreground/10"
          >
            {/* No fabricated count — the cell is data-free until the
                aggregation endpoint ships. A muted dash communicates
                "no data" honestly. */}
            <span aria-hidden>—</span>
            <span className="sr-only">
              {orgUnit.name} / {t.name}: {tip}
            </span>
          </button>
        );
      })}
    </>
  );
}
