// Slice 217 — OSCAL export future-state disclosure (label-honesty path).
//
// The /audits page formerly rendered a permanently-disabled "Export
// OSCAL bundle" button next to three working CSV/JSON/XLSX exports
// (slices 138 / 139). The mockup at `Plans/_archive/mockups/audits.html` line
// 116 positioned the OSCAL export as a primary affordance, but the
// underlying capability is a per-period (not list-level) operation —
// the right home for it is the per-period detail view (slice 184
// follow-on), not this index. Slice 217 closes the HONESTY-GAP per
// slice 178's first-pass finding (F-178-217) on the label-honesty path
// (Path A from the slice doc).
//
// Shape choice — non-button `<span>` with `title` + `aria-label`.
//
// Precedent: slice 183's calendar agenda-view does exactly this for
// exception / policy events whose detail routes do not exist yet (see
// `web/components/calendar/agenda-view.tsx` lines 173-179). The shape:
//
//     <span title={reason} aria-label={reason} data-testid={...}>
//       OSCAL bundle export — per-period detail view (coming).
//     </span>
//
// Why a span, not a Popover / Tooltip primitive:
//   * The shadcn ecosystem does not have a `tooltip.tsx` primitive
//     in this repo's `web/components/ui/`. Pulling
//     `@base-ui/react/tooltip` (or equivalent) purely for this surface
//     would echo the version-footer note: "we don't pull in the
//     popover dependency purely for this surface" (see
//     `web/components/version-footer.tsx` lines 17-22).
//   * The slice 183 span+title pattern composes with the slice 178
//     honesty harness — the harness specifically looks for
//     `button[disabled]` whose text matches a coming-soon pattern (see
//     `web/e2e-audit/lib/heuristics.ts` `captureComingSoonButtons`).
//     A `<span>` is invisible to that heuristic, which is the correct
//     behavior: the disclosure IS the affordance, so the gap is
//     closed.
//   * `cursor-help` styling is already in the slice 183 vocabulary
//     (`web/components/calendar/agenda-view.tsx` line 176;
//     `web/components/calendar/month-grid-view.tsx` line 229).
//
// Constants are exported so:
//   * Vitest pins the testid token (AC-A2) without rendering the page.
//   * Playwright (AC-A4) can assert the visible text contains
//     "per-period" without hard-coding the full copy.
//   * A future copy rewrite changes one place, both tests follow.

/**
 * Test-id token surfaced on the `<span>` that replaces the formerly-
 * disabled "Export OSCAL bundle" button.
 *
 * Pinned by `oscal-export-future.test.ts` (AC-A2 of slice 217). The
 * slice 178 UI-honesty harness's manifest will reference this token to
 * confirm the disclosure is the affordance, not a dead button.
 */
export const OSCAL_EXPORT_FUTURE_TESTID = "audits-oscal-export-future";

/**
 * The disclosure copy rendered as the visible text AND as `title` +
 * `aria-label` on the `<span>` that replaces the formerly-disabled
 * button. Single source of truth — the page imports it directly.
 *
 * Copy discipline:
 *   * Future-tense framing — "ships with", not "is broken" / "is
 *     unavailable" / "is disabled".
 *   * Names the capability ("per-period detail view") rather than a
 *     specific tracking issue number, per slice 184 D3 ("Issue number
 *     in banner copy: omitted"): a slice number can be re-shuffled,
 *     the capability name is stable.
 *   * Sentence-shaped (capitalized, ends with a period). Tests pin
 *     both.
 *   * The substring "per-period" is load-bearing — AC-A4's Playwright
 *     spec asserts on it.
 */
export const OSCAL_EXPORT_FUTURE_REASON =
  "OSCAL bundle export ships with the per-period detail view — open a period to export.";
