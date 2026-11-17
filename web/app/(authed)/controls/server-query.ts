// #2131 — the /controls page asks the server for one filtered, sorted page
// instead of loading every control and narrowing it in the browser. This
// module is the pure half of that: it checks the URL's filter values, maps
// the table's sortable columns to the platform's sort keys, and builds the
// query each view sends.
//
// No React, no fetch. Data-in, data-out, vitest-pinned.

import { ALL, FILTER_KEYS, type ControlFilters } from "./filters";

/**
 * The platform's sort keys for `GET /v1/controls?include=state`, keyed by
 * the table column that sorts on each. The platform allow-lists the same
 * set; a key outside it is a 400 there, so the page never sends one.
 */
export const SORT_KEY_BY_COLUMN: Record<string, string> = {
  scf_id: "scf_id",
  name: "name",
  family: "family",
  result: "state",
  freshness_status: "freshness",
  last_observed_at: "last_observed_at",
  owner: "owner",
  frameworks: "frameworks",
};

const SORT_KEYS = new Set(Object.values(SORT_KEY_BY_COLUMN));

export type SortState = { key: string; desc: boolean };

/** The sort a `sort` value names, or null for the default order. */
export function parseSort(raw: string): SortState | null {
  if (!raw || raw === ALL) return null;
  const desc = raw.startsWith("-");
  const key = desc ? raw.slice(1) : raw;
  return SORT_KEYS.has(key) ? { key, desc } : null;
}

/**
 * The `sort` value after a click on a column header: ascending first, then
 * descending, then back to the default order.
 */
export function nextSort(current: string, key: string): string {
  const sort = parseSort(current);
  if (!sort || sort.key !== key) return key;
  if (!sort.desc) return `-${key}`;
  return ALL;
}

/** `aria-sort` for a column header under the current sort. */
export function ariaSort(
  current: string,
  key: string,
): "ascending" | "descending" | "none" {
  const sort = parseSort(current);
  if (!sort || sort.key !== key) return "none";
  return sort.desc ? "descending" : "ascending";
}

const UUID_RE =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

const RESULT_VALUES = new Set([
  "pass",
  "fail",
  "inconclusive",
  "excepted",
  "na",
]);

// Values the State pill offered before #2131. No evaluation ever carried
// them, so a saved view holding one matched nothing; it now asks for the
// state the label meant.
const LEGACY_RESULT: Record<string, string> = {
  insufficient_evidence: "inconclusive",
  not_applicable: "na",
};

const FRESHNESS_VALUES = new Set(["fresh", "stale", "expired", "no_evidence"]);
const ANCHOR_FIT_VALUES = new Set(["exact", "partial", "none"]);

/**
 * One filter value as the platform will accept it, or `ALL` when the
 * value is not one it allows. A hand-edited URL or an old saved view
 * therefore widens the list instead of failing the page with a 400.
 */
export function normalizeFilterValue(
  key: keyof ControlFilters,
  raw: string,
): string {
  if (!raw || raw === ALL) return ALL;
  switch (key) {
    case "result": {
      const v = LEGACY_RESULT[raw] ?? raw;
      return RESULT_VALUES.has(v) ? v : ALL;
    }
    case "freshness":
      return FRESHNESS_VALUES.has(raw) ? raw : ALL;
    case "anchor_fit":
      return ANCHOR_FIT_VALUES.has(raw) ? raw : ALL;
    case "scope":
      return UUID_RE.test(raw) ? raw : ALL;
    case "owner":
      return raw === "none" || UUID_RE.test(raw) ? raw : ALL;
    case "sort":
      return parseSort(raw) ? raw : ALL;
    default:
      return raw;
  }
}

/** The filter set with every value normalized. */
export function normalizeFilters(filters: ControlFilters): ControlFilters {
  const out = { ...filters };
  for (const k of FILTER_KEYS) out[k] = normalizeFilterValue(k, filters[k]);
  return out;
}

/** The 0-based row offset of a 1-indexed page. */
export function pageOffset(page: number, pageSize: number): number {
  return Math.max(0, Math.floor(page) - 1) * pageSize;
}

/**
 * The query for one page of "My controls": every active filter, the sort,
 * and the page. `ALL` values are left out, so the platform applies no
 * filter and its default order.
 */
export function myControlsQuery(
  filters: ControlFilters,
  page: number,
  pageSize: number,
): Record<string, string> {
  const out: Record<string, string> = {};
  const f = normalizeFilters(filters);
  for (const k of FILTER_KEYS) {
    if (f[k] !== ALL) out[k] = f[k];
  }
  out.limit = String(pageSize);
  out.offset = String(pageOffset(page, pageSize));
  return out;
}

/**
 * The last page that holds rows, when the requested page is past the end
 * (a stale bookmark, or a filter that shrank the set); null otherwise.
 */
export function clampedPage(
  page: number,
  pageSize: number,
  total: number,
): number | null {
  const last = Math.max(1, Math.ceil(total / pageSize));
  return page > last ? last : null;
}
