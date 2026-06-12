// Slice 242 — Policies empty-state scaffold future-state disclosure
// (label-honesty path).
//
// The /policies empty-state formerly rendered a primary CTA
// "Scaffold five foundational policies" whose `onClick` handler
// pointed at `/admin/credentials` — an unrelated admin surface
// (see slice 101 P0-A4 placeholder note + slice 100 precedent).
// That is the slice-178 honesty-gap class: the button text claims
// capability X, the runtime behavior delivers unrelated
// destination Y.
//
// Slice 242 closes the gap on the label-honesty path (Path B in
// the slice doc — Path A ships an actual scaffold wizard;
// neither `/policies/scaffold` nor `/policies/new` page routes
// exist on main today, so building the wizard inline is
// explicitly out of scope per the slice doc and would also bust
// the 0.5d budget). Per the slice doc, the fix is to soften the
// CTA copy so the empty-state accurately describes what it does
// — which on main today is nothing in-app. Path A becomes
// available when a separate scaffold-wizard slice ships.
//
// Shape choice — drop the CTA prop entirely; fold the disclosure
// into the empty-state body text.
//
// The `EmptyState` shell at `web/components/list/empty-state.tsx`
// renders the `cta` prop as a real `<Button>` element. The slice
// 217 precedent on `/audits` swapped a `<Button disabled>` for a
// `<span>` carrying `title` + `aria-label` because that button
// stood in a row of working sibling export-buttons; replacing it
// with a `<span>` in-place preserved the toolbar's visual
// rhythm. Here the lying CTA is the ONLY action on the
// empty-state card — there is no sibling row to keep visually
// balanced, and the EmptyState shell does not currently support
// rendering a span+title affordance in the CTA slot without
// broadening its API (out of scope for this 0.5d slice).
//
// Folding the disclosure into the body text:
//   * Eliminates the lying-button entirely (P0-242-4: "does NOT
//     redirect the CTA to yet another unrelated admin page. The
//     fix is either ship the destination or update the copy —
//     not move the lie.").
//   * Reads as informational prose, which matches the
//     informational nature of the message (the operator's next
//     action is documentation, not a click).
//   * The body-as-disclosure pattern composes with the slice 178
//     honesty harness — `captureComingSoonButtons` looks for
//     `button[disabled]` matching coming-soon copy; a body
//     paragraph is invisible to that heuristic, which is the
//     correct behavior because the disclosure IS the affordance.
//   * Slice 152 (`/controls`) set the precedent of an
//     informational empty-state body that names the maintainer's
//     next action ("seed the SCF catalog") without inventing an
//     in-app button for it. Same pattern.
//
// Why a body rewrite, not the slice 217 `<span>` shape:
//   * The `<span>` shape is the right answer when the lying
//     button stands alongside working sibling buttons (slice 217:
//     three working CSV/JSON/XLSX export buttons next to one
//     lying OSCAL bundle button). Replacing the lying button
//     with a span preserves the toolbar's rhythm.
//   * The empty-state card has ONE action slot. Removing the
//     CTA prop and folding the disclosure into the body is
//     cleaner than inventing a span+title pattern inside an
//     EmptyState shell that doesn't currently model one.
//
// Constants are exported so:
//   * Vitest pins the body copy invariants (sentence-shape,
//     future-tense, no failure-framing words, no placeholder
//     slice number) without rendering the page.
//   * Playwright (AC-7) asserts the visible body text contains
//     the load-bearing capability phrase.
//   * A future copy rewrite changes one place; both tests
//     follow.

/**
 * The disclosure copy rendered as the empty-state `body` on the
 * `/policies` zero-row state. Single source of truth — the page
 * imports it directly. Replaces the formerly-lying
 * "Scaffold five foundational policies" CTA whose `onClick`
 * pointed at `/admin/credentials`.
 *
 * Copy discipline:
 *   * Future-tense framing — names what WILL happen ("ships
 *     with a future slice"), NOT what is broken
 *     ("disabled" / "unavailable" / "not working" / "error").
 *   * Names the capability ("policy scaffold wizard") rather
 *     than a tracking issue number, per slice 184 D3 / slice
 *     217 D3 precedent: a slice number can be re-shuffled, the
 *     capability name is stable.
 *   * Names the operator's next concrete action — drafting
 *     policies via the API (`POST /v1/policies`) — so the
 *     empty-state is a signpost, not a dead end.
 *   * Sentence-shaped (capitalized, ends with a period). Tests
 *     pin both.
 *   * The substring "policy scaffold" is load-bearing — AC-7's
 *     Playwright spec asserts on it.
 */
export const POLICIES_SCAFFOLD_FUTURE_BODY =
  "The in-app policy scaffold wizard is not available yet — until then, drafts can be created via the platform API (POST /v1/policies).";

/**
 * Test-id token surfaced on the empty-state body wrapper so the
 * slice 178 honesty harness can assert on the disclosure shape
 * directly. The harness's `captureComingSoonButtons` heuristic
 * looks for `button[disabled]`; a `data-testid` on the body lets
 * the manifest entry confirm the disclosure IS the affordance,
 * not a dead button.
 *
 * Pinned by `scaffold-future.test.ts` (AC-7 of slice 242).
 */
export const POLICIES_SCAFFOLD_FUTURE_TESTID = "policies-scaffold-future";
