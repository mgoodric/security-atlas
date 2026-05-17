// Slice 097 — inline-SVG line chart for the per-metric detail page.
//
// Renders a 480x200 line chart over the observation series with
// horizontal threshold overlays (target, warning, critical) when a
// target row is set. Theme-token colors via Tailwind classes. See
// decisions log D2 for rationale (no chart library).

import type { MetricTarget, Observation } from "@/lib/api/metrics";
import { parseValue } from "./format";

export type LineChartProps = {
  observations: Observation[];
  target: MetricTarget | null;
  width?: number;
  height?: number;
  testid?: string;
};

const PADDING_X = 32;
const PADDING_Y = 12;

export function LineChart({
  observations,
  target,
  width = 480,
  height = 200,
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
        className="flex h-[200px] w-full items-center justify-center rounded-md border border-dashed text-sm text-muted-foreground"
      >
        No observations in the selected window.
      </div>
    );
  }

  const innerW = width - PADDING_X * 2;
  const innerH = height - PADDING_Y * 2;
  const t0 = series[0].t;
  const t1 = series[series.length - 1].t;
  const tSpan = t1 - t0 || 1;

  const targetVal = parseValue(target?.target_value);
  const warnVal = parseValue(target?.warning_threshold);
  const critVal = parseValue(target?.critical_threshold);

  const dataMin = Math.min(...series.map((s) => s.v));
  const dataMax = Math.max(...series.map((s) => s.v));
  const overlays = [targetVal, warnVal, critVal].filter(
    (v): v is number => v !== undefined,
  );
  const yMin = Math.min(dataMin, ...overlays);
  const yMax = Math.max(dataMax, ...overlays);
  const yRange = yMax - yMin || 1;

  const x = (t: number) => PADDING_X + ((t - t0) / tSpan) * innerW;
  const y = (v: number) => PADDING_Y + (1 - (v - yMin) / yRange) * innerH;

  const path = series
    .map(
      (s, i) =>
        `${i === 0 ? "M" : "L"}${x(s.t).toFixed(2)},${y(s.v).toFixed(2)}`,
    )
    .join(" ");

  return (
    <svg
      width="100%"
      viewBox={`0 0 ${width} ${height}`}
      preserveAspectRatio="xMidYMid meet"
      data-testid={testid}
      className="text-primary"
    >
      {/* threshold overlays */}
      {targetVal !== undefined ? (
        <line
          x1={PADDING_X}
          x2={width - PADDING_X}
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
          x1={PADDING_X}
          x2={width - PADDING_X}
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
          x1={PADDING_X}
          x2={width - PADDING_X}
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
        d={path}
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
    </svg>
  );
}
