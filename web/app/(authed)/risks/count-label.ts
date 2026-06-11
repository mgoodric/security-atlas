// Slice 684 — Risks list header count label.
//
// Fixes the header/footer count contradiction on /risks (the identical
// defect slice 666 fixed on /controls). The header read
// "Showing {visible.length} of {rows.length} risks" while the shared
// pagination footer (`<ListPagination>`) read "Showing M–N of TOTAL".
// Both used the verb "Showing" but meant different things — the header
// meant "filtered subset of the register total", the footer meant "this
// page's range of the filtered set". Read together they collided (all
// rows shown vs only the first page shown).
//
// The fix is copy/semantics only (anti-criterion: no page-size or
// underlying-count change). The footer (`<ListPagination>`, a shared
// primitive consumed by /risks, /controls, /policies) is the canonical
// owner of the "Showing M–N of TOTAL" page-range phrasing, so it is
// left untouched. The header drops the verb "Showing" entirely and
// renders a plain COUNT of the filtered register:
//
//   * unfiltered:  "53 risks"
//   * filtered:    "42 of 53 risks"
//
// With the verb removed, the header reads as a count and the footer
// reads as a page range; they no longer collide. The filtered total
// ("42 of 53") drives the header (AC-3), and the footer's TOTAL is the
// same filtered count ("Showing 1–50 of 42"), so the two are now
// mutually consistent.
//
// JUDGMENT call (see docs/audit-log/684-risks-count-semantics-decisions.md
// D1): this module is a PAGE-LOCAL seam, mirroring slice 666's deliberate
// page-local choice and the filters.ts/selection.ts per-page convention,
// rather than a shared generic. The noun differs per page ("risks" vs
// "SCF anchors"); a shared extraction would need a noun parameter for
// modest benefit and would force a refactor of the just-merged /controls
// page. A shared generic stays reasonable if a third consumer appears.
//
// This module is the pure, vitest-covered seam: the page renders the
// returned parts inside styled <span>s, but the wording + the
// filtered-vs-total branch live here so the semantics are unit-tested
// without a React tree.

/** Singular/plural noun for the count of risks. */
export const RISK_NOUN = "risks";

export type RisksCountLabel = {
  /**
   * The filtered count rendered prominently (the number of risks that
   * match the active filters — drives the header per AC-3).
   */
  filtered: number;
  /** The unfiltered register total. */
  total: number;
  /**
   * True when a filter is narrowing the set (`filtered < total`). When
   * false the label collapses to the plain total ("53 risks").
   */
  isFiltered: boolean;
  /**
   * The fully-assembled accessible label, e.g. "42 of 53 risks" or
   * "53 risks". Used as the aria/title text and as the vitest assertion
   * surface; the page renders the same wording via styled spans so the
   * visible text matches this string exactly.
   */
  text: string;
};

/**
 * Build the risks-list header count label from the filtered count and
 * the register total.
 *
 * Semantics (slice 684, mirroring slice 666):
 *   - The header is a COUNT, never a "Showing …" range — that phrasing
 *     belongs to the pagination footer, and sharing the verb is what
 *     produced the original contradiction.
 *   - When `filtered === total` (no active filter, or a filter that
 *     happens to match everything) the label is the plain total so the
 *     common case stays terse: "53 risks".
 *   - When `filtered < total` the label is "N of M risks" so the user
 *     sees both the narrowed count and the register size.
 *
 * Defensive clamping: a negative input is treated as 0, and a
 * `filtered` greater than `total` (which the page never produces, since
 * `visible` is a subset of `rows`) is clamped to `total` so the label
 * can never read "60 of 53".
 */
export function risksCountLabel(
  filtered: number,
  total: number,
): RisksCountLabel {
  const safeTotal = Math.max(0, Math.floor(total));
  const safeFiltered = Math.min(safeTotal, Math.max(0, Math.floor(filtered)));
  const isFiltered = safeFiltered < safeTotal;
  const text = isFiltered
    ? `${safeFiltered} of ${safeTotal} ${RISK_NOUN}`
    : `${safeTotal} ${RISK_NOUN}`;
  return {
    filtered: safeFiltered,
    total: safeTotal,
    isFiltered,
    text,
  };
}
