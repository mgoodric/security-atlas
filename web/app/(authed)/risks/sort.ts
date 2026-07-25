// Slice 681 / ATLAS-039 — pure column-sort logic for the /risks list.
//
// With 20+ rows the register had no sortable columns: RESIDUAL,
// INHERENT SEVERITY, and REVIEW-DUE headers were inert, so an operator
// could not triage "worst residual first" or "next review soonest".
// This module supplies the comparator + the asc/desc toggle math.
//
// Design (decisions log D1): CLIENT-SIDE sort. The page already filters
// and paginates the full `GET /v1/risks` list in-memory (slice 100 /
// 246); sorting joins that pattern rather than introducing a `?sort=`
// wire round-trip. The platform's `ParseListSort` (`internal/risk/
// store.go`) only exposes one ordering today (`residual,age`) and is
// consumed by the dashboard, not the list view — reusing it would mean
// extending the wire enum + a `-p 1` integration test for what the
// client already does for free over the in-memory array. Keeping it
// client-side is the smaller, pattern-consistent change.
//
// This module knows nothing about React — data in, data out — so the
// comparator is unit-tested without a DOM (the filters.ts convention).

import type { Risk } from "@/lib/api/risks";

import { residualState } from "./filters";

/** The three columns an operator triages on. */
export type SortKey = "residual" | "severity" | "review_due";

export type SortDir = "asc" | "desc";

export type SortState = {
  key: SortKey;
  dir: SortDir;
};

const SORT_KEYS: readonly SortKey[] = ["residual", "severity", "review_due"];

function isSortKey(v: string): v is SortKey {
  return (SORT_KEYS as readonly string[]).includes(v);
}

/**
 * Default ordering: worst inherent severity first. A security operator
 * opening the register wants the highest-exposure risks at the top, so
 * severity-descending is the sensible default (AC-1 "a sensible default
 * order"). The default is also the *unsorted-param* state, so the
 * canonical URL of the register carries no `sort` key until the user
 * clicks a header.
 */
export const DEFAULT_SORT: SortState = { key: "severity", dir: "desc" };

/**
 * Parse the `?sort=` URL value (shape `"<key>:<dir>"`) into a SortState.
 * An absent / malformed / unknown-key value falls back to DEFAULT_SORT;
 * an unknown direction falls back to `desc` (the worst-first triage
 * default). The page treats the DEFAULT_SORT as "no param" so the URL
 * stays clean until a non-default sort is chosen.
 */
export function parseSortState(raw: string | null | undefined): SortState {
  if (!raw) return DEFAULT_SORT;
  const [keyPart, dirPart] = raw.split(":", 2);
  if (!keyPart || !isSortKey(keyPart)) return DEFAULT_SORT;
  const dir: SortDir = dirPart === "asc" ? "asc" : "desc";
  return { key: keyPart, dir };
}

/** Serialize a SortState to the `"<key>:<dir>"` URL value. */
export function serializeSortState(state: SortState): string {
  return `${state.key}:${state.dir}`;
}

/**
 * Header-click toggle. Clicking a NEW column sorts it descending first
 * (worst / latest first — the triage default); clicking the ALREADY-
 * active column flips the direction. This matches the convention users
 * expect from a data grid: first click = "show me the extreme", second
 * click = "flip it".
 */
export function nextSortState(current: SortState, clicked: SortKey): SortState {
  if (current.key !== clicked) {
    return { key: clicked, dir: "desc" };
  }
  return { key: clicked, dir: current.dir === "desc" ? "asc" : "desc" };
}

/**
 * Residual magnitude on the 0..1 scale, or `null` for a row whose
 * residual is not yet evaluated (slice 680 "pending"). Mirrors
 * `formatResidualScore`'s math but returns a number for comparison.
 */
function residualMagnitude(score: unknown): number | null {
  if (residualState(score) !== "scored") return null;
  const s = score as { likelihood: number; impact: number };
  return (s.likelihood * s.impact) / 25;
}

/**
 * Review-due as a sortable epoch, or `null` when unset (pending). The
 * ISO date string is lexically sortable, but parsing to a timestamp is
 * robust to a date-only vs date-time mix on the wire.
 */
function reviewDueValue(reviewDueAt: string | undefined): number | null {
  if (reviewDueAt == null || reviewDueAt.trim() === "") return null;
  const t = Date.parse(reviewDueAt);
  return Number.isFinite(t) ? t : null;
}

/**
 * The comparison value for a row under a given key. A `null` means the
 * row is "pending" (un-scored / un-dated) and must sort to the END
 * regardless of direction — an operator triaging worst-first (desc) or
 * soonest-review (asc) should never have a brand-new, un-evaluated risk
 * jump ahead of evaluated ones.
 */
function valueFor(row: Risk, key: SortKey): number | null {
  switch (key) {
    case "severity":
      return row.severity;
    case "residual":
      return residualMagnitude(row.residual_score);
    case "review_due":
      return reviewDueValue(row.review_due_at);
  }
}

/**
 * Return a NEW array of `rows` ordered by `state`. Pure — the input is
 * never mutated (the page memoizes over `rows`, so an in-place sort
 * would corrupt the query cache).
 *
 * Ordering rules:
 *   - `null` (pending) values always sort last, in both directions.
 *   - present values compare numerically, flipped for `desc`.
 *   - ties (including two pending rows) break by `id` ascending so the
 *     order is fully deterministic across renders.
 */
export function sortRisks(rows: Risk[], state: SortState): Risk[] {
  const factor = state.dir === "asc" ? 1 : -1;
  return [...rows].sort((a, b) => {
    const va = valueFor(a, state.key);
    const vb = valueFor(b, state.key);

    // Pending (null) rows sink to the bottom regardless of direction.
    if (va === null && vb === null) return a.id.localeCompare(b.id);
    if (va === null) return 1;
    if (vb === null) return -1;

    if (va !== vb) return (va - vb) * factor;
    // Deterministic tie-break — id ascending, independent of `factor`.
    return a.id.localeCompare(b.id);
  });
}
