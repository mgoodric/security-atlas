// Slice 043 — control coverage trend visual (per board-pack.html §03).
//
// The slice-032 coverage_trend section carries scalar fields:
// coverage_pct, baseline_coverage_pct, coverage_delta. Since #2056 they
// are the mean of the scoped frameworks' dashboard posture, with the
// baseline taken from the start of the 90-day posture trend. There is no
// per-framework time series on the server — fabricating one would
// violate the anti-pattern of made-up data. The visual is therefore a
// trend SUMMARY card showing the baseline, current, and delta as three
// readable rows. When a richer series ships, this component is the
// single place to upgrade.

import { cn } from "@/lib/utils";

import { NO_BASELINE_TEXT } from "./posture-coverage-caption";

type CoverageTrendProps = {
  baseline: number;
  current: number;
  delta: number;
  // No baseline to compare against (#2056 review B1): show n/a, not 0%.
  noBaseline?: boolean;
};

export function CoverageTrend({
  baseline,
  current,
  delta,
  noBaseline = false,
}: CoverageTrendProps) {
  const fmt = (n: number) => Number(n.toFixed(1)).toString();
  const deltaTone =
    delta > 0
      ? "text-pass"
      : delta < 0
        ? "text-critical"
        : "text-muted-foreground";
  const deltaBorder =
    delta > 0
      ? "border-pass/40"
      : delta < 0
        ? "border-critical/40"
        : "border-border";

  return (
    <div className="space-y-4" data-testid="coverage-trend">
      <div className="grid grid-cols-3 gap-3 text-sm">
        <CoverageCard
          label="Baseline"
          value={noBaseline ? "n/a" : `${fmt(baseline)}%`}
          tone="muted"
        />
        <CoverageCard
          label="Current"
          value={`${fmt(current)}%`}
          tone="primary"
        />
        <div
          className={cn("rounded-lg border p-3", deltaBorder)}
          data-testid="coverage-delta-card"
        >
          <div className="mb-0.5 text-xs text-muted-foreground">
            Quarter delta
          </div>
          <div className={cn("font-semibold", deltaTone)}>
            {noBaseline
              ? "n/a"
              : delta > 0
                ? `+${fmt(delta)}`
                : delta === 0
                  ? "flat"
                  : fmt(delta)}
            {!noBaseline && delta !== 0 ? " pts" : ""}
          </div>
        </div>
      </div>
      <p className="text-xs text-muted-foreground">
        Mean coverage of the frameworks with an activated scope. Baseline is
        their coverage at the start of the 90-day posture trend, unless the
        operator entered a prior-quarter figure; delta is current minus
        baseline.
        {noBaseline ? ` There is ${NO_BASELINE_TEXT}.` : ""}
      </p>
    </div>
  );
}

function CoverageCard({
  label,
  value,
  tone,
}: {
  label: string;
  value: string;
  tone: "primary" | "muted";
}) {
  return (
    <div className="rounded-lg border border-border p-3">
      <div className="mb-0.5 text-xs text-muted-foreground">{label}</div>
      <div
        className={cn(
          "text-lg font-semibold",
          tone === "primary" ? "text-foreground" : "text-muted-foreground",
        )}
      >
        {value}
      </div>
    </div>
  );
}
