// Slice 177 — pure filter logic for the /exceptions list view.
//
// Mirrors the slice 098 controls / slice 101 policies filter shape: pure
// functions, no React, vitest-unit-testable without spinning up a tree.
//
// Filter set per the slice 177 ACs (AC-2):
//   - status (requested / approved / denied / active / expired)
//   - control_id (when arriving from a control detail page deep-link)
//
// Both filters compare exact strings from `exceptionWire`. P0-A-176-2
// honored: no invented columns — only `status` and `control_id` from
// the wire are referenced.

import type { Exception, ExceptionStatus } from "@/lib/api/exceptions";

/**
 * The "all values" sentinel. Used as the default filter value on every
 * pill — selecting it disables that filter. The literal string "all"
 * round-trips cleanly through the URL query string.
 */
export const ALL = "all" as const;

export type ExceptionFilters = {
  status: string;
  control_id: string;
};

export const DEFAULT_FILTERS: ExceptionFilters = {
  status: ALL,
  control_id: ALL,
};

/**
 * True when no filter is narrowing the result set.
 */
export function isDefault(filters: ExceptionFilters): boolean {
  return filters.status === ALL && filters.control_id === ALL;
}

/**
 * Narrow an exception list against the active filter set. Both filters
 * compare the exact string from `exceptionWire`.
 *
 * Status values come from `internal/exception/store.go` StateRequested /
 * StateApproved / StateDenied / StateActive / StateExpired. Control IDs
 * are UUIDs.
 */
export function applyFilters(
  rows: Exception[],
  filters: ExceptionFilters,
): Exception[] {
  return rows.filter((row) => {
    if (filters.status !== ALL && row.status !== filters.status) {
      return false;
    }
    if (filters.control_id !== ALL && row.control_id !== filters.control_id) {
      return false;
    }
    return true;
  });
}

/**
 * Extract the unique control_id set from a row list. Drives the
 * "Control" pill options. Sorted alphabetically for deterministic
 * ordering in the pill dropdown.
 */
export function uniqueControlIDs(rows: Exception[]): string[] {
  const seen = new Set<string>();
  for (const r of rows) {
    if (r.control_id) seen.add(r.control_id);
  }
  return Array.from(seen).sort();
}

/**
 * Merge a partial filter update onto the existing filter set.
 */
export function setFilter(
  filters: ExceptionFilters,
  key: keyof ExceptionFilters,
  value: string,
): ExceptionFilters {
  return { ...filters, [key]: value };
}

/**
 * Clear all filters back to the default.
 */
export function clearFilters(): ExceptionFilters {
  return { ...DEFAULT_FILTERS };
}

/**
 * Translate the page-local filter shape into the
 * `ExceptionsListFilters` shape the BFF expects. Sentinel ALL values
 * become absent params; concrete values map through. Exported so the
 * page can pass the result into `fetchExceptionsList` directly.
 */
export function toFetchOptions(filters: ExceptionFilters): {
  status?: ExceptionStatus;
  controlId?: string;
} {
  const out: { status?: ExceptionStatus; controlId?: string } = {};
  if (filters.status !== ALL) {
    out.status = filters.status as ExceptionStatus;
  }
  if (filters.control_id !== ALL) {
    out.controlId = filters.control_id;
  }
  return out;
}
