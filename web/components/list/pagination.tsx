"use client";

// Slice 246 — generic list-view pagination footer.
//
// Used by list pages whose upstream API ships the full filtered set in a
// single response (the v1 shape across /risks, /controls, /policies, and
// peers). The page slices its own `visible` array client-side; this
// component renders the "Showing M–N of TOTAL" footer plus Prev / Next
// buttons that match the iteration-1 mockups verbatim
// (`Plans/_archive/mockups/risks.html` lines 267-273, `controls.html`, etc.).
//
// Constitutional commitment: NO page-specific imports, types, or
// strings here. The page owns the page-state (URL binding, default
// size, reset-on-filter-change); this primitive only renders the
// footer + emits an `onPageChange` callback.
//
// Server-side LIMIT/OFFSET is explicitly out of scope per slice 246
// P0-246-1. If a future slice extends the wire shape, this component
// stays unchanged — the page's `totalCount` will simply derive from a
// header field instead of `visible.length`.

import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

export type ListPaginationProps = {
  /** 1-indexed current page. */
  currentPage: number;
  /** Rows-per-page (page-defined constant; greppable per P0-246-4). */
  pageSize: number;
  /** Total row count of the FILTERED set (not the raw upstream total). */
  totalCount: number;
  /** Fired when the user clicks Prev/Next. Page is 1-indexed. */
  onPageChange: (page: number) => void;
  /**
   * Optional override for the testid prefix. Defaults to
   * `list-pagination`. Page-local consumers (e.g. risks) MAY pass a
   * page-specific prefix so multiple paginated lists on the same screen
   * stay disambiguable, but in v1 every page has at most one footer.
   */
  testIdPrefix?: string;
};

/**
 * Pure pagination math. Exported so vitest can cover the edge cases
 * (page 1, last page, empty, total < pageSize) without rendering a
 * React tree.
 *
 * Returns 1-indexed `from`/`to` bounds for the current page slice.
 * When `totalCount === 0`, returns `{ from: 0, to: 0, totalPages: 0 }` —
 * the footer renders "Showing 0 of 0" in that case.
 */
export function paginationBounds(
  currentPage: number,
  pageSize: number,
  totalCount: number,
): { from: number; to: number; totalPages: number } {
  if (totalCount <= 0) {
    return { from: 0, to: 0, totalPages: 0 };
  }
  const totalPages = Math.max(1, Math.ceil(totalCount / pageSize));
  const safePage = Math.min(Math.max(1, currentPage), totalPages);
  const from = (safePage - 1) * pageSize + 1;
  const to = Math.min(safePage * pageSize, totalCount);
  return { from, to, totalPages };
}

/**
 * Pure array slicer — returns the page-N slice of `rows` for a given
 * `pageSize`. Out-of-range pages clamp to the last available page.
 * Empty input returns an empty array.
 */
export function paginateRows<T>(
  rows: readonly T[],
  currentPage: number,
  pageSize: number,
): T[] {
  if (rows.length === 0) return [];
  const { from, to } = paginationBounds(currentPage, pageSize, rows.length);
  // `from` is 1-indexed; convert to a 0-indexed slice start.
  return rows.slice(from - 1, to);
}

export function ListPagination({
  currentPage,
  pageSize,
  totalCount,
  onPageChange,
  testIdPrefix = "list-pagination",
}: ListPaginationProps): ReactNode {
  const { from, to, totalPages } = paginationBounds(
    currentPage,
    pageSize,
    totalCount,
  );
  const isFirst = totalPages === 0 || currentPage <= 1;
  const isLast = totalPages === 0 || currentPage >= totalPages;

  // AC-4: truth-telling chrome — the M–N range and TOTAL are computed
  // from the filtered set the page passes in. When the set is empty,
  // the footer says "Showing 0 of 0" rather than hiding (so the user
  // sees the pagination control exists and is honestly empty).
  const summary =
    totalCount === 0
      ? "Showing 0 of 0"
      : from === to
        ? `Showing ${from} of ${totalCount}`
        : `Showing ${from}–${to} of ${totalCount}`;

  return (
    <div
      data-testid={testIdPrefix}
      className="border-t px-5 py-2.5 flex items-center justify-between text-xs text-muted-foreground bg-muted/30"
    >
      <span data-testid={`${testIdPrefix}-summary`}>{summary}</span>
      <div className="flex items-center gap-2">
        <button
          type="button"
          data-testid={`${testIdPrefix}-prev`}
          disabled={isFirst}
          onClick={() => {
            if (!isFirst) onPageChange(currentPage - 1);
          }}
          className={cn(
            "px-2 py-1 border rounded text-foreground bg-card hover:bg-muted",
            "disabled:opacity-50 disabled:cursor-not-allowed disabled:hover:bg-card",
          )}
        >
          Previous
        </button>
        <button
          type="button"
          data-testid={`${testIdPrefix}-next`}
          disabled={isLast}
          onClick={() => {
            if (!isLast) onPageChange(currentPage + 1);
          }}
          className={cn(
            "px-2 py-1 border rounded text-foreground bg-card hover:bg-muted",
            "disabled:opacity-50 disabled:cursor-not-allowed disabled:hover:bg-card",
          )}
        >
          Next
        </button>
      </div>
    </div>
  );
}
