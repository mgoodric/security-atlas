// Slice 240 — Policies list "365-day acknowledgment window" disclosure.
//
// The /policies list footer (added by slice 240 alongside the
// `<ListPagination>` primitive from slice 246) discloses the
// acknowledgment-window cadence the platform uses internally for the
// per-policy ack-rate cell rendered in the slice 107 column. The
// mockup at `Plans/mockups/policies.html` lines 278-284 shows the
// disclosure as a tail substring on the footer's summary line:
//
//     Showing 1–7 of 17 · 365-day acknowledgment window
//
// This module is the single source of truth for two values:
//
//   * `POLICY_ACK_WINDOW_DAYS` — the integer constant. A future
//     change to the acknowledgment-cadence policy (e.g. SOC 2 CC1.4
//     revisions, or a tenant-by-tenant override) lands as a one-line
//     edit here; the rendered caption and the vitest assertion both
//     follow automatically.
//
//   * `POLICY_ACK_WINDOW_CAPTION` — the rendered string. The page
//     concatenates it after the pagination summary; pinning the
//     literal here keeps the JSX free of magic numbers (P0-240-2)
//     and ensures the substring "365-day acknowledgment window" is
//     greppable from the test suite (AC-5).
//
// Why a separate module rather than inlining in page.tsx:
//
//   * P0-240-2 explicitly forbids hard-coding `365` as a literal in
//     JSX. The constant has to live somewhere greppable; a sibling
//     module next to the existing `./ack-rate.ts`, `./filters.ts`,
//     `./header-cta-future.ts`, `./scaffold-future.ts` etc. matches
//     the project's per-page-helper convention (slice 217 / 241 / 242
//     precedent).
//
//   * The vitest test environment is node-env, no JSX — pure-data
//     tests over the constants are cheaper and more focused than
//     a JSX rendering test, and they pin the load-bearing invariants
//     that the page-render and the eventual Playwright spec both
//     rely on.
//
// Why "365-day" not "1-year":
//
//   * The mockup uses "365-day" (not "annual" or "yearly"). Audit
//     conventions count calendar days, not anniversaries — a policy
//     ack from 2025-02-29 (which does not exist) versus 2025-03-01
//     cannot use anniversary math. The 365-day count matches the
//     SOC 2 CC1.4 evaluator semantics that drove the choice.

/**
 * The acknowledgment-window length in days. A policy acknowledgment
 * older than this is treated as stale by the per-policy ack-rate
 * evaluator (slice 107).
 *
 * SOC 2 CC1.4 acknowledgment-cadence convention; matches the mockup
 * at `Plans/mockups/policies.html` line 279.
 *
 * Single source of truth — the rendered caption derives from this
 * constant, and a future change to the policy-of-record (e.g. a
 * tenant override or an upstream SOC 2 revision) lands as a one-line
 * edit here.
 */
export const POLICY_ACK_WINDOW_DAYS = 365;

/**
 * The rendered footer-caption substring that discloses the
 * acknowledgment window to the operator. Pinned to derive from
 * `POLICY_ACK_WINDOW_DAYS` so the displayed text and the policy
 * constant cannot drift.
 *
 * The page concatenates this string to the right-hand side of the
 * `<ListPagination>` summary line (the same row that reads
 * "Showing M–N of TOTAL"), producing the mockup-matching
 * "Showing 1-7 of 17 · 365-day acknowledgment window".
 *
 * Substring-shape invariants pinned by `./ack-window.test.ts`:
 *   * non-empty
 *   * starts with the day count (no leading bullet/space — the
 *     page composes the separator)
 *   * contains "acknowledgment window" — load-bearing for the
 *     eventual Playwright assertion (slice 178 / 217 / 241
 *     honesty-harness precedent)
 *   * does NOT name a tracking issue number (slice 184 D3 / 217 D3
 *     / 241 honesty discipline)
 */
export const POLICY_ACK_WINDOW_CAPTION = `${POLICY_ACK_WINDOW_DAYS}-day acknowledgment window`;

/**
 * Test-id surfaced on the `<span>` that renders the
 * acknowledgment-window disclosure in the /policies list footer.
 *
 * Pinned by `./ack-window.test.ts` (AC-6). Slice 178 UI-honesty
 * harness manifests can reference this token to confirm the
 * disclosure is wired even when the row count is zero — though per
 * D3 in the decisions log the entire footer is suppressed on the
 * zero-row case, so the harness asserts on the populated path only.
 */
export const POLICY_ACK_WINDOW_TESTID = "policies-ack-window-disclosure";
