"use client";

// Slice 681 / ATLAS-039 — clickable, accessible column header for the
// /risks register sort.
//
// The list-table primitive (web/components/list/list-table.tsx) renders
// whatever `header` ReactNode the page supplies into a `<th>`. This
// component is that node for the three sortable columns (residual /
// inherent severity / review-due): a `<button>` that toggles the sort
// on click and exposes the current sort to assistive tech via
// `aria-sort` on the surrounding header cell is not reachable from here,
// so the button itself carries an `aria-label` describing the action +
// the current state, and a visible direction caret.
//
// Accessibility (slice 359 / 361 / 363 a11y lineage):
//   - a real `<button>` (keyboard-operable, focusable) — NOT a click
//     handler on a `<span>`.
//   - `aria-label` states the column AND the current/next ordering so a
//     screen-reader user knows both what clicking does and how the
//     column is sorted now.
//   - the direction caret is `aria-hidden` (the label already conveys
//     it) so it is not double-announced.

import type { ReactNode } from "react";

import type { SortDir, SortKey, SortState } from "./sort";

export function SortableHeader({
  sortKey,
  label,
  title,
  state,
  onSort,
}: {
  /** The column this header sorts by. */
  sortKey: SortKey;
  /** Visible header label (may include the slice-680 axis-name copy). */
  label: ReactNode;
  /** Native tooltip text (the slice-680 axis-disambiguation copy). */
  title?: string;
  /** The register's current sort. */
  state: SortState;
  /** Click handler — the page toggles the URL sort param. */
  onSort: (key: SortKey) => void;
}) {
  const isActive = state.key === sortKey;
  const dir: SortDir | null = isActive ? state.dir : null;

  // Caret: ▲ ascending, ▼ descending, ↕ inactive (sortable but not the
  // active column). Plain glyphs so no icon dependency is added.
  const caret = dir === "asc" ? "▲" : dir === "desc" ? "▼" : "↕";

  const ariaLabel = isActive
    ? `${ariaText(label)}, sorted ${
        dir === "asc" ? "ascending" : "descending"
      }. Activate to sort ${dir === "asc" ? "descending" : "ascending"}.`
    : `${ariaText(label)}, not sorted. Activate to sort descending.`;

  return (
    <button
      type="button"
      onClick={() => onSort(sortKey)}
      title={title}
      aria-label={ariaLabel}
      data-testid={`risks-sort-${sortKey}`}
      data-active={isActive ? "true" : "false"}
      data-dir={dir ?? "none"}
      className="inline-flex items-center gap-1 text-left uppercase tracking-wider hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring rounded-sm cursor-pointer"
    >
      <span>{label}</span>
      <span
        aria-hidden
        className={isActive ? "text-foreground" : "text-muted-foreground/60"}
      >
        {caret}
      </span>
    </button>
  );
}

/**
 * Best-effort accessible text for a header whose visible label may be a
 * JSX node (the slice-680 columns wrap the label in a tooltip span). For
 * the slice-681 sortable columns the label is always a plain string, so
 * this returns it directly; a non-string node falls back to the column
 * key's human form so the aria-label is never empty.
 */
function ariaText(label: ReactNode): string {
  return typeof label === "string" ? label : "Column";
}
