// #2058 — one severity display for inherent and residual.
//
// Both axes sit on the 1..25 scale (likelihood × impact for inherent;
// inherent × (1 − control effectiveness) for residual), so they render
// as the same banded pill: the number in the band's color, then the band
// label. `testId` lands on the number (existing specs read it as the
// bare score); the label carries `${testId}-band`.

import { severityBandLabel, type SeverityBand } from "./filters";

export function SeverityPill({
  value,
  band,
  className,
  testId,
  title,
  size = "sm",
}: {
  value: string;
  band: SeverityBand;
  className: string;
  testId: string;
  title?: string;
  size?: "sm" | "md";
}) {
  const box =
    size === "md" ? "min-w-7 h-7 px-1 text-sm" : "min-w-6 h-6 px-1 text-[11px]";
  return (
    <span className="inline-flex items-center gap-1.5" title={title}>
      <span
        className={`inline-flex items-center justify-center font-semibold rounded ${box} ${className}`}
        data-testid={testId}
      >
        {value}
      </span>
      <span
        className="text-xs text-muted-foreground"
        data-testid={`${testId}-band`}
      >
        {severityBandLabel(band)}
      </span>
    </span>
  );
}
