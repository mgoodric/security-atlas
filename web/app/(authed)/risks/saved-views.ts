// #2133 — saved views for the risk register.
//
// A view is the register's URL state: its filters, sort, roll-ups toggle
// and chosen columns, stored server-side under the `risks` surface. The
// stored entries are `GET /v1/risks` parameters (the filters by their own
// names, `sort`, `include_rollups`) plus `cols`, so the platform checks
// them against the same allow-list as the list itself. Paging is not part
// of a view.
//
// Pure functions, no React, no fetch: vitest-pinned.

import {
  COLUMNS_PARAM,
  FILTER_KEYS,
  filtersFromParams,
  normalizeFilterValue,
  parseColumns,
  DEFAULT_FILTERS,
} from "./server-query";
import { parseSortState, sortParam } from "./sort";

/** The register's URL keys a view carries beside the filters. */
export const SORT_PARAM = "sort";
export const ROLLUPS_PARAM = "rollups";

/** The platform's roll-ups parameter, as a view stores it. */
const INCLUDE_ROLLUPS = "include_rollups";

type ParamReader = { get(key: string): string | null };

/**
 * The view entries for the register's current URL. Defaults are left out,
 * so an unfiltered register saves an empty view.
 */
export function viewFromSearch(search: ParamReader): Record<string, string> {
  const out: Record<string, string> = {};
  const filters = filtersFromParams(search);
  for (const k of FILTER_KEYS) {
    if (filters[k] !== DEFAULT_FILTERS[k]) out[k] = filters[k];
  }
  const sort = sortParam(parseSortState(search.get(SORT_PARAM)));
  if (sort) out.sort = sort;
  if (search.get(ROLLUPS_PARAM) === "1") out[INCLUDE_ROLLUPS] = "true";
  const cols = parseColumns(search.get(COLUMNS_PARAM));
  if (cols.length > 0) out[COLUMNS_PARAM] = cols.join(",");
  return out;
}

/**
 * The register URL query a saved view applies. Every entry is normalized
 * again, so a value the register no longer knows widens the list instead
 * of failing it.
 */
export function searchFromView(view: Record<string, string>): string {
  const sp = new URLSearchParams();
  for (const k of FILTER_KEYS) {
    const v = normalizeFilterValue(k, view[k] ?? "");
    if (v !== DEFAULT_FILTERS[k]) sp.set(k, v);
  }
  const sort = sortParam(parseSortState(view.sort ?? null));
  if (sort) sp.set(SORT_PARAM, sort);
  if (view[INCLUDE_ROLLUPS] === "true") sp.set(ROLLUPS_PARAM, "1");
  const cols = parseColumns(view[COLUMNS_PARAM]);
  if (cols.length > 0) sp.set(COLUMNS_PARAM, cols.join(","));
  return sp.toString();
}
