// Slice 101 — pure filter logic for the /policies list view.
//
// Mirrors the slice 100 risks filter shape: pure functions, no React,
// vitest-unit-testable without spinning up a tree.
//
// Filter set per the policies.html mockup §filter row (lines 124-166):
// status + owner_role. Two additional pills shown in the mockup —
// `Linked control` and `Ack status` — are deferred per slice 101 D2:
//
//   * Linked control: the wire shape has `linked_control_ids` (array
//     of UUIDs); rendering a usable pill requires joining against
//     control titles via `/v1/controls/{id}` which is not on the list
//     wire. Spillover candidate.
//   * Ack status: requires the `?include=ack_rate` extension that does
//     NOT exist on main (spillover slice 107). Until then there is no
//     data to filter against — exposing the pill would be misleading.
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

export type PolicyFilters = {
  status: string;
  owner_role: string;
};

export const DEFAULT_FILTERS: PolicyFilters = {
  status: ALL,
  owner_role: ALL,
};

/**
 * True when no filter is narrowing the result set.
 */
export function isDefault(filters: PolicyFilters): boolean {
  return filters.status === ALL && filters.owner_role === ALL;
}

/**
 * Narrow a policy list against the active filter set. Both filters
 * compare the exact string from `policyWire`.
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
