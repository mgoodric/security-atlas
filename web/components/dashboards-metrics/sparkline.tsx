// Slice 097 — inline-SVG sparkline (no chart library).
//
// Renders a 90-day observation series as a 120x32 svg <path>. The line
// uses the shadcn theme's --primary token via Tailwind's text-primary
// + currentColor on the SVG stroke. No external dependency. See
// `docs/audit-log/097-metrics-dashboard-cascade-view-decisions.md` D2.

import type { Observation } from "@/lib/api/metrics";
import { parseValue } from "./format";

export type SparklineProps = {
  observations: Observation[];
  width?: number;
  height?: number;
  className?: string;
  testid?: string;
};

export function Sparkline({
  observations,
  width = 120,
  height = 32,
  className,
  testid,
}: SparklineProps) {
  const points = observations
    .slice()
    .sort(
      (a, b) =>
        new Date(a.observed_at).getTime() - new Date(b.observed_at).getTime(),
    )
    .map((o) => parseValue(o.numeric_value))
    .filter((v): v is number => v !== undefined);

  if (points.length === 0) {
    return (
      <div
        data-testid={testid ? `${testid}-empty` : undefined}
        className="flex h-8 w-full items-center text-xs text-muted-foreground"
      >
        no data
      </div>
    );
  }
  if (points.length === 1) {
    // Render a single dot — a line of 1 sample is degenerate.
    return (
      <svg
        width={width}
        height={height}
        viewBox={`0 0 ${width} ${height}`}
        className={className}
        data-testid={testid}
      >
        <circle cx={width / 2} cy={height / 2} r="2.5" fill="currentColor" />
      </svg>
    );
  }

  const min = Math.min(...points);
  const max = Math.max(...points);
  const range = max - min || 1;
  const stepX = width / (points.length - 1);
  const path = points
    .map((v, i) => {
      const x = i * stepX;
      const y = height - ((v - min) / range) * height;
      return `${i === 0 ? "M" : "L"}${x.toFixed(2)},${y.toFixed(2)}`;
    })
    .join(" ");

  return (
    <svg
      width={width}
      height={height}
      viewBox={`0 0 ${width} ${height}`}
      className={`text-primary ${className ?? ""}`}
      data-testid={testid}
    >
      <path
        d={path}
        fill="none"
        stroke="currentColor"
        strokeWidth={1.5}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}
