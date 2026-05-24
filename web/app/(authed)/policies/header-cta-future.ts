// Slice 241 — Policies header CTAs future-state disclosure
// (label-honesty path).
//
// The /policies header action row formerly rendered two
// permanently-disabled buttons next to the working slice-138
// CSV/JSON/XLSX export trio:
//
//     <Button variant="outline" size="sm" disabled>
//       Acknowledgment report
//     </Button>
//     <Button size="sm" disabled>
//       New policy
//     </Button>
//
// Both are inert and undisclosed — the slice-178 honesty-gap class
// (F-178-241 in the audit log of `/policies` against
// `Plans/mockups/policies.html` lines 116-121). The mockup shows
// both buttons as active primary affordances; the production page
// renders them grey and unclickable with no tooltip / no link / no
// "coming in slice X" pointer. The user sees a button-shaped
// control that looks clickable and does nothing.
//
// Slice 241 closes the gap on the label-honesty path (Path B in
// the slice doc — Path A ships the underlying capabilities;
// neither `/policies/new` nor an acknowledgment-report-generation
// surface exists on main today, so building either inline is
// explicitly out of scope per the slice doc's anti-criteria
// (P0-241-1, P0-241-2) and would blow the 0.5d budget for both
// buttons combined).
//
// Why the slice 217 `<span>` shape (not the slice 247 enable-via-
// Link shape):
//   * Slice 247 enabled the formerly-disabled "New risk" button on
//     `/risks` by wrapping a `<Link>` in `buttonVariants({size:
//     "sm"})` because the destination — `/risks/new` from slice
//     105 — ALREADY EXISTED. For slice 241, neither destination
//     exists. Verified by directory listing: `web/app/(authed)/
//     policies/new/` does NOT exist; no acknowledgment-report
//     surface exists either.
//   * Slice 217 replaced a single permanently-disabled OSCAL
//     export button next to working sibling buttons with a
//     `<span>` carrying `title` + `aria-label` + a stable test-id
//     so the disclosure IS the affordance. That is the right
//     shape here — both lying buttons sit next to the working
//     slice-138 export trio in the same toolbar, and replacing
//     each in-place with a labelled `<span>` preserves the
//     toolbar's visual rhythm.
//
// Why a span, not a Popover / Tooltip primitive:
//   * The shadcn ecosystem does not have a `tooltip.tsx` primitive
//     in this repo's `web/components/ui/`. Same reasoning slice
//     217 cited — pulling `@base-ui/react/tooltip` (or equivalent)
//     purely for this surface would echo the version-footer note:
//     "we don't pull in the popover dependency purely for this
//     surface" (`web/components/version-footer.tsx` lines 17-22).
//   * The slice 183 / 217 `<span title>` pattern composes with the
//     slice 178 honesty harness — the harness's
//     `captureComingSoonButtons` heuristic specifically looks for
//     `button[disabled]` whose text matches a coming-soon pattern
//     (`web/e2e-audit/lib/heuristics.ts`). A `<span>` is invisible
//     to that heuristic, which is the correct behavior: the
//     disclosure IS the affordance, so the gap is closed.
//   * `cursor-help` is the established cue (slice 183 + 217).
//
// Constants are exported so:
//   * Vitest pins the testid tokens (AC-4) without rendering the
//     page.
//   * Playwright (AC-4) asserts the visible text contains the
//     load-bearing capability substring.
//   * A future copy rewrite changes one place; both tests follow.

/**
 * Test-id token surfaced on the `<span>` that replaces the
 * formerly-disabled "Acknowledgment report" button.
 *
 * Pinned by `header-cta-future.test.ts` (AC-4 of slice 241). The
 * slice 178 UI-honesty harness's manifest will reference this
 * token to confirm the disclosure is the affordance, not a dead
 * button.
 */
export const POLICIES_ACK_REPORT_FUTURE_TESTID = "policies-ack-report-future";

/**
 * The disclosure copy rendered as the visible text AND as `title`
 * + `aria-label` on the `<span>` that replaces the formerly-
 * disabled "Acknowledgment report" button. Single source of truth
 * — the page imports it directly.
 *
 * Copy discipline:
 *   * Future-tense framing — "ships with a future slice", NOT "is
 *     broken" / "is unavailable" / "is disabled".
 *   * Names the capability ("acknowledgment report") rather than
 *     a specific tracking issue number, per slice 184 D3 / slice
 *     217 D3 / slice 242 D3 precedent: a slice number can be re-
 *     shuffled, the capability name is stable.
 *   * Names the operator's next concrete check — the existing
 *     per-policy ack-rate column (slice 107) on this same page is
 *     where ack data is surfaced today — so the disclosure is a
 *     signpost, not a dead end.
 *   * Sentence-shaped (capitalized, ends with a period). Tests
 *     pin both.
 *   * The substring "acknowledgment report" is load-bearing —
 *     AC-4's Playwright spec asserts on it.
 */
export const POLICIES_ACK_REPORT_FUTURE_REASON =
  "The in-app acknowledgment report ships with a future slice — until then, per-policy ack rates surface in the Acknowledgment column on this page.";

/**
 * Test-id token surfaced on the `<span>` that replaces the
 * formerly-disabled "New policy" button.
 *
 * Pinned by `header-cta-future.test.ts` (AC-4 of slice 241).
 */
export const POLICIES_NEW_POLICY_FUTURE_TESTID = "policies-new-policy-future";

/**
 * The disclosure copy rendered as the visible text AND as `title`
 * + `aria-label` on the `<span>` that replaces the formerly-
 * disabled "New policy" button. Single source of truth — the
 * page imports it directly.
 *
 * Copy discipline mirrors slice 242 D2 / D3 for cross-slice
 * coherence — both surfaces (this header CTA and the empty-state
 * disclosure) point the operator at the same `POST /v1/policies`
 * platform API endpoint as the next concrete action. When the
 * in-app `/policies/new` form ships, both disclosures retire
 * together.
 *
 * Copy discipline:
 *   * Future-tense framing — "ships with a future slice".
 *   * Names the capability ("policy-create form") rather than a
 *     specific tracking issue number.
 *   * Names the operator's next concrete action — drafting
 *     policies via the platform API (`POST /v1/policies`).
 *   * Sentence-shaped (capitalized, ends with a period).
 *   * The substring "policy-create form" is load-bearing — AC-4's
 *     Playwright spec asserts on it. The substring "POST /v1/
 *     policies" is load-bearing — vitest asserts the API endpoint
 *     is named so the disclosure is a signpost, not a dead end.
 */
export const POLICIES_NEW_POLICY_FUTURE_REASON =
  "The in-app policy-create form ships with a future slice — until then, policies can be drafted via the platform API (POST /v1/policies).";
