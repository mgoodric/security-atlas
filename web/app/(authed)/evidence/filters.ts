// Slice 099 + 106 + 234 — pure filter logic for the /evidence list view.
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
//
// Slice 234 changes:
//   * Three new filter axes (mockup parity, six pills total):
//     - `source`: combined connector|kind pill option set derived from
//       the observed `(source_actor_type, source_actor_id)` tuples in
//       the current result set. Selecting an option sets BOTH the
//       sourceActorType and sourceActorId URL params atomically.
//     - `scope`: a scope cell UUID (or `ALL`). Server-side intersection
//       via `scope_cell_id` (slice 224 pattern; out-of-tenant cells
//       return zero rows naturally under RLS).
//     - `since`: preset window key (`24h` / `7d` / `30d` / `audit`),
//       mapped to an RFC3339 cutoff client-side via `sinceCutoff`.

import type { Anchor } from "@/lib/api/anchors";
import type { ScopeCell } from "@/lib/api/controls-list";
import type { EvidenceResultEnum } from "@/lib/api/evidence";

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
  /**
   * Slice 234 — scope cell UUID narrowing, or `ALL` for no narrowing.
   * Server-side intersection via the new `scope_cell_id` query param;
   * the BFF forwards to upstream and the SQL predicate runs on
   * `evidence_records.scope_id`. Out-of-tenant cell ids return zero
   * rows naturally under RLS (no 404 leak).
   */
  scopeCellId: string;
  /**
   * Slice 234 — Since preset key. One of `SINCE_PRESET_KEYS` or `ALL`.
   * The page maps the key to an RFC3339 `since` cutoff via
   * `sinceCutoff` and forwards it on the wire. The "audit" preset
   * resolves to the active audit period's `period_start`; when no
   * active period exists the option still renders but the page reuses
   * the upstream default window (last 30 days).
   */
  since: string;
};

export const DEFAULT_FILTERS: EvidenceFilters = {
  controlId: NONE,
  kind: ALL,
  result: ALL,
  sourceActorType: ALL,
  sourceActorId: ALL,
  scopeCellId: ALL,
  since: ALL,
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
    filters.sourceActorId === ALL &&
    filters.scopeCellId === ALL &&
    filters.since === ALL
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

// ----- slice 234 -----

/**
 * A composite source-pill option value. The Source pill encodes BOTH
 * actor_type AND actor_id in a single URL-safe key (`type|id`); the
 * page splits the value at submit-time and sets the two URL params
 * atomically. The ALL sentinel still means "no narrowing on source".
 */
export const SOURCE_DELIM = "|";

/**
 * The provenance shape on the evidence wire — what we read to derive
 * the Source pill's observed `(actor_type, actor_id)` tuples. Matches
 * the slice-013 source_attribution JSONB shape (`{actor_type, actor_id,
 * session_id}`); we only look at the first two.
 */
export type EvidenceSource = {
  actor_type?: string;
  actor_id?: string;
};

/**
 * Build the Source pill option list from a set of distinct
 * `(actor_type, actor_id)` tuples observed in the current result set.
 * Pattern mirrors `buildKindOptions`: take what the wire emitted, no
 * invented values, dedupe + sort.
 *
 * Value shape: `<actor_type>|<actor_id>` (the composite key); label
 * shape: `<actor_type> · <actor_id>` (same as the table cell renderer
 * in `sourceSummary`). Rows missing either component are skipped.
 */
export function buildSourceOptions(
  sources: EvidenceSource[],
): { value: string; label: string }[] {
  const tuples = new Set<string>();
  for (const s of sources) {
    const t = typeof s.actor_type === "string" ? s.actor_type : "";
    const id = typeof s.actor_id === "string" ? s.actor_id : "";
    if (t && id) tuples.add(`${t}${SOURCE_DELIM}${id}`);
  }
  const sorted = Array.from(tuples).sort();
  return [
    { value: ALL, label: "All sources" },
    ...sorted.map((v) => {
      const [t, id] = v.split(SOURCE_DELIM);
      return { value: v, label: `${t} · ${id}` };
    }),
  ];
}

/**
 * Render a scope cell's display label using the slice-224 convention:
 * the cell's explicit `label` first; falling back to a deterministic
 * `k=v / k=v` summary of the `dimensions` JSONB (sorted by dimension
 * key); falling back to the cell UUID when the cell has neither.
 */
export function scopeCellLabel(cell: ScopeCell): string {
  if (cell.label) return cell.label;
  const dims = Object.entries(cell.dimensions ?? {})
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([k, v]) => `${k}=${v}`)
    .join(" / ");
  return dims || cell.id;
}

/**
 * Slice 234 — cap the Scope pill at 50 entries (slice 224 convention).
 * Tenants exceeding the cap surface a banner; the dropdown still works
 * for the first 50 cells (newest-first ordering from /v1/scopes/cells).
 * A typeahead replacement is deferred per the slice 224 decision log.
 */
export const SCOPE_CELL_CAP = 50;

/**
 * Build the Scope pill option list from the tenant's scope cells.
 * First option is ALL ("All cells"); the rest are at most
 * `SCOPE_CELL_CAP` cells in input order.
 */
export function buildScopeCellOptions(
  cells: ScopeCell[],
): { value: string; label: string }[] {
  const capped = cells.slice(0, SCOPE_CELL_CAP);
  return [
    { value: ALL, label: "All cells" },
    ...capped.map((c) => ({ value: c.id, label: scopeCellLabel(c) })),
  ];
}

/**
 * The Since pill preset keys. The page resolves the key to an RFC3339
 * cutoff via `sinceCutoff`; the BFF forwards the resolved `since` query
 * param to upstream. `audit` resolves to the active audit period's
 * `period_start` when one exists; otherwise the page reuses the
 * upstream default window (last 30 days).
 */
export const SINCE_PRESET_KEYS = ["24h", "7d", "30d", "audit"] as const;
export type SincePresetKey = (typeof SINCE_PRESET_KEYS)[number];

/**
 * Build the Since pill option list. First option is ALL ("All time"
 * — really the upstream default window, last 30 days; the label is
 * "All time" because honestly conveying "default window" in a single
 * dropdown label would be more confusing than the operator-friendly
 * "All time"). The "audit" option label adjusts to include the active
 * audit period name when one is supplied.
 */
export function buildSinceOptions(
  activeAuditPeriodName?: string,
): { value: string; label: string }[] {
  const auditLabel = activeAuditPeriodName
    ? `Audit period (${activeAuditPeriodName})`
    : "Audit period (current)";
  return [
    { value: ALL, label: "All time" },
    { value: "24h", label: "Last 24 hours" },
    { value: "7d", label: "Last 7 days" },
    { value: "30d", label: "Last 30 days" },
    { value: "audit", label: auditLabel },
  ];
}

/**
 * Map a Since preset key to an RFC3339 cutoff timestamp. Pure: takes
 * a `now` clock parameter for testability. The "audit" preset needs an
 * external `auditPeriodStart` (RFC3339); when absent it returns
 * undefined and the caller treats the filter as "no override" (the
 * upstream default window applies).
 *
 * Returns undefined when the input is not a valid preset key, so the
 * caller can fall through to "no narrowing".
 */
export function sinceCutoff(
  key: string,
  now: Date,
  auditPeriodStart?: string,
): string | undefined {
  if (key === "24h") {
    return new Date(now.getTime() - 24 * 60 * 60 * 1000).toISOString();
  }
  if (key === "7d") {
    return new Date(now.getTime() - 7 * 24 * 60 * 60 * 1000).toISOString();
  }
  if (key === "30d") {
    return new Date(now.getTime() - 30 * 24 * 60 * 60 * 1000).toISOString();
  }
  if (key === "audit") {
    return auditPeriodStart;
  }
  return undefined;
}

/**
 * Translate an EvidenceFilters into the shape `fetchEvidenceList`
 * expects (omit ALL/NONE sentinel values; pass only the narrowing
 * predicates). Slice 234 added `scopeCellID` and `since`.
 *
 * `since` is supplied externally because the EvidenceFilters carries a
 * preset *key* (`24h` / `7d` / `30d` / `audit`) rather than a resolved
 * timestamp — the resolution depends on `now` and (for `audit`) on the
 * active audit period's `period_start`, which the page reads from a
 * separate query.
 */
export function toFetchOptions(
  filters: EvidenceFilters,
  resolvedSince?: string,
): {
  controlID?: string;
  kind?: string;
  result?: EvidenceResultEnum;
  sourceActorType?: string;
  sourceActorID?: string;
  scopeCellID?: string;
  since?: string;
} {
  const out: {
    controlID?: string;
    kind?: string;
    result?: EvidenceResultEnum;
    sourceActorType?: string;
    sourceActorID?: string;
    scopeCellID?: string;
    since?: string;
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
  if (filters.scopeCellId !== ALL) {
    out.scopeCellID = filters.scopeCellId;
  }
  if (filters.since !== ALL && resolvedSince) {
    out.since = resolvedSince;
  }
  return out;
}
