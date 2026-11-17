// #2132 — the register's column sort, done on the server.
//
// Slice 681 sorted the loaded rows in the browser. The register is now
// paged, so the browser holds one page and cannot sort the set: the sort
// goes to `GET /v1/risks?sort=` instead. The URL `sort` value is that
// parameter verbatim (`key` ascending, `-key` descending), so a bookmark,
// a saved view and the request all say the same thing.
//
// This module knows nothing about React: data in, data out.

/**
 * The platform's sort keys, one per register column. The platform
 * allow-lists the same set (`internal/risk/list_page.go`); a key outside
 * it is a 400 there, so the page never sends one.
 */
export const SORT_KEYS = [
  "id",
  "title",
  "category",
  "owner",
  "residual",
  "treatment",
  "review_due",
  "status",
  "methodology",
  "level",
  "org_unit",
  "themes",
  "created_at",
] as const;

export type SortKey = (typeof SORT_KEYS)[number];

export type SortDir = "asc" | "desc";

export type SortState = {
  key: SortKey;
  dir: SortDir;
};

function isSortKey(v: string): v is SortKey {
  return (SORT_KEYS as readonly string[]).includes(v);
}

/**
 * The default order: residual, highest first. It is also the platform's
 * order with no `sort`, so the default sends no parameter and the
 * canonical register URL carries none.
 */
export const DEFAULT_SORT: SortState = { key: "residual", dir: "desc" };

/**
 * The sort a URL `sort` value names. An absent or unknown value (an old
 * `severity:asc` bookmark, a hand-edited URL) is the default, so the page
 * never sends the platform a key it refuses.
 */
export function parseSortState(raw: string | null | undefined): SortState {
  if (!raw) return DEFAULT_SORT;
  const desc = raw.startsWith("-");
  const key = desc ? raw.slice(1) : raw;
  if (!isSortKey(key)) return DEFAULT_SORT;
  return { key, dir: desc ? "desc" : "asc" };
}

/** The `sort` parameter for a state: "" for the default order. */
export function sortParam(state: SortState): string {
  if (state.key === DEFAULT_SORT.key && state.dir === DEFAULT_SORT.dir) {
    return "";
  }
  return state.dir === "desc" ? `-${state.key}` : state.key;
}

/**
 * Header-click toggle. A new column sorts descending first (highest,
 * latest, Z first); the active column flips direction.
 */
export function nextSortState(current: SortState, clicked: SortKey): SortState {
  if (current.key !== clicked) return { key: clicked, dir: "desc" };
  return { key: clicked, dir: current.dir === "desc" ? "asc" : "desc" };
}

/** `aria-sort` for a column header under the current sort. */
export function ariaSort(
  current: SortState,
  key: SortKey,
): "ascending" | "descending" | "none" {
  if (current.key !== key) return "none";
  return current.dir === "desc" ? "descending" : "ascending";
}
