"use client";

// Slice 040 — framework posture tiles (AC-2) — REBOUND by slice 147.
//
// Binds to `GET /v1/frameworks/posture` via the dashboard BFF (slice
// 066 backend reads — see `docs/audit-log/066-...-decisions.md` D3 for
// the spine-walk aggregation). Each tile renders one active framework
// version: a coverage percentage, a freshness composite percentage, and
// a signed 90-day trend delta. #2095: no traffic-light colour without a
// target; the tile says "No target set".
//
// Slice 040 originally shipped this as a `MissingEndpointPanel`
// (slice 041/060 precedent) — slice 066 then shipped the backend
// endpoint, and slice 147 closed the loop by re-pointing the panel.
// See `docs/audit-log/147-dashboard-placeholders-decisions.md` D1+D2.

import Link from "next/link";

import { PanelCard, type PanelState } from "@/components/dashboard/panel-card";
import { Term } from "@/components/help/term";
import {
  deltaAriaLabel,
  deltaArrow,
  deltaTone,
  deltaToneClass,
} from "@/lib/charts/delta-tone";
import { frameworkLabel } from "@/lib/labels/framework-label";
import type {
  FrameworkPostureReport,
  FrameworkPostureRow,
} from "@/lib/api/dashboard";

// #2095: posture has no target yet (framework posture targets are
// #2341), so a tile has nothing to grade against. Coverage renders in
// the neutral foreground and the 90-day delta is toned by `deltaTone`
// with no target, which is always neutral: a rising number is not "good"
// until there is a target that says which way good is. The arrow and the
// aria-label carry the direction, not the colour.
const POSTURE_TARGET = null;

function trendLabel(delta: number): string {
  if (delta > 0) return `+${delta.toFixed(1)}`;
  if (delta < 0) return delta.toFixed(1);
  return "0.0";
}

// ScopeNotSetTile (#2114): the version has no activated framework scope,
// so nothing has been decided out of scope and a percentage would mean
// nothing. The tile says so and links to the framework-scope page.
function ScopeNotSetTile({ row }: { row: FrameworkPostureRow }) {
  return (
    <div
      data-testid="framework-tile"
      className="rounded-xl bg-muted/40 p-4 ring-1 ring-foreground/5"
    >
      <div className="text-[11px] font-medium tracking-wider text-muted-foreground uppercase">
        {frameworkLabel(row.framework_name, row.framework_version)}
      </div>
      <Link
        href={`/framework-scopes/${encodeURIComponent(
          row.framework_version_id,
        )}`}
        className="mt-2 inline-block text-sm font-semibold underline underline-offset-2"
        data-testid="framework-tile-scope-not-set"
      >
        Scope not set
      </Link>
      <div className="mt-1 text-xs text-muted-foreground">
        Activate a <Term id="framework-scope">framework scope</Term> to see
        coverage.
      </div>
    </div>
  );
}

function PostureTile({ row }: { row: FrameworkPostureRow }) {
  if (
    !row.scope_set ||
    row.coverage_pct === null ||
    row.trend_delta_90d === null ||
    row.freshness_composite === null
  ) {
    return <ScopeNotSetTile row={row} />;
  }
  const trendTone = deltaTone(
    row.coverage_pct,
    row.trend_delta_90d,
    POSTURE_TARGET,
  );
  return (
    <div
      data-testid="framework-tile"
      className="rounded-xl bg-muted/40 p-4 ring-1 ring-foreground/5"
    >
      <div className="text-[11px] font-medium tracking-wider text-muted-foreground uppercase">
        {frameworkLabel(row.framework_name, row.framework_version)}
      </div>
      <div className="mt-2 flex items-baseline gap-2">
        <span
          className="font-mono text-2xl font-semibold text-foreground"
          data-testid="framework-tile-coverage"
          data-tone="neutral"
        >
          {row.coverage_pct.toFixed(1)}%
        </span>
        <span
          className={`font-mono text-xs ${deltaToneClass(trendTone)}`}
          data-testid="framework-tile-trend"
          data-tone={trendTone}
          title="90-day coverage delta"
          aria-label={`90-day coverage change ${deltaAriaLabel(
            trendTone,
            trendLabel(row.trend_delta_90d),
          )}`}
        >
          {deltaArrow(row.trend_delta_90d)} {trendLabel(row.trend_delta_90d)}
        </span>
      </div>
      <div
        className="mt-1 text-xs text-muted-foreground"
        data-testid="framework-tile-no-target"
      >
        No target set
      </div>
      <div
        className="mt-1 text-xs text-muted-foreground"
        data-testid="framework-tile-freshness"
      >
        <Term id="evidence-freshness">freshness</Term>{" "}
        {row.freshness_composite.toFixed(1)}%
      </div>
      {row.covered_by_exception_pct !== null &&
      row.covered_by_exception_pct > 0 ? (
        <div
          className="mt-1 text-xs text-muted-foreground"
          data-testid="framework-tile-exception"
        >
          {row.covered_by_exception_pct.toFixed(1)}% covered by exception
        </div>
      ) : null}
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
      title={
        <>
          Framework <Term id="posture">posture</Term>
        </>
      }
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
