// Slice 043 — control coverage trend visual (per board-pack.html §03).
//
// The slice-032 coverage_trend section carries scalar fields:
// coverage_pct, baseline_coverage_pct, coverage_delta. There is no
// per-framework time series on the server — fabricating one would
// violate the anti-pattern of made-up data. The visual is therefore a
// trend SUMMARY card showing the baseline, current, and delta as three
// readable rows. When a richer series ships, this component is the
// single place to upgrade.

import { cn } from "@/lib/utils";

type CoverageTrendProps = {
  baseline: number;
  current: number;
  delta: number;
};

export function CoverageTrend({
  baseline,
  current,
  delta,
}: CoverageTrendProps) {
  const deltaTone =
    delta > 0
      ? "text-emerald-700"
      : delta < 0
        ? "text-rose-700"
        : "text-slate-500";
  const deltaBg =
    delta > 0
      ? "bg-emerald-50 border-emerald-100"
      : delta < 0
        ? "bg-rose-50 border-rose-100"
        : "bg-slate-50 border-slate-200";

  return (
    <div className="space-y-4" data-testid="coverage-trend">
      <div className="grid grid-cols-3 gap-3 text-sm">
        <CoverageCard label="Baseline" value={`${baseline}%`} tone="muted" />
        <CoverageCard label="Current" value={`${current}%`} tone="primary" />
        <div
          className={cn("rounded-lg border p-3", deltaBg)}
          data-testid="coverage-delta-card"
        >
          <div className="mb-0.5 text-xs text-slate-600">Quarter delta</div>
          <div className={cn("font-semibold", deltaTone)}>
            {delta > 0 ? `+${delta}` : delta === 0 ? "flat" : delta}
            {delta !== 0 ? " pts" : ""}
          </div>
        </div>
      </div>
      <p className="text-xs text-slate-500">
        Baseline is the operator-entered prior-quarter coverage; delta is
        current minus baseline.
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
    <div className="rounded-lg border border-slate-200 p-3">
      <div className="mb-0.5 text-xs text-slate-600">{label}</div>
      <div
        className={cn(
          "text-lg font-semibold",
          tone === "primary" ? "text-slate-900" : "text-slate-600",
        )}
      >
        {value}
      </div>
    </div>
  );
}
