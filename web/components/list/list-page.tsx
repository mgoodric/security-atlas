"use client";

// Slice 098 — generic list-view page chrome.
//
// Wraps a list-view's title + description + action buttons in a
// consistent layout. Used by all five list-view slices
// (098/099/100/101/102) so /controls, /evidence, /risks, /policies,
// /audits share the same visual rhythm.
//
// Layout:
//   <header>: title (h1) + subtitle (p) on the left; actions slot on
//             the right.
//   <main>:   filter row (slot) + content (slot).
//
// The page wires the actual data fetching, filter state, and table
// rendering. This shell only does layout.

import type { ReactNode } from "react";

export type ListPageProps = {
  title: string;
  subtitle?: ReactNode;
  /**
   * Optional inline adornment rendered to the right of the H1, on the
   * same baseline. Used by /audits for the status tally
   * (`1 in_progress · 4 frozen · 1 closed`) per slice 215. The
   * adornment is a sibling of the H1 inside a `flex items-baseline`
   * row, matching `Plans/mockups/audits.html` lines 109-112.
   *
   * Callers SHOULD wrap the adornment in a `<span>` with semantic
   * styling (e.g. small muted text) and an `aria-label` describing
   * what the inline value is — see slice 215 AC-3.
   */
  titleAdornment?: ReactNode;
  /** Right-side action buttons (e.g. "Export CSV", "New X"). */
  actions?: ReactNode;
  /** The horizontal filter pill row — usually a `<FilterPills>`. */
  filterRow?: ReactNode;
  /** The list table or its skeleton/empty-state variants. */
  children: ReactNode;
};

export function ListPage({
  title,
  subtitle,
  titleAdornment,
  actions,
  filterRow,
  children,
}: ListPageProps) {
  return (
    <div data-testid="list-page" className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="flex items-baseline gap-3 flex-wrap">
            <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
            {titleAdornment}
          </div>
          {subtitle ? (
            <p className="text-sm text-muted-foreground mt-0.5">{subtitle}</p>
          ) : null}
        </div>
        {actions ? (
          <div className="flex items-center gap-2">{actions}</div>
        ) : null}
      </div>
      {filterRow ? <div className="mb-1">{filterRow}</div> : null}
      <div data-testid="list-page-content">{children}</div>
    </div>
  );
}
