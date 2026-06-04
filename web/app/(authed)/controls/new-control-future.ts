// Slice 225 — "New control" future-state disclosure (label-honesty path).
//
// The /controls page formerly rendered a permanently-disabled "New
// control" button at the top-right of the toolbar (`page.tsx` line 429
// pre-slice-225). The mockup at `Plans/_archive/mockups/controls.html` (lines
// 121-124) positioned it as an enabled primary CTA, but the
// create-control flow is a substantive feature (SCF anchor pick +
// applicability_expr + framework satisfactions + optional policy
// attach) deliberately deferred per the slice doc. The route
// `/controls/new` does not exist on `main` — verified by listing
// `web/app/(authed)/controls/`.
//
// This module mirrors the slice 217 label-honesty pattern verbatim
// (see `../audits/oscal-export-future.ts`). The shape is a non-button
// `<span>` carrying `title` + `aria-label` + a stable test-id — the
// disclosure IS the affordance, and the slice 178 honesty-harness
// `captureComingSoonButtons` heuristic (which flags `button[disabled]`
// patterns) becomes invisible to this surface, which is the correct
// behavior: the HONESTY-GAP closes both visually (no greyed-out dead
// button) and at the audit-harness level.
//
// Constants are exported so:
//   * Vitest pins the testid token (AC-2) without rendering the page.
//   * Playwright (AC-4) can assert the visible text contains the
//     load-bearing substring without hard-coding the full copy.
//   * A future copy rewrite changes one place, both tests follow.
//
// Reversibility: when a future slice ships the create-control flow
// (route `/controls/new` + form + mutation), this `<span>` flips back
// to a working `<Link>`-wrapped `<Button>` (per slice 247's pattern)
// and this module deletes. One PR, clean reversal.

/**
 * Test-id token surfaced on the `<span>` that replaces the formerly-
 * disabled "New control" button.
 *
 * Pinned by `new-control-future.test.ts` (AC-2 of slice 225). The
 * slice 178 UI-honesty harness's manifest will reference this token
 * to confirm the disclosure is the affordance, not a dead button.
 */
export const NEW_CONTROL_FUTURE_TESTID = "controls-new-control-disabled-reason";

/**
 * The disclosure copy rendered as the visible text AND as `title` +
 * `aria-label` on the `<span>` that replaces the formerly-disabled
 * button. Single source of truth — the page imports it directly.
 *
 * Copy discipline:
 *   * Future-tense framing — "lands in", not "is broken" / "is
 *     unavailable" / "is disabled".
 *   * Names the capability ("create-control flow") rather than a
 *     specific tracking issue number, per slice 184 D3 + slice 217
 *     D3 ("Issue number in banner copy: omitted") — a slice number
 *     can be re-shuffled, the capability name is stable.
 *   * Names two concrete positive next steps (SCF importer + atlas
 *     CLI) so the user has somewhere to go, mirroring the slice
 *     spec's AC-1 copy.
 *   * Sentence-shaped (capitalized, ends with a period). Tests pin
 *     both.
 *   * The substring "create-control flow" is load-bearing — AC-4's
 *     Playwright spec asserts on it.
 */
export const NEW_CONTROL_FUTURE_REASON =
  "Create-control flow lands in a future slice. For now, controls are instantiated by the SCF importer or by the atlas CLI.";
