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
  actions,
  filterRow,
  children,
}: ListPageProps) {
  return (
    <div data-testid="list-page" className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
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
