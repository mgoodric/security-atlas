// Slice 098 — pure filter logic for the /controls list view.
//
// All filter-related calculations live here as pure functions so they
// can be vitest-unit-tested without spinning up React. The page imports
// these and applies them to the fetched anchorWire rows.
//
// Constitutional commitment: this module knows nothing about React,
// useSearchParams, or the BFF. It is data-in, data-out.

import type { Anchor } from "@/lib/api";

/**
 * The "all values" sentinel. Used as the default filter value on every
 * pill — selecting it disables that filter. The literal string "all"
 * round-trips cleanly through the URL query string.
 */
export const ALL = "all" as const;

export type ControlFilters = {
  framework: string;
  family: string;
  result: string;
  freshness: string;
  /**
   * Slice 224 — scope cell filter. `ALL` means no scope narrowing; a
   * UUID value narrows controls to those whose `applicability_expr`
   * intersects with the given scope cell (server-side intersection;
   * the BFF forwards this id to the upstream as `?scope=<id>`). The
   * client never receives `applicability_expr` (P0-224-2).
   */
  scope: string;
};

export const DEFAULT_FILTERS: ControlFilters = {
  framework: ALL,
  family: ALL,
  result: ALL,
  freshness: ALL,
  scope: ALL,
};

/**
 * True when no filter is narrowing the result set.
 */
export function isDefault(filters: ControlFilters): boolean {
  return (
    filters.framework === ALL &&
    filters.family === ALL &&
    filters.result === ALL &&
    filters.freshness === ALL &&
    filters.scope === ALL
  );
}

/**
 * Narrow an anchor list against the active filter set.
 *
 * Because the slice ships without per-anchor state (the per-row state
 * fan-out is rejected — see slice 098 §spillover, spillover slice 104),
 * the `result` and `freshness` filters narrow against the optional
 * state attached to each anchor row. If state is absent, those filters
 * skip the row when the filter is set (non-ALL), so the user sees only
 * the anchors that have data.
 */
export function applyFilters(
  rows: AnchorRow[],
  filters: ControlFilters,
): AnchorRow[] {
  return rows.filter((row) => {
    if (
      filters.family !== ALL &&
      row.anchor.family.toLowerCase() !== filters.family.toLowerCase()
    ) {
      return false;
    }
    if (filters.result !== ALL) {
      if (!row.state || row.state.result !== filters.result) return false;
    }
    if (filters.freshness !== ALL) {
      if (!row.state || row.state.freshness_status !== filters.freshness) {
        return false;
      }
    }
    // framework filter is a no-op for v1 — the anchorWire doesn't carry
    // the per-anchor framework set; spillover 104 brings it. The pill
    // still renders so the UI shape stays stable.
    return true;
  });
}

/**
 * Extract the unique family set from a row list. Used to drive the
 * "Family" pill options. Sorted alphabetically for stable ordering.
 */
export function uniqueFamilies(rows: AnchorRow[]): string[] {
  const seen = new Set<string>();
  for (const r of rows) {
    if (r.anchor.family) seen.add(r.anchor.family);
  }
  return Array.from(seen).sort();
}

/**
 * Merge a partial filter update onto the existing filter set.
 */
export function setFilter(
  filters: ControlFilters,
  key: keyof ControlFilters,
  value: string,
): ControlFilters {
  return { ...filters, [key]: value };
}

/**
 * Clear all filters back to the default.
 */
export function clearFilters(): ControlFilters {
  return { ...DEFAULT_FILTERS };
}

/**
 * Slice 226 — render-helper for the Frameworks column. Pure data-in /
 * data-out so the cell renderer in `page.tsx` can stay JSX-only and so
 * the formatting contract is vitest-pinned.
 *
 * Contract (matches `Plans/mockups/controls.html` line 217):
 *   - Non-empty input → join with " · " (middle-dot, U+00B7,
 *     surrounded by single spaces).
 *   - Empty input → the em-dash placeholder, mirroring the `—`
 *     fallback the State / Freshness / Last-observed cells use.
 *
 * The function does NOT sort or transform the input — the backend
 * (`internal/catalog.SortedFrameworkDisplayCodes`) already ships
 * sorted display abbreviations. P0-226-2: no slug → display mapping
 * in the frontend.
 */
export const FRAMEWORKS_EMPTY_PLACEHOLDER = "—";
export const FRAMEWORKS_JOIN_SEPARATOR = " · ";
export function formatFrameworksCell(frameworks: string[]): string {
  if (frameworks.length === 0) return FRAMEWORKS_EMPTY_PLACEHOLDER;
  return frameworks.join(FRAMEWORKS_JOIN_SEPARATOR);
}

/**
 * One row of the controls table: an anchor (always present), an
 * optional state cell (slice 104), and the slice-226 per-anchor
 * framework set (display abbreviations — `SOC2`, `ISO`, `CSF`, etc.).
 *
 * Slice 226: `frameworks` is always a string array on the wire —
 * empty array when the anchor has no satisfaction edges; the page
 * renders `—` in that branch (AC-6).
 */
export type AnchorRow = {
  anchor: Anchor;
  state: AnchorRowState | null;
  frameworks: string[];
};

export type AnchorRowState = {
  result: string;
  freshness_status: string;
  last_observed_at: string | null;
};
