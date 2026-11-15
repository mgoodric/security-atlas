// #2095 — shared chart wrapper: an inline SVG with a labelled y-axis
// fitted to the data and a dated x-axis. The caller draws its marks in
// `children`, given the scales. Tick labels and axis titles live in the
// margins, outside the plot area, so they never overlap a mark. The axis
// math is pure and tested in `lib/charts/axes.ts`.

import type { ReactNode } from "react";

import { formatDateTick, linearAxis, timeAxis } from "@/lib/charts/axes";

export type ChartScales = {
  x: (t: number) => number;
  y: (v: number) => number;
  plot: { left: number; right: number; top: number; bottom: number };
};

export type AxisChartProps = {
  // Time extent of the data (epoch ms).
  tExtent: [number, number];
  // Value extent to fit the y-axis to (data plus any overlays).
  yExtent: [number, number];
  yFormat: (v: number) => string;
  xLabel: string;
  yLabel: string;
  width?: number;
  height?: number;
  testid?: string;
  className?: string;
  children: (scales: ChartScales) => ReactNode;
};

const MARGIN = { top: 12, right: 20, bottom: 40, left: 64 };

export function AxisChart({
  tExtent,
  yExtent,
  yFormat,
  xLabel,
  yLabel,
  width = 480,
  height = 220,
  testid,
  className,
  children,
}: AxisChartProps) {
  const plot = {
    left: MARGIN.left,
    right: width - MARGIN.right,
    top: MARGIN.top,
    bottom: height - MARGIN.bottom,
  };
  const yAxis = linearAxis(yExtent[0], yExtent[1]);
  const xAxis = timeAxis(tExtent[0], tExtent[1]);
  const span = xAxis.max - xAxis.min;
  const ySpan = yAxis.max - yAxis.min || 1;

  const x = (t: number) =>
    plot.left + ((t - xAxis.min) / span) * (plot.right - plot.left);
  const y = (v: number) =>
    plot.bottom - ((v - yAxis.min) / ySpan) * (plot.bottom - plot.top);

  const tid = (s: string) => (testid ? `${testid}-${s}` : undefined);

  return (
    <svg
      width="100%"
      viewBox={`0 0 ${width} ${height}`}
      preserveAspectRatio="xMidYMid meet"
      data-testid={testid}
      className={className}
      role="img"
      aria-label={`${yLabel} by ${xLabel.toLowerCase()}`}
    >
      {/* y-axis: gridlines + tick labels in the left margin */}
      <g data-testid={tid("y-axis")} className="text-muted-foreground">
        {yAxis.ticks.map((v) => (
          <g key={`y-${v}`}>
            <line
              x1={plot.left}
              x2={plot.right}
              y1={y(v)}
              y2={y(v)}
              stroke="currentColor"
              strokeOpacity={0.15}
              strokeWidth={1}
            />
            <text
              x={plot.left - 6}
              y={y(v)}
              textAnchor="end"
              dominantBaseline="middle"
              fontSize={10}
              fill="currentColor"
              data-testid={tid("y-tick")}
            >
              {yFormat(v)}
            </text>
          </g>
        ))}
        <text
          transform={`translate(12 ${
            (plot.top + plot.bottom) / 2
          }) rotate(-90)`}
          textAnchor="middle"
          fontSize={10}
          fill="currentColor"
          data-testid={tid("y-label")}
        >
          {yLabel}
        </text>
      </g>

      {/* x-axis: baseline + dated ticks below the plot */}
      <g data-testid={tid("x-axis")} className="text-muted-foreground">
        <line
          x1={plot.left}
          x2={plot.right}
          y1={plot.bottom}
          y2={plot.bottom}
          stroke="currentColor"
          strokeOpacity={0.4}
          strokeWidth={1}
        />
        {xAxis.ticks.map((t, i) => (
          <text
            key={`x-${t}`}
            x={x(t)}
            y={plot.bottom + 14}
            // End ticks anchor inward so their labels stay on the canvas.
            textAnchor={
              i === 0
                ? "start"
                : i === xAxis.ticks.length - 1
                  ? "end"
                  : "middle"
            }
            fontSize={10}
            fill="currentColor"
            data-testid={tid("x-tick")}
          >
            {formatDateTick(t, span)}
          </text>
        ))}
        <text
          x={(plot.left + plot.right) / 2}
          y={height - 4}
          textAnchor="middle"
          fontSize={10}
          fill="currentColor"
          data-testid={tid("x-label")}
        >
          {xLabel}
        </text>
      </g>

      {children({ x, y, plot })}
    </svg>
  );
}
