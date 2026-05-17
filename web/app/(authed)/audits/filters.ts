// Slice 102 — pure filter logic for the /audits list view.
//
// All filter-related calculations live here as pure functions so they
// can be vitest-unit-tested without React. The page imports these and
// applies them to the fetched periodWire rows.
//
// Constitutional commitment: this module knows nothing about React,
// useSearchParams, or the BFF. It is data-in, data-out.

import type { AuditPeriod } from "@/lib/api";

/**
 * "All values" sentinel. Default value for every filter pill; selecting
 * it disables that filter. The literal string "all" round-trips cleanly
 * through the URL query string.
 */
export const ALL = "all" as const;

export type AuditFilters = {
  framework: string;
  status: string;
  year: string;
};

export const DEFAULT_FILTERS: AuditFilters = {
  framework: ALL,
  status: ALL,
  year: ALL,
};

export function isDefault(filters: AuditFilters): boolean {
  return (
    filters.framework === ALL && filters.status === ALL && filters.year === ALL
  );
}

export function clearFilters(): AuditFilters {
  return { ...DEFAULT_FILTERS };
}

export function setFilter(
  filters: AuditFilters,
  key: keyof AuditFilters,
  value: string,
): AuditFilters {
  return { ...filters, [key]: value };
}

/**
 * Extract the year of `period_start` for the Year pill. We use the ISO
 * date prefix YYYY directly — no Date parsing, so we don't depend on
 * the runtime timezone. periodWire serializes period_start as RFC3339,
 * which always begins with YYYY.
 */
export function yearOf(period: AuditPeriod): string {
  return period.period_start.slice(0, 4);
}

/**
 * Sorted descending unique year set from a period list. Used to drive
 * the Year pill options. Newest year first because the user most often
 * wants the current period.
 */
export function uniqueYears(periods: AuditPeriod[]): string[] {
  const seen = new Set<string>();
  for (const p of periods) {
    const y = yearOf(p);
    if (y) seen.add(y);
  }
  return Array.from(seen).sort((a, b) => b.localeCompare(a));
}

/**
 * Narrow a period list against the active filter set.
 *
 * Framework filter:
 *   periodWire has no friendly framework label — only `framework_version_id`
 *   (a UUID). When a framework label endpoint lands (spillover candidate),
 *   the page will map UUID -> framework name client-side and this filter
 *   becomes meaningful. Until then, the framework pill is a no-op (it
 *   still renders so the UI shape stays stable across slices, matching
 *   the slice-098 controls framework pill pattern).
 *
 * Status filter:
 *   Direct string compare against periodWire.status. The platform's
 *   audit_periods.status CHECK constraint allows {'open','frozen'} in
 *   v1. The status pill options enumerate the broader forward-looking
 *   set per the design doc; missing values just filter to zero rows
 *   today, which is correct behavior.
 *
 * Year filter:
 *   Matches the period_start's YYYY prefix.
 */
export function applyFilters(
  periods: AuditPeriod[],
  filters: AuditFilters,
): AuditPeriod[] {
  return periods.filter((p) => {
    if (filters.status !== ALL && p.status !== filters.status) {
      return false;
    }
    if (filters.year !== ALL && yearOf(p) !== filters.year) {
      return false;
    }
    // framework filter is a no-op for v1 (no framework label endpoint).
    return true;
  });
}
