// Slice 097 — inline-SVG line chart for the per-metric detail page.
//
// Renders the observation series with horizontal threshold overlays
// (target, warning, critical) when a target row is set. Theme-token
// colors via Tailwind classes. See decisions log D2 for rationale (no
// chart library). #2095: drawn inside the shared `AxisChart` wrapper, so
// it has a fitted, labelled value axis and a dated time axis.

import { AxisChart } from "@/components/charts/axis-chart";
import type { MetricTarget, Observation } from "@/lib/api/metrics";
import { fractionBounds } from "@/lib/charts/axes";

import { formatValue, parseValue } from "./format";

export type LineChartProps = {
  observations: Observation[];
  target: MetricTarget | null;
  unit: string;
  width?: number;
  height?: number;
  testid?: string;
};

export function LineChart({
  observations,
  target,
  unit,
  width = 480,
  height = 220,
  testid,
}: LineChartProps) {
  const series = observations
    .slice()
    .sort(
      (a, b) =>
        new Date(a.observed_at).getTime() - new Date(b.observed_at).getTime(),
    )
    .map((o) => ({
      t: new Date(o.observed_at).getTime(),
      v: parseValue(o.numeric_value),
    }))
    .filter((p): p is { t: number; v: number } => p.v !== undefined);

  if (series.length === 0) {
    return (
      <div
        data-testid={testid ? `${testid}-empty` : undefined}
        className="flex h-[220px] w-full items-center justify-center rounded-md border border-dashed text-sm text-muted-foreground"
      >
        No observations in the selected window.
      </div>
    );
  }

  const targetVal = parseValue(target?.target_value);
  const warnVal = parseValue(target?.warning_threshold);
  const critVal = parseValue(target?.critical_threshold);

  const values = series.map((s) => s.v);
  const overlays = [targetVal, warnVal, critVal].filter(
    (v): v is number => v !== undefined,
  );
  const yMin = Math.min(...values, ...overlays);
  const yMax = Math.max(...values, ...overlays);

  return (
    <AxisChart
      tExtent={[series[0].t, series[series.length - 1].t]}
      yExtent={[yMin, yMax]}
      yBounds={fractionBounds(unit, [...values, ...overlays])}
      yFormat={(v) => formatValue(v, unit)}
      xLabel="Observed"
      yLabel={unitLabel(unit)}
      width={width}
      height={height}
      testid={testid}
      className="text-primary"
    >
      {({ x, y, plot }) => (
        <>
          {/* threshold overlays */}
          {targetVal !== undefined ? (
            <line
              x1={plot.left}
              x2={plot.right}
              y1={y(targetVal)}
              y2={y(targetVal)}
              stroke="currentColor"
              strokeOpacity={0.4}
              strokeDasharray="4 2"
              strokeWidth={1}
              data-testid={testid ? `${testid}-target-line` : undefined}
            />
          ) : null}
          {warnVal !== undefined ? (
            <line
              x1={plot.left}
              x2={plot.right}
              y1={y(warnVal)}
              y2={y(warnVal)}
              stroke="orange"
              strokeOpacity={0.6}
              strokeDasharray="2 2"
              strokeWidth={1}
              data-testid={testid ? `${testid}-warning-line` : undefined}
            />
          ) : null}
          {critVal !== undefined ? (
            <line
              x1={plot.left}
              x2={plot.right}
              y1={y(critVal)}
              y2={y(critVal)}
              stroke="red"
              strokeOpacity={0.6}
              strokeDasharray="2 2"
              strokeWidth={1}
              data-testid={testid ? `${testid}-critical-line` : undefined}
            />
          ) : null}

          {/* series */}
          <path
            d={series
              .map(
                (s, i) =>
                  `${i === 0 ? "M" : "L"}${x(s.t).toFixed(2)},${y(s.v).toFixed(
                    2,
                  )}`,
              )
              .join(" ")}
            fill="none"
            stroke="currentColor"
            strokeWidth={2}
            strokeLinecap="round"
            strokeLinejoin="round"
          />
          {series.map((s, i) => (
            <circle
              key={`${s.t}-${i}`}
              cx={x(s.t)}
              cy={y(s.v)}
              r={2.5}
              fill="currentColor"
            />
          ))}
        </>
      )}
    </AxisChart>
  );
}

// unitLabel names the value axis from the metric's unit.
function unitLabel(unit: string): string {
  switch (unit) {
    case "percent":
    case "ratio":
      return "Percent";
    case "days":
      return "Days";
    case "hours":
      return "Hours";
    case "count":
      return "Count";
    case "dollars":
    case "usd":
      return "USD";
    default:
      return "Value";
  }
}
