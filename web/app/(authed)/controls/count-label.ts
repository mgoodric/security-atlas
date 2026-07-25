// Slice 666 — Controls list header count label.
//
// Fixes the header/footer count contradiction surfaced by the
// 2026-06-10 empty-tenant UI audit (ATLAS-007): the header read
// "Showing 53 of 53 SCF anchors" while the pagination footer read
// "Showing 1–50 of 53". Both used the verb "Showing" but meant
// different things — the header meant "filtered subset of catalog
// total", the footer meant "this page's range of the filtered set".
// Reading them together implied a contradiction (all 53 shown vs only
// the first 50 shown).
//
// The fix is copy/semantics only (anti-criterion: no page-size or
// underlying-count change). The footer (`<ListPagination>`, a shared
// primitive consumed by /risks, /controls, /policies) is the canonical
// owner of the "Showing M–N of TOTAL" page-range phrasing, so it is
// left untouched. The header drops the verb "Showing" entirely and
// renders a plain COUNT of the filtered catalog:
//
//   * unfiltered:  "53 SCF anchors"
//   * filtered:    "42 of 53 SCF anchors"
//
// With the verb removed, the header reads as a count and the footer
// reads as a page range; they no longer collide. The filtered total
// ("42 of 53") drives the header (AC-3), and the footer's TOTAL is the
// same filtered count ("Showing 1–50 of 42"), so the two are now
// mutually consistent.
//
// This module is the pure, vitest-covered seam: the page renders the
// returned parts inside styled <span>s, but the wording + the
// filtered-vs-total branch live here so the semantics are unit-tested
// without a React tree.

/** Singular/plural noun for the count of SCF anchors. */
export const SCF_ANCHOR_NOUN = "SCF anchors";

export type ControlsCountLabel = {
  /**
   * The filtered count rendered prominently (the number of anchors that
   * match the active filters — drives the header per AC-3).
   */
  filtered: number;
  /** The unfiltered catalog total. */
  total: number;
  /**
   * True when a filter is narrowing the set (`filtered < total`). When
   * false the label collapses to the plain total ("53 SCF anchors").
   */
  isFiltered: boolean;
  /**
   * The fully-assembled accessible label, e.g. "42 of 53 SCF anchors"
   * or "53 SCF anchors". Used as the aria/title text and as the
   * vitest assertion surface; the page renders the same wording via
   * styled spans so the visible text matches this string exactly.
   */
  text: string;
};

/**
 * Build the controls-list header count label from the filtered count
 * and the catalog total.
 *
 * Semantics (slice 666):
 *   - The header is a COUNT, never a "Showing …" range — that phrasing
 *     belongs to the pagination footer, and sharing the verb is what
 *     produced the original contradiction.
 *   - When `filtered === total` (no active filter, or a filter that
 *     happens to match everything) the label is the plain total so the
 *     common case stays terse: "53 SCF anchors".
 *   - When `filtered < total` the label is "N of M SCF anchors" so the
 *     user sees both the narrowed count and the catalog size.
 *
 * Defensive clamping: a negative input is treated as 0, and a
 * `filtered` greater than `total` (which the page never produces, since
 * `visible` is a subset of `rows`) is clamped to `total` so the label
 * can never read "60 of 53".
 */
export function controlsCountLabel(
  filtered: number,
  total: number,
): ControlsCountLabel {
  const safeTotal = Math.max(0, Math.floor(total));
  const safeFiltered = Math.min(safeTotal, Math.max(0, Math.floor(filtered)));
  const isFiltered = safeFiltered < safeTotal;
  const text = isFiltered
    ? `${safeFiltered} of ${safeTotal} ${SCF_ANCHOR_NOUN}`
    : `${safeTotal} ${SCF_ANCHOR_NOUN}`;
  return {
    filtered: safeFiltered,
    total: safeTotal,
    isFiltered,
    text,
  };
}
