// #2132 — the register asks the server for one filtered, sorted page
// instead of loading every risk and narrowing it in the browser. This
// module is the pure half of that: it checks the URL's filter values,
// builds the query the page sends, and names the register's columns.
//
// No React, no fetch. Data-in, data-out, vitest-pinned.

import { ALL } from "./filters";
import { sortParam, parseSortState } from "./sort";

export { ALL };

/**
 * The register's filters. Each key is the platform parameter of the same
 * name, so the URL is the request.
 */
export const FILTER_KEYS = [
  "status",
  "owner",
  "treatment",
  "category",
  "review_due",
  "methodology",
  "org_unit",
  "review_due_from",
  "review_due_to",
] as const;

export type FilterKey = (typeof FILTER_KEYS)[number];

export type RegisterFilters = Record<FilterKey, string>;

/**
 * Status is the one filter whose default is not "all": the register shows
 * open risks unless asked for closed ones (#2107). "open" is also the
 * platform's default, so it is never sent.
 */
export const DEFAULT_STATUS = "open";

export const DEFAULT_FILTERS: RegisterFilters = {
  status: DEFAULT_STATUS,
  owner: ALL,
  treatment: ALL,
  category: ALL,
  review_due: ALL,
  methodology: ALL,
  org_unit: ALL,
  review_due_from: ALL,
  review_due_to: ALL,
};

const UUID_RE =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const DATE_RE = /^\d{4}-\d{2}-\d{2}$/;
const SLUG_RE = /^[a-z0-9][a-z0-9_-]{0,62}$/;

const STATUS_VALUES = new Set(["open", "closed", ALL]);
const TREATMENT_VALUES = new Set([
  "accept",
  "mitigate",
  "transfer",
  "avoid",
  "untreated",
]);
const METHODOLOGY_VALUES = new Set([
  "nist_800_30",
  "fair",
  "cis_ram",
  "iso_27005",
  "qualitative_5x5",
]);
const REVIEW_DUE_VALUES = new Set(["overdue", "due_soon"]);

function validDate(raw: string): boolean {
  if (!DATE_RE.test(raw)) return false;
  const d = new Date(`${raw}T00:00:00Z`);
  return !Number.isNaN(d.getTime()) && d.toISOString().slice(0, 10) === raw;
}

/**
 * One filter value as the platform will accept it, or the default when
 * it is not one it allows. A hand-edited URL therefore widens the list
 * instead of failing the page with a 400.
 */
export function normalizeFilterValue(key: FilterKey, raw: string): string {
  const fallback = DEFAULT_FILTERS[key];
  if (!raw) return fallback;
  switch (key) {
    case "status":
      return STATUS_VALUES.has(raw) ? raw : fallback;
    case "owner":
      return raw === "none" || UUID_RE.test(raw) ? raw : fallback;
    case "treatment":
      return TREATMENT_VALUES.has(raw) ? raw : fallback;
    case "methodology":
      return METHODOLOGY_VALUES.has(raw) ? raw : fallback;
    case "review_due":
      return REVIEW_DUE_VALUES.has(raw) ? raw : fallback;
    case "category":
      return SLUG_RE.test(raw) || UUID_RE.test(raw) ? raw : fallback;
    case "org_unit":
      return UUID_RE.test(raw) ? raw : fallback;
    case "review_due_from":
    case "review_due_to":
      return validDate(raw) ? raw : fallback;
  }
}

/** The filters a URL names, every value normalized. */
export function filtersFromParams(params: {
  get(key: string): string | null;
}): RegisterFilters {
  const out = { ...DEFAULT_FILTERS };
  for (const k of FILTER_KEYS) {
    out[k] = normalizeFilterValue(k, params.get(k) ?? "");
  }
  return out;
}

/** True when no filter narrows the register beyond its default. */
export function isDefaultFilters(filters: RegisterFilters): boolean {
  return FILTER_KEYS.every((k) => filters[k] === DEFAULT_FILTERS[k]);
}

/** The 0-based row offset of a 1-indexed page. */
export function pageOffset(page: number, pageSize: number): number {
  return Math.max(0, Math.floor(page) - 1) * pageSize;
}

/**
 * The query for one page of the register: every filter that differs from
 * its default, the sort, the roll-ups toggle and the page. A default is
 * left out, so the platform applies its own (open risks, residual
 * descending).
 */
export function registerQuery(opts: {
  filters: RegisterFilters;
  sort: string | null;
  rollups: boolean;
  page: number;
  pageSize: number;
}): Record<string, string> {
  const out: Record<string, string> = {};
  for (const k of FILTER_KEYS) {
    const v = normalizeFilterValue(k, opts.filters[k]);
    if (v !== DEFAULT_FILTERS[k]) out[k] = v;
  }
  const sort = sortParam(parseSortState(opts.sort));
  if (sort) out.sort = sort;
  if (opts.rollups) out.include_rollups = "true";
  out.limit = String(opts.pageSize);
  out.offset = String(pageOffset(opts.page, opts.pageSize));
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

// ----- columns -----

/** The register's default columns, in order (#2132). */
export const DEFAULT_COLUMNS = [
  "id",
  "title",
  "category",
  "owner",
  "residual",
  "treatment",
  "review_due",
  "status",
] as const;

/** The columns the chooser adds, in the order they append. */
export const OPTIONAL_COLUMNS = [
  "methodology",
  "level",
  "org_unit",
  "themes",
  "created_at",
] as const;

export type OptionalColumn = (typeof OPTIONAL_COLUMNS)[number];

export const OPTIONAL_COLUMN_LABELS: Record<OptionalColumn, string> = {
  methodology: "Methodology",
  level: "Level",
  org_unit: "Org unit",
  themes: "Themes",
  created_at: "Created",
};

/** URL key for the chosen optional columns, comma-separated. */
export const COLUMNS_PARAM = "cols";

/** The optional columns a `cols` value names, in chooser order. */
export function parseColumns(raw: string | null | undefined): OptionalColumn[] {
  const want = new Set((raw ?? "").split(",").map((s) => s.trim()));
  return OPTIONAL_COLUMNS.filter((c) => want.has(c));
}

/** The `cols` value after the chooser toggles one column ("" for none). */
export function toggleColumn(
  current: OptionalColumn[],
  column: OptionalColumn,
): string {
  const next = current.includes(column)
    ? current.filter((c) => c !== column)
    : [...current, column];
  return OPTIONAL_COLUMNS.filter((c) => next.includes(c)).join(",");
}

/**
 * The short form of a risk id the ID column shows: its first eight hex
 * digits. The full id is the row's link and tooltip.
 */
export function shortRiskId(id: string): string {
  return id.replace(/-/g, "").slice(0, 8);
}
