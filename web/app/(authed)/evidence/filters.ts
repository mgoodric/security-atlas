// Slice 099 — pure filter logic for the /evidence list view.
//
// All filter-related calculations live here as pure functions so they
// can be vitest-unit-tested without spinning up React. The page imports
// these and applies them to the filtered evidence list.
//
// Constitutional commitment: this module knows nothing about React,
// useSearchParams, or the BFF. It is data-in, data-out.

import type { Anchor } from "@/lib/api";

/**
 * The "all values" sentinel. Used as the default filter value on the
 * pills that aren't currently driving narrowing. For the control pill
 * specifically, the sentinel doubles as "no control selected" — and
 * the page treats `NONE` distinct from `ALL` for the data-fetch gate
 * (we cannot fetch without a control_id; see slice 099 D2).
 */
export const ALL = "all" as const;
export const NONE = "" as const;

export type EvidenceFilters = {
  /** Selected control anchor id (UUID). `NONE` means no control selected
   *  yet — the page renders the "pick a control" prompt instead of
   *  fetching. */
  controlId: string;
};

export const DEFAULT_FILTERS: EvidenceFilters = {
  controlId: NONE,
};

/**
 * True when no control is selected. The page renders the "pick a
 * control" prompt in this case (data fetch is gated on a real id).
 */
export function isNoneSelected(filters: EvidenceFilters): boolean {
  return filters.controlId === NONE;
}

/**
 * True when no filter is narrowing the result set beyond the bare
 * control selection. Used to decide whether to surface the "Clear
 * filters" CTA on the empty state.
 */
export function isDefault(filters: EvidenceFilters): boolean {
  return filters.controlId === NONE;
}

/**
 * Merge a partial filter update onto the existing filter set.
 */
export function setFilter(
  filters: EvidenceFilters,
  key: keyof EvidenceFilters,
  value: string,
): EvidenceFilters {
  return { ...filters, [key]: value };
}

/**
 * Clear all filters back to the default (no control selected).
 */
export function clearFilters(): EvidenceFilters {
  return { ...DEFAULT_FILTERS };
}

/**
 * Build the control-pill option list from the fetched anchor catalog.
 * The first option is the "pick a control" sentinel (NONE); the rest
 * are anchors sorted by scf_id for stable ordering.
 */
export function buildControlOptions(
  anchors: Anchor[],
): { value: string; label: string }[] {
  const sorted = [...anchors].sort((a, b) => a.scf_id.localeCompare(b.scf_id));
  return [
    { value: NONE, label: "Select a control…" },
    ...sorted.map((a) => ({
      value: a.id,
      label: `${a.scf_id} · ${a.name}`,
    })),
  ];
}
