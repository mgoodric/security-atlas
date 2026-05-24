"use client";

// Slice 237 — cursor-paginated footer for list views whose upstream
// wire is cursor-based (the `/evidence` ledger is the v1 consumer).
//
// Sibling primitive to slice 246's `<ListPagination>` (which is
// page-number / client-side-slicing — fundamentally incompatible with a
// cursor wire). The two primitives live side-by-side in the list-shell
// barrel; consuming pages pick the one that matches their upstream
// shape:
//   - `/risks`, `/controls`, `/policies` → ListPagination (full-set
//     wire, client-side slicing)
//   - `/evidence`                         → CursorPagination (opaque
//     cursor wire, keyset-paginated server-side)
//
// Spec anti-criterion P0-237-1 forbids offset/limit math here — the
// component intentionally has no `currentPage` or `totalPages` prop. The
// only navigational primitives it exposes are "next cursor exists" and
// "previous cursor exists" (the latter sourced from a client-side stack
// the page owns).
//
// Anti-criterion P0-237-2: the stack is page-state — passed in as props,
// not persisted. The component is a dumb renderer; storage is the
// page's concern.
//
// Mockup parity: `Plans/mockups/evidence.html` lines 266-272.

import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

export type CursorPaginationProps = {
  /**
   * Number of records visible on the current page. Used to render the
   * "Showing N records" summary. Slice 236 surfaces the tenant-wide
   * `total` separately via the meta line above the table; this footer
   * intentionally does not re-render the total to avoid double-printing
   * the same number. Callers pass `records.length`.
   */
  recordCount: number;
  /**
   * True when the upstream returned a non-empty `next_cursor` — i.e.
   * another page exists. Drives the Next button's disabled state.
   */
  hasNext: boolean;
  /**
   * True when the operator can step back. The page derives this from
   * EITHER (a) the in-memory cursor stack having entries, OR (b) the
   * URL carrying a `?cursor=` value with an empty stack (the
   * deep-linked / first-Previous-clears-URL case). The component does
   * not care which — it only renders the button state.
   */
  hasPrevious: boolean;
  /** Fired when the user clicks Next. Page-level wiring pushes the current
   *  URL cursor onto the stack and replaces the URL with the new cursor. */
  onNext: () => void;
  /** Fired when the user clicks Previous. Page-level wiring pops the
   *  stack and replaces the URL with the popped cursor (or clears the
   *  URL cursor when the stack is empty). */
  onPrevious: () => void;
  /**
   * Optional override for the testid prefix. Defaults to
   * `cursor-pagination`. Page-local consumers MAY pass a page-specific
   * prefix (e.g. `evidence-pagination`) so Playwright selectors stay
   * disambiguable.
   */
  testIdPrefix?: string;
};

/**
 * Pure cursor-stack helper: push a cursor onto a stack and return a
 * NEW array (no input mutation). Pages that wire Next click their
 * current URL cursor (which is what they were "on" when the click
 * happened) onto the stack so a Previous click can pop it back.
 */
export function pushCursor(stack: readonly string[], cur: string): string[] {
  return [...stack, cur];
}

/**
 * Pure cursor-stack helper: pop the most-recent cursor off a stack
 * and return both the popped value and the remaining stack. The
 * remaining stack is a NEW array (no input mutation).
 *
 * Empty input returns `{ popped: undefined, rest: [] }` — callers (the
 * `/evidence` page) treat that as the signal to clear the URL cursor
 * (return to the unparameterized first page).
 */
export function popCursor(stack: readonly string[]): {
  popped: string | undefined;
  rest: string[];
} {
  if (stack.length === 0) return { popped: undefined, rest: [] };
  return {
    popped: stack[stack.length - 1],
    rest: stack.slice(0, -1),
  };
}

export function CursorPagination({
  recordCount,
  hasNext,
  hasPrevious,
  onNext,
  onPrevious,
  testIdPrefix = "cursor-pagination",
}: CursorPaginationProps): ReactNode {
  // Mockup parity: the footer shows "Showing N record(s)" on the left
  // and Previous / Next buttons on the right. The tenant-wide ledger
  // total + the filter window count are rendered in the meta line ABOVE
  // the table (slice 236) — this footer is page-navigation chrome only.
  const summary =
    recordCount === 1
      ? "Showing 1 record on this page"
      : `Showing ${recordCount} records on this page`;

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
          disabled={!hasPrevious}
          onClick={() => {
            if (hasPrevious) onPrevious();
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
          disabled={!hasNext}
          onClick={() => {
            if (hasNext) onNext();
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
