"use client";

// Slice 040 — framework posture tiles (AC-2) — REBOUND by slice 147.
//
// Binds to `GET /v1/frameworks/posture` via the dashboard BFF (slice
// 066 backend reads — see `docs/audit-log/066-...-decisions.md` D3 for
// the spine-walk aggregation). Each tile renders one active framework
// version: a coverage percentage, a freshness composite percentage, and
// a signed 90-day trend delta. #2095: no traffic-light colour without a
// target; the tile says "No target set". #2136: with a posture target the
// coverage colours by its thresholds (the shared `thresholdBadgeColor`
// rule) and says which band it is in; admin and grc_engineer get "Set
// target" / "Edit target" on the tile.
//
// Slice 040 originally shipped this as a `MissingEndpointPanel`
// (slice 041/060 precedent) — slice 066 then shipped the backend
// endpoint, and slice 147 closed the loop by re-pointing the panel.
// See `docs/audit-log/147-dashboard-placeholders-decisions.md` D1+D2.

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";

import { PanelCard, type PanelState } from "@/components/dashboard/panel-card";
import { PostureTargetDialog } from "@/components/dashboard/posture-target-dialog";
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
import { getMe } from "@/lib/api/me";
import {
  canSetPostureTarget,
  POSTURE_COLOR_LABEL,
  postureColorClass,
  postureTileColor,
} from "@/lib/api/posture-targets";

// #2095 / #2136: without a target a tile has nothing to grade against.
// Coverage renders in the neutral foreground and the 90-day delta is
// toned by `deltaTone` with no target, which is always neutral: a rising
// number is not "good" until a target says which way good is. With a
// target, coverage takes its threshold colour and the delta is toned by
// the target's direction. The arrow, the band label and the aria-label
// carry the meaning; colour is never the only cue.

// #2269: the rule behind covered_by_exception_pct (#2112 D11), stated where
// the number is shown. One excepted scope cell is enough for a control to
// count as resting on an exception.
const COVERED_BY_EXCEPTION_TOOLTIP =
  "Requirements whose every covering control rests on an exception. A control counts as resting on an exception when even one of its scope cells is excepted.";

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

function PostureTile({
  row,
  canSetTarget,
}: {
  row: FrameworkPostureRow;
  canSetTarget: boolean;
}) {
  if (
    !row.scope_set ||
    row.coverage_pct === null ||
    row.trend_delta_90d === null ||
    row.freshness_composite === null
  ) {
    return <ScopeNotSetTile row={row} />;
  }
  const target = row.target ?? null;
  const color = postureTileColor(row.coverage_pct, target);
  const trendTone = deltaTone(row.coverage_pct, row.trend_delta_90d, target);
  const label = frameworkLabel(row.framework_name, row.framework_version);
  return (
    <div
      data-testid="framework-tile"
      className="rounded-xl bg-muted/40 p-4 ring-1 ring-foreground/5"
    >
      <div className="text-[11px] font-medium tracking-wider text-muted-foreground uppercase">
        {label}
      </div>
      <div className="mt-2 flex items-baseline gap-2">
        <span
          className={`font-mono text-2xl font-semibold ${postureColorClass(
            color,
          )}`}
          data-testid="framework-tile-coverage"
          data-tone={color}
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
      {target ? (
        <div
          className="mt-1 text-xs text-muted-foreground"
          data-testid="framework-tile-target"
        >
          <span className={`font-medium ${postureColorClass(color)}`}>
            {POSTURE_COLOR_LABEL[color]}
          </span>{" "}
          · target {target.target_value}%
        </div>
      ) : (
        <div
          className="mt-1 text-xs text-muted-foreground"
          data-testid="framework-tile-no-target"
        >
          No target set
        </div>
      )}
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
          title={COVERED_BY_EXCEPTION_TOOLTIP}
        >
          {row.covered_by_exception_pct.toFixed(1)}% covered by exception
        </div>
      ) : null}
      {canSetTarget ? (
        <PostureTargetDialog
          frameworkVersionId={row.framework_version_id}
          frameworkLabel={label}
          current={target}
          prior={row.prior_target ?? null}
        />
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
  // Only admin and grc_engineer may set a target; the platform refuses
  // anyone else (403). Until /v1/me answers, the tile offers nothing.
  const meQuery = useQuery({ queryKey: ["dashboard", "me"], queryFn: getMe });
  const canSetTarget = canSetPostureTarget(meQuery.data);
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
              canSetTarget={canSetTarget}
            />
          ))}
        </div>
      )}
    </PanelCard>
  );
}
