// Slice 101 — minimal accessible <Progress> primitive.
//
// The codebase has no existing <Progress> primitive (shadcn upstream
// uses @radix-ui/react-progress which is not yet a dependency here).
// Slice 101 ships a Radix-free shadcn-style component: a div with
// `role="progressbar"` + ARIA-valuenow/min/max attributes so screen
// readers announce "47 of 52 acknowledged · 90%" via `aria-label`.
//
// Why scaffold here vs. add Radix: keep the dependency surface minimal.
// The component is a passive bar (no thumb drag, no animation
// transitions beyond CSS) — Radix adds nothing for our use case.
//
// Wired for the slice 101 policies list view (acknowledgment progress
// column). Reusable by future slices that need a bar visualization
// without a slider's interactivity.

import { cn } from "@/lib/utils";

export type ProgressProps = {
  /** Percent value in 0..100. Values outside the range are clamped. */
  value: number;
  /** Required accessible label — read by screen readers verbatim. */
  "aria-label": string;
  /** Optional Tailwind classes for the outer track. */
  className?: string;
  /** Optional Tailwind classes for the inner indicator (e.g. color). */
  indicatorClassName?: string;
};

export function Progress({
  value,
  "aria-label": ariaLabel,
  className,
  indicatorClassName,
}: ProgressProps) {
  const clamped = Math.max(0, Math.min(100, value));
  return (
    <div
      role="progressbar"
      aria-label={ariaLabel}
      aria-valuenow={Math.round(clamped)}
      aria-valuemin={0}
      aria-valuemax={100}
      data-slot="progress"
      className={cn(
        "h-1.5 w-16 overflow-hidden rounded-full bg-muted",
        className,
      )}
    >
      <div
        data-slot="progress-indicator"
        className={cn("h-full transition-all", indicatorClassName)}
        style={{ width: `${clamped}%` }}
      />
    </div>
  );
}
