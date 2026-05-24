// Slice 101 — pure filter logic for the /policies list view.
//
// Mirrors the slice 100 risks filter shape: pure functions, no React,
// vitest-unit-testable without spinning up a tree.
//
// Filter set per the policies.html mockup §filter row (lines 124-166):
// status + owner_role + ack_status (added in slice 238 once the joined
// `ack_rate` cell shipped in slice 107). One mockup pill — `Linked
// control` — remains deferred per slice 238 AC-4 because the list
// endpoint does not yet surface a `linked_controls` field on each
// row; a follow-on slice files the wire extension + multi-select
// pill together.
//
// Slice 238 — `ack_status` band predicate.
//
//   The mockup at `Plans/mockups/policies.html` lines 154-165 names
//   four bands: `All` / `>= 95% acknowledged` / `< 95% acknowledged`
//   / `< 50% acknowledged`. The thresholds match the slice 101
//   `ackRateBand` SOC 2 CC1.4 norm (`./ack-rate.ts`).
//
//   Null-rate handling (AC-2): rows with `ack_rate: null` (non-
//   published) or `ack_rate.percent: null` (no required-role users)
//   are excluded from the `ge95` / `lt95` / `lt50` selections and
//   included only when the pill is at the default `ALL` value.
//
//   The band values are URL-safe short strings (`ge95`/`lt95`/`lt50`)
//   so `?ack_status=ge95` etc. is bookmarkable (AC-3).
//
// Constitutional anti-criterion P0-A3 honored: only fields that exist
// on `policyWire` are referenced — no invented columns.

import type { Policy } from "@/lib/api";

/**
 * The "all values" sentinel. Used as the default filter value on every
 * pill — selecting it disables that filter. The literal string "all"
 * round-trips cleanly through the URL query string.
 */
export const ALL = "all" as const;

/**
 * URL-safe band identifiers for the `ack_status` pill. Slice 238 — the
 * bands match the mockup at `Plans/mockups/policies.html` lines 154-
 * 165 and the SOC 2 CC1.4 thresholds in `./ack-rate.ts`.
 *
 * - `ALL` (default): no narrowing — every row is included.
 * - `GE95`: `ack_rate.percent >= 95`. Non-null cells only.
 * - `LT95`: `ack_rate.percent < 95`. Non-null cells only.
 * - `LT50`: `ack_rate.percent < 50`. Non-null cells only.
 *
 * Rows with `ack_rate: null` or `ack_rate.percent: null` are excluded
 * from the non-`ALL` selections (AC-2). This is the correct behavior
 * because a band claim against unknown data would be misleading.
 */
export const ACK_STATUS_GE_95 = "ge95" as const;
export const ACK_STATUS_LT_95 = "lt95" as const;
export const ACK_STATUS_LT_50 = "lt50" as const;

export type AckStatusBand =
  | typeof ALL
  | typeof ACK_STATUS_GE_95
  | typeof ACK_STATUS_LT_95
  | typeof ACK_STATUS_LT_50;

export type PolicyFilters = {
  status: string;
  owner_role: string;
  ack_status: string;
};

export const DEFAULT_FILTERS: PolicyFilters = {
  status: ALL,
  owner_role: ALL,
  ack_status: ALL,
};

/**
 * True when no filter is narrowing the result set.
 */
export function isDefault(filters: PolicyFilters): boolean {
  return (
    filters.status === ALL &&
    filters.owner_role === ALL &&
    filters.ack_status === ALL
  );
}

/**
 * Pure predicate for the `ack_status` band. Returns true when the row
 * should remain visible under the active band. Null-rate rows
 * (non-published policies, zero-denominator cells) are excluded from
 * every non-`ALL` band.
 *
 * Exported separately from `applyFilters` so vitest can pin the band
 * semantics independent of the row-level intersection.
 */
export function ackStatusMatches(row: Policy, band: string): boolean {
  if (band === ALL) return true;
  const pct = row.ack_rate?.percent;
  if (pct == null || !Number.isFinite(pct)) {
    // AC-2: null-rate rows are filtered OUT for every non-ALL band.
    return false;
  }
  switch (band) {
    case ACK_STATUS_GE_95:
      return pct >= 95;
    case ACK_STATUS_LT_95:
      return pct < 95;
    case ACK_STATUS_LT_50:
      return pct < 50;
    default:
      // Unknown band value (e.g. stale URL from an older deploy) —
      // treat as ALL so the page stays usable rather than blanking.
      return true;
  }
}

/**
 * Narrow a policy list against the active filter set. The three
 * filters intersect.
 *
 * Unassigned-owner rows match `owner_role = "unassigned"` (the
 * sentinel for an empty wire value). policyWire.owner_role is required
 * on create (handlers.go writeCreateErr ErrOwnerRoleRequired), but the
 * filter still normalizes for forward-compat.
 */
export function applyFilters(rows: Policy[], filters: PolicyFilters): Policy[] {
  return rows.filter((row) => {
    if (filters.status !== ALL && row.status !== filters.status) {
      return false;
    }
    if (filters.owner_role !== ALL) {
      const ownerNorm = row.owner_role.trim() || "unassigned";
      if (ownerNorm !== filters.owner_role) return false;
    }
    if (!ackStatusMatches(row, filters.ack_status)) {
      return false;
    }
    return true;
  });
}

/**
 * Extract the unique owner_role set from a row list. Drives the
 * "Owner role" pill options. Sorted alphabetically with the
 * "unassigned" sentinel pinned last so it stays visually distinct from
 * real role names.
 */
export function uniqueOwners(rows: Policy[]): string[] {
  const seen = new Set<string>();
  let hasUnassigned = false;
  for (const r of rows) {
    const norm = r.owner_role.trim();
    if (norm === "") {
      hasUnassigned = true;
    } else {
      seen.add(norm);
    }
  }
  const named = Array.from(seen).sort();
  return hasUnassigned ? [...named, "unassigned"] : named;
}

/**
 * Merge a partial filter update onto the existing filter set.
 */
export function setFilter(
  filters: PolicyFilters,
  key: keyof PolicyFilters,
  value: string,
): PolicyFilters {
  return { ...filters, [key]: value };
}

/**
 * Clear all filters back to the default.
 */
export function clearFilters(): PolicyFilters {
  return { ...DEFAULT_FILTERS };
}
