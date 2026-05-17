// Slice 099 + 106 — pure filter logic for the /evidence list view.
//
// All filter-related calculations live here as pure functions so they
// can be vitest-unit-tested without spinning up React. The page imports
// these and applies them to the filtered evidence list.
//
// Constitutional commitment: this module knows nothing about React,
// useSearchParams, or the BFF. It is data-in, data-out.
//
// Slice 106 changes:
//   * The list now defaults to TENANT-WIDE — no control needs to be
//     selected first (the upstream `GET /v1/evidence` now serves the
//     full ledger window when control_id is absent). `controlId === NONE`
//     is the canonical "no control filter" state.
//   * Four new filter axes: `kind`, `result`, `sourceActorType`,
//     `sourceActorId`. Each uses the `ALL` sentinel to mean "no
//     narrowing on this axis" (consistent with the slice-098 shared
//     shell semantics).

import type { Anchor, EvidenceResultEnum } from "@/lib/api";

/**
 * The "all values" sentinel. Used as the default filter value on the
 * pills that aren't currently driving narrowing. For the control pill
 * specifically, the page treats `NONE` distinct from `ALL` for URL
 * state (NONE encodes as omitting the URL param entirely).
 */
export const ALL = "all" as const;
export const NONE = "" as const;

export type EvidenceFilters = {
  /** Selected control anchor id (UUID), or `NONE` for "no control filter"
   *  (tenant-wide list). Post-slice-106 the page is happy to render a
   *  tenant-wide list when `NONE`. */
  controlId: string;
  /** Evidence kind narrowing, or `ALL` for no narrowing. */
  kind: string;
  /** Evidence result enum narrowing, or `ALL` for no narrowing. */
  result: string;
  /** source_attribution.actor_type narrowing, or `ALL` for no narrowing. */
  sourceActorType: string;
  /** source_attribution.actor_id narrowing, or `ALL` for no narrowing. */
  sourceActorId: string;
};

export const DEFAULT_FILTERS: EvidenceFilters = {
  controlId: NONE,
  kind: ALL,
  result: ALL,
  sourceActorType: ALL,
  sourceActorId: ALL,
};

/**
 * Slice 099 legacy helper — kept for backwards compatibility with the
 * filters.test.ts coverage written at slice 099 time. Returns true when
 * no control is selected (i.e. the tenant-wide path is in play).
 */
export function isNoneSelected(filters: EvidenceFilters): boolean {
  return filters.controlId === NONE;
}

/**
 * True when no filter is narrowing the result set. Used to decide
 * whether to surface the "Clear filters" CTA on the empty state.
 */
export function isDefault(filters: EvidenceFilters): boolean {
  return (
    filters.controlId === NONE &&
    filters.kind === ALL &&
    filters.result === ALL &&
    filters.sourceActorType === ALL &&
    filters.sourceActorId === ALL
  );
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
 * Clear all filters back to the default (no narrowing on any axis).
 */
export function clearFilters(): EvidenceFilters {
  return { ...DEFAULT_FILTERS };
}

/**
 * Build the control-pill option list from the fetched anchor catalog.
 * Slice 106: the first option is "All controls" (NONE — meaning the
 * tenant-wide path), rather than the slice-099 "Select a control…"
 * gate. The rest are anchors sorted by scf_id for stable ordering.
 */
export function buildControlOptions(
  anchors: Anchor[],
): { value: string; label: string }[] {
  const sorted = [...anchors].sort((a, b) => a.scf_id.localeCompare(b.scf_id));
  return [
    { value: NONE, label: "All controls" },
    ...sorted.map((a) => ({
      value: a.id,
      label: `${a.scf_id} · ${a.name}`,
    })),
  ];
}

/**
 * The canonical evidence_result enum value set, in the order the
 * backend declares them. Used to build the Result filter pill.
 */
export const RESULT_VALUES: EvidenceResultEnum[] = [
  "pass",
  "fail",
  "na",
  "inconclusive",
];

/**
 * Build the result-pill option list. First option is ALL.
 */
export function buildResultOptions(): { value: string; label: string }[] {
  return [
    { value: ALL, label: "All results" },
    ...RESULT_VALUES.map((r) => ({ value: r, label: r })),
  ];
}

/**
 * Build the kind-pill option list from a set of distinct kinds observed
 * in the current page of evidence rows. The first option is ALL.
 * Returns kinds sorted alphabetically for stable ordering.
 */
export function buildKindOptions(
  kinds: string[],
): { value: string; label: string }[] {
  const uniq = Array.from(new Set(kinds.filter((k) => k && k.length > 0)));
  uniq.sort();
  return [
    { value: ALL, label: "All kinds" },
    ...uniq.map((k) => ({ value: k, label: k })),
  ];
}

/**
 * Translate an EvidenceFilters into the shape `fetchEvidenceList`
 * expects (omit ALL/NONE sentinel values; pass only the narrowing
 * predicates).
 */
export function toFetchOptions(filters: EvidenceFilters): {
  controlID?: string;
  kind?: string;
  result?: EvidenceResultEnum;
  sourceActorType?: string;
  sourceActorID?: string;
} {
  const out: {
    controlID?: string;
    kind?: string;
    result?: EvidenceResultEnum;
    sourceActorType?: string;
    sourceActorID?: string;
  } = {};
  if (filters.controlId !== NONE) out.controlID = filters.controlId;
  if (filters.kind !== ALL) out.kind = filters.kind;
  if (filters.result !== ALL) {
    out.result = filters.result as EvidenceResultEnum;
  }
  if (filters.sourceActorType !== ALL) {
    out.sourceActorType = filters.sourceActorType;
  }
  if (filters.sourceActorId !== ALL) {
    out.sourceActorID = filters.sourceActorId;
  }
  return out;
}
