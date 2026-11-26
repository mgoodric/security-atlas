// Slice 098 — pure filter state for the /controls list view.
//
// #2131: the filters are applied on the server. This module holds the
// filter set the URL carries and the helpers that edit it; `./server-query`
// turns it into the `GET /v1/controls?include=state` query. Nothing here
// narrows or sorts rows in the browser.
//
// Constitutional commitment: this module knows nothing about React,
// useSearchParams, or the BFF. It is data-in, data-out.

import type { Anchor } from "@/lib/api/anchors";

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
  /**
   * Issue #1736 — how well a control's SCF anchor fits it: `exact`,
   * `partial` or `none`. Only "My controls" rows carry a fit.
   */
  anchor_fit: string;
  /**
   * #2131 — the assigned owner: a user id, or `none` for controls nobody
   * owns. "My controls" only.
   */
  owner: string;
  /**
   * #2131 — the server sort, `key` or `-key` (see `./server-query`).
   * `ALL` is the default order: failing first, then stalest evidence.
   * It rides with the filters so a saved view keeps its order.
   */
  sort: string;
};

export const DEFAULT_FILTERS: ControlFilters = {
  framework: ALL,
  family: ALL,
  result: ALL,
  freshness: ALL,
  scope: ALL,
  anchor_fit: ALL,
  owner: ALL,
  sort: ALL,
};

/** Every filter key, in URL order. */
export const FILTER_KEYS = Object.keys(
  DEFAULT_FILTERS,
) as (keyof ControlFilters)[];

/**
 * True when no filter is narrowing the result set and the order is the
 * default.
 */
export function isDefault(filters: ControlFilters): boolean {
  return FILTER_KEYS.every((k) => filters[k] === ALL);
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
 * Contract (matches `Plans/_archive/mockups/controls.html` line 217):
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
//
// Issue #1741: `key` is the row's identity (the control id in the "My
// controls" view, the anchor id in the catalog view), and `controlId` is
// the tenant control the row opens, or null for a catalog anchor the
// tenant has no control on. Rows link by `controlId`, never by the
// anchor id, which the control detail page cannot resolve.
export type AnchorRow = {
  key: string;
  controlId: string | null;
  anchor: Anchor;
  state: AnchorRowState | null;
  frameworks: string[];
  /**
   * Issue #1736 — the control's anchor fit and its note. Set on "My
   * controls" rows; absent on catalog anchor rows, which have no control.
   */
  anchorFit?: AnchorFit;
  anchorFitNote?: string | null;
  /** #2043 — the in-place demo seed added this control; the row shows a Demo badge. */
  demo?: boolean;
  /** #2128 — the assigned owner's user id; null or absent when unassigned. */
  ownerUserId?: string | null;
  /**
   * #2392 — the control still has an undecided SCF remap proposal (open or
   * rejected), so it sits on an older release's anchor until a reviewer
   * settles it. Absent on catalog rows.
   */
  legacyAnchor?: boolean;
};

export type AnchorFit = "exact" | "partial" | "none";

export const ANCHOR_FIT_OPTIONS: { value: string; label: string }[] = [
  { value: ALL, label: "All fits" },
  { value: "exact", label: "Exact" },
  { value: "partial", label: "Partial" },
  { value: "none", label: "No SCF fit" },
];

/**
 * Issue #1736 — the marker a controls-list row carries for a non-exact
 * anchor fit, or null for an exact fit (and for catalog rows). The
 * marker's tooltip is the fit note.
 */
export function anchorFitMarker(
  row: Pick<AnchorRow, "anchorFit" | "anchorFitNote">,
): { label: string; title: string } | null {
  if (row.anchorFit !== "partial" && row.anchorFit !== "none") return null;
  return {
    label: row.anchorFit === "partial" ? "Partial fit" : "No SCF fit",
    title: row.anchorFitNote ?? "",
  };
}

/** Display label for a fit value; absent or unknown reads as exact. */
export function anchorFitLabel(fit: string | undefined): string {
  if (fit === "partial") return "Partial";
  if (fit === "none") return "No SCF fit";
  return "Exact";
}

export type AnchorRowState = {
  result: string;
  freshness_status: string;
  last_observed_at: string | null;
};
