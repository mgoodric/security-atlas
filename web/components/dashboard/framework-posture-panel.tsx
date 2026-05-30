"use client";

// Slice 040 — framework posture tiles (AC-2) — REBOUND by slice 147.
//
// Binds to `GET /v1/frameworks/posture` via the dashboard BFF (slice
// 066 backend reads — see `docs/audit-log/066-...-decisions.md` D3 for
// the spine-walk aggregation). Each tile renders one active framework
// version: a coverage percentage, a freshness composite percentage, and
// a signed 90-day trend delta.
//
// Slice 040 originally shipped this as a `MissingEndpointPanel`
// (slice 041/060 precedent) — slice 066 then shipped the backend
// endpoint, and slice 147 closed the loop by re-pointing the panel.
// See `docs/audit-log/147-dashboard-placeholders-decisions.md` D1+D2.

import { PanelCard, type PanelState } from "@/components/dashboard/panel-card";
import type {
  FrameworkPostureReport,
  FrameworkPostureRow,
} from "@/lib/api/dashboard";

// trendBadgeClass colours the trend arrow: green for growth, destructive
// for regression, muted for flat. The +/- prefix is rendered in the
// label so a screen reader speaks the sign.
function trendBadgeClass(delta: number): string {
  if (delta > 0) return "text-emerald-600 dark:text-emerald-400";
  if (delta < 0) return "text-destructive";
  return "text-muted-foreground";
}

function trendLabel(delta: number): string {
  if (delta > 0) return `+${delta.toFixed(1)}`;
  if (delta < 0) return delta.toFixed(1);
  return "0.0";
}

// coverageBadgeClass mirrors the slice-040 freshness bar palette:
// green at 90%+, amber at 70-89%, destructive below 70%.
function coverageBadgeClass(pct: number): string {
  if (pct >= 90) return "text-emerald-600 dark:text-emerald-400";
  if (pct >= 70) return "text-amber-600 dark:text-amber-400";
  return "text-destructive";
}

function PostureTile({ row }: { row: FrameworkPostureRow }) {
  return (
    <div
      data-testid="framework-tile"
      className="rounded-xl bg-muted/40 p-4 ring-1 ring-foreground/5"
    >
      <div className="text-[11px] font-medium tracking-wider text-muted-foreground uppercase">
        {row.framework_version}
      </div>
      <div className="mt-2 flex items-baseline gap-2">
        <span
          className={`font-mono text-2xl font-semibold ${coverageBadgeClass(
            row.coverage_pct,
          )}`}
          data-testid="framework-tile-coverage"
        >
          {row.coverage_pct.toFixed(1)}%
        </span>
        <span
          className={`font-mono text-xs ${trendBadgeClass(
            row.trend_delta_90d,
          )}`}
          data-testid="framework-tile-trend"
          title="90-day coverage delta"
        >
          {trendLabel(row.trend_delta_90d)}
        </span>
      </div>
      <div
        className="mt-1 text-xs text-muted-foreground"
        data-testid="framework-tile-freshness"
      >
        freshness {row.freshness_composite.toFixed(1)}%
      </div>
    </div>
  );
}

export function FrameworkPosturePanel({
  report,
  state,
}: {
  report: FrameworkPostureReport | undefined;
  state: PanelState;
}) {
  return (
    <PanelCard
      title="Framework posture"
      description="Coverage and 90-day trend per active framework version"
      state={state}
      skeletonClassName="h-32 w-full"
      testid="framework-posture-panel"
    >
      {!report || report.frameworks.length === 0 ? (
        <p
          className="py-6 text-sm text-muted-foreground"
          data-testid="framework-posture-empty"
        >
          No active framework versions yet. Import a framework catalog to
          populate posture tiles.
        </p>
      ) : (
        <div
          className="grid grid-cols-2 gap-3 md:grid-cols-3 lg:grid-cols-6"
          data-testid="framework-posture-list"
        >
          {report.frameworks.map((row) => (
            <PostureTile
              key={`${row.framework_id}:${row.framework_version}`}
              row={row}
            />
          ))}
        </div>
      )}
    </PanelCard>
  );
}
