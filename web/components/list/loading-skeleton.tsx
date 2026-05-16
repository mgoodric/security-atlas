"use client";

// Slice 098 — generic list-view loading skeleton.
//
// Three shimmer rows mirroring the column widths of the consuming page's
// table. Used by all five list-view slices (098/099/100/101/102).
//
// Design reference: `Plans/canvas/12-ui-fill-in-design-decisions.md` §3 —
// "3 shimmer rows that mirror the table's column widths" + anti-pattern
// "generic centered spinners".
//
// The consuming page can override `rowCount` (default 3) or `columns`
// (array of Tailwind width classes per cell) but the shape stays the
// same: a header bar + N rows of left-aligned cells, all using the
// shadcn `<Skeleton>` primitive (animate-pulse + bg-muted).

import { Skeleton } from "@/components/ui/skeleton";

export type ListLoadingSkeletonProps = {
  /** Number of shimmer rows. Defaults to 3 per design doc §3. */
  rowCount?: number;
  /** Tailwind width class per cell. Defaults to the slice-098 column shape. */
  columns?: string[];
};

const DEFAULT_COLUMNS = ["w-16", "flex-1", "w-24", "w-14", "w-20"];

export function ListLoadingSkeleton({
  rowCount = 3,
  columns = DEFAULT_COLUMNS,
}: ListLoadingSkeletonProps) {
  return (
    <div
      data-testid="list-loading-skeleton"
      className="rounded-xl border bg-card overflow-hidden"
    >
      <div className="bg-muted/50 border-b px-5 py-2.5">
        <Skeleton className="h-3 w-1/3" />
      </div>
      <div className="divide-y">
        {Array.from({ length: rowCount }).map((_, i) => (
          <div
            key={i}
            data-testid="list-loading-skeleton-row"
            className="px-5 py-4 flex items-center gap-4"
          >
            {columns.map((width, j) => (
              <Skeleton key={j} className={`h-3 ${width}`} />
            ))}
          </div>
        ))}
      </div>
    </div>
  );
}
