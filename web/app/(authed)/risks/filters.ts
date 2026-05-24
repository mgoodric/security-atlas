// Slice 100 — pure filter + formatter logic for the /risks list view.
//
// All filter-related calculations + per-row formatting live here as
// pure functions so they can be vitest-unit-tested without spinning up
// React. The page imports these and applies them to the fetched
// `riskWire` rows.
//
// Constitutional commitment: this module knows nothing about React,
// useSearchParams, or the BFF. It is data-in, data-out.
//
// Slice 100 shipped 3 pills (treatment + severity band + owner). Slice
// 244 extended this to 6 pills by adding category + methodology +
// org_unit — the three mockup pills slice 100 deferred (see the
// "future extension" note that lived here before this slice). The data
// for all three was already on `riskWire` (slice 019 carried category +
// methodology; slice 067 carried org_unit_id), so the cost was purely
// UI plumbing.
//
// Constitutional anti-criterion P0-A3 (slice 100) honored: only fields
// that exist on `riskWire` (slice 019 + slice 067 additions) are
// referenced — no invented columns.

import type { Risk } from "@/lib/api";

/**
 * The "all values" sentinel. Used as the default filter value on every
 * pill — selecting it disables that filter. The literal string "all"
 * round-trips cleanly through the URL query string.
 */
export const ALL = "all" as const;

/**
 * Severity bands per the slice-067 5x5 severity scalar (likelihood ×
 * impact, range 0..25). Boundaries chosen to mirror the mockup's
 * rose/amber/emerald color tiers:
 *
 *   high   = severity >= 15   (rose)
 *   medium = 8..14            (amber)
 *   low    = 1..7             (emerald)
 *   none   = 0                (no numeric score on inherent_score)
 *
 * `none` is bucketed separately so a risk with a malformed score (or a
 * FAIR risk with no L×I component) is not silently lumped into "low".
 * The pill exposes 4 explicit values; the user picks one. The default
 * `ALL` returns every row regardless of severity.
 */
export type SeverityBand = "high" | "medium" | "low" | "none";

export function severityBand(severity: number): SeverityBand {
  if (severity >= 15) return "high";
  if (severity >= 8) return "medium";
  if (severity >= 1) return "low";
  return "none";
}

export type RiskFilters = {
  treatment: string;
  severity: string;
  owner: string;
  // Slice 244 additions.
  category: string;
  methodology: string;
  org_unit: string;
};

export const DEFAULT_FILTERS: RiskFilters = {
  treatment: ALL,
  severity: ALL,
  owner: ALL,
  // Slice 244 additions — all default to ALL so the pill row starts
  // unfiltered and only narrows when the user picks a specific value.
  category: ALL,
  methodology: ALL,
  org_unit: ALL,
};

/**
 * True when no filter is narrowing the result set.
 */
export function isDefault(filters: RiskFilters): boolean {
  return (
    filters.treatment === ALL &&
    filters.severity === ALL &&
    filters.owner === ALL &&
    filters.category === ALL &&
    filters.methodology === ALL &&
    filters.org_unit === ALL
  );
}

/**
 * Narrow a risk list against the active filter set. The treatment +
 * owner + category + methodology + org_unit filters compare the exact
 * string from `riskWire`; the severity filter buckets the numeric
 * `severity` scalar into the four bands above.
 *
 * Unassigned-owner rows match `owner = "unassigned"` (the sentinel the
 * mockup uses) — the wire shape carries an empty string for an unset
 * treatment_owner, so the filter normalizes both shapes.
 *
 * Slice 244 — three new branches:
 *   - category: exact match against the wire enum (`risk_category`).
 *   - methodology: exact match against the wire enum
 *     (`risk_methodology`).
 *   - org_unit: exact match against the row's `org_unit_id` (UUID).
 *     Rows with no `org_unit_id` never match a specific filter value —
 *     a row with `org_unit_id = undefined` only passes when the filter
 *     is `ALL` (i.e., disabled). This is the same shape as how the
 *     other identity-bearing pills (owner) treat unset values.
 */
export function applyFilters(rows: Risk[], filters: RiskFilters): Risk[] {
  return rows.filter((row) => {
    if (filters.treatment !== ALL && row.treatment !== filters.treatment) {
      return false;
    }
    if (filters.severity !== ALL) {
      if (severityBand(row.severity) !== filters.severity) return false;
    }
    if (filters.owner !== ALL) {
      const ownerNorm = row.treatment_owner.trim() || "unassigned";
      if (ownerNorm !== filters.owner) return false;
    }
    if (filters.category !== ALL && row.category !== filters.category) {
      return false;
    }
    if (
      filters.methodology !== ALL &&
      row.methodology !== filters.methodology
    ) {
      return false;
    }
    if (filters.org_unit !== ALL && row.org_unit_id !== filters.org_unit) {
      return false;
    }
    return true;
  });
}

/**
 * Extract the unique owner set from a row list. Used to drive the
 * "Owner" pill options. Sorted alphabetically with the "unassigned"
 * sentinel pinned last so it stays visually distinct from real names.
 */
export function uniqueOwners(rows: Risk[]): string[] {
  const seen = new Set<string>();
  let hasUnassigned = false;
  for (const r of rows) {
    const norm = r.treatment_owner.trim();
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
  filters: RiskFilters,
  key: keyof RiskFilters,
  value: string,
): RiskFilters {
  return { ...filters, [key]: value };
}

/**
 * Clear all filters back to the default.
 */
export function clearFilters(): RiskFilters {
  return { ...DEFAULT_FILTERS };
}

// ----- formatters -----

/**
 * Format a `residual_score` JSONB blob into the mockup's display string.
 *
 * `residual_score` follows the canvas §2.2 5x5 shape: `{likelihood,
 * impact}` numerics. The display value mirrors the platform's
 * `residualMagnitude` (internal/risk/store.go): `likelihood × impact`
 * normalized against the 5x5 ceiling (25) so the rendered scalar is in
 * `0..1`. A malformed score (missing either component, non-JSON, or a
 * FAIR-shaped score with no L×I) renders as `"—"` — the same
 * graceful-degradation posture as the residual-magnitude sort.
 *
 * Returned as a string so the cell renderer can drop it straight into
 * the table without re-formatting.
 */
export function formatResidualScore(score: unknown): string {
  if (score == null || typeof score !== "object") return "—";
  const s = score as { likelihood?: unknown; impact?: unknown };
  const l = typeof s.likelihood === "number" ? s.likelihood : null;
  const i = typeof s.impact === "number" ? s.impact : null;
  if (l == null || i == null) return "—";
  const normalized = (l * i) / 25;
  if (!Number.isFinite(normalized)) return "—";
  return normalized.toFixed(2);
}

/**
 * Map a severity band to a Tailwind color class set for the pill in
 * the severity column. Centralised so the rose/amber/emerald palette
 * matches the mockup exactly.
 */
export function severityClasses(band: SeverityBand): string {
  switch (band) {
    case "high":
      return "bg-rose-100 text-rose-700";
    case "medium":
      return "bg-amber-100 text-amber-700";
    case "low":
      return "bg-emerald-100 text-emerald-700";
    case "none":
    default:
      return "bg-muted text-muted-foreground";
  }
}

/**
 * Map a residual numeric (the formatted string parsed back) to its
 * Tailwind color class — rose for the high band, amber for medium,
 * emerald for low, neutral for unparseable. Mirrors the mockup palette.
 */
export function residualClass(formatted: string): string {
  if (formatted === "—") return "text-muted-foreground";
  const n = parseFloat(formatted);
  if (!Number.isFinite(n)) return "text-muted-foreground";
  if (n >= 0.6) return "text-rose-700 font-semibold";
  if (n >= 0.32) return "text-amber-700 font-semibold";
  return "text-emerald-700 font-semibold";
}
