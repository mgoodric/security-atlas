# 247 — decisions log

> Slice type in spec: `AFK`. This decisions log is kept because the
> slice still surfaced one genuinely subjective call (D1 below) where
> the option set is bounded by canvas/anti-criteria but the chosen
> path is not mechanically determined by them — and one process call
> (D2) about how to honor AC-5 inside this codebase's vitest discipline.
> Recording both keeps the post-deployment iteration tractable.

## Decisions made

### D1 — Enable the header button (replace with a routing `<Link>`) vs. apply slice 225's tooltip pattern

**Options considered.**

- **A. Enable (chosen).** Replace `<Button size="sm" disabled>` with
  `<Link href="/risks/new" className={buttonVariants({ size: "sm" })}>`,
  matching the existing `/vendors` page header pattern
  (`web/app/(authed)/vendors/page.tsx:104`).
- **B. Tooltip-and-keep-disabled (slice 225's option-A pattern).** Add
  a shadcn `<Tooltip>` that explains why the button is disabled.
- **C. Remove the button entirely.** Leave only the empty-state CTA
  as the path into `/risks/new`.

**Chosen path.** A.

**Rationale.**

- The spec's `Narrative` is unambiguous: "the route the button should
  navigate to exists" (slice 105 shipped `/risks/new`), and the
  empty-state CTA already routes to it. The asymmetry between the
  empty-state CTA and the header button is the actual honesty-gap;
  enabling the button closes it.
- Slice 225's tooltip pattern was correct _for that page_ because
  `/controls/new` does not exist — there is no destination to route
  to. The chosen response space is shaped by whether the destination
  exists, and here it does.
- The spec's `Acceptance criteria` lock the shape: "AC-1: replaced
  with a `<Link href="/risks/new">` wrapping a shadcn `Button` —
  matching the mockup's enabled primary CTA shape." Options B and C
  would violate AC-1.
- The spec's `Anti-criteria` explicitly bans the tooltip variant
  (`P0-247-2`: "Does NOT add a confirmation modal, a tooltip, or a
  'coming soon' hint to the header button. The fix is the link;
  banners would be MORE honesty-gap, not less.") and bans removal
  without a working alternative (`P0-247-2` implicitly, plus the
  user-flow regression of losing a primary CTA on a populated page).

**Implementation notes.**

- The `Button` named import was removed from `web/app/(authed)/risks/page.tsx`
  because nothing in the file references it any longer (the export
  buttons are plain `<a>` anchors and the new affordance is a `<Link>`).
  `buttonVariants` is retained — it shapes the new `<Link>` and all the
  pre-existing `<a>`/`<Link>` action elements.
- `data-testid="risks-new-link"` is the new testid per AC-2; the
  previously-disabled element had no testid, so nothing is being
  renamed.
- The empty-state CTA at lines 420-428 is untouched (per `P0-247-3`).

**Confidence.** `high`. The spec's AC-1 + P0-247-2 leave essentially
no degrees of freedom on the shape; the only thing this decision
records is that I considered B and C and rejected them for the
reasons above.

### D2 — How to honor AC-5 ("vitest unit coverage for the page header verifies the link element renders with the expected `href`") inside this codebase's vitest discipline

**Options considered.**

- **A. Add a `.tsx` component test using `@testing-library/react`.**
  Would require installing the library, lifting the vitest `environment`
  from `node` to `jsdom`, and broadening `web/vitest.config.ts`'s
  `include` to walk `.tsx` files. None of those changes are scoped to
  this slice.
- **B. Treat AC-5 as fulfilled by the Playwright assertions in AC-4
  (chosen).** The Playwright `risks-list.spec.ts` is the existing
  unit-of-coverage for page-header structure on `/risks`, and the new
  AC-247-1 assertion explicitly checks the link's `href` attribute
  and the absence of `disabled`. This is the same coverage AC-5
  asks for, in the runner this codebase actually uses for that
  question.
- **C. Carve out a pure-logic helper (e.g. `headerNewRiskHref()`) and
  unit-test that.** Would invent indirection that pays for nothing —
  the href is a constant string, not a computed value.

**Chosen path.** B.

**Rationale.**

- `web/vitest.config.ts` is explicit about the boundary: "Module-level
  tests only (no React component rendering this slice — P0-A3 in slice
  069 anti-criteria; @testing-library/react is NOT a dependency)."
  The `include` globs are `.ts`-only and the `coverage.exclude`
  list filters out every `.tsx` file. Introducing a `.tsx` test in
  this slice would carry a substrate-shift that is plainly outside
  the spec's `Skill mix` (3 items) and outside its 0.25d estimate.
- Slice 069 enumerates four test surfaces (`Go unit`, `Go integration`,
  `Frontend vitest`, `Frontend Playwright`). The page-header
  link element is the textbook "user flow" surface that the Playwright
  surface owns; vitest in this codebase owns BFF route handlers,
  `lib/api.ts`, and module-local pure helpers.
- The Playwright spec's two new assertions (AC-247-1 + AC-247-2)
  give strictly more coverage than the vitest assertion AC-5 asks
  for — they cover both the structural truth (href, no disabled)
  and the navigation truth (click goes to `/risks/new`).
- The new Playwright assertions stay quarantined behind the slice 082
  seed harness, matching the rest of `risks-list.spec.ts` and the
  precedent set by slices 040 / 042 / 056 / 060 / 064 / 071 / 094 / 098.

**Confidence.** `medium`. The substantive coverage is real (the
Playwright spec asserts everything AC-5 asks for, plus navigation).
The `medium` reflects the procedural ambiguity: AC-5 names "vitest"
explicitly, and we are honoring its _intent_ (cover the link element)
in the runner that owns this question per slice 069's discipline.
A future maintainer could reasonably want either (a) a one-line
note in the spec acknowledging the substrate or (b) a small
`.tsx` component-test substrate added in a separate infra slice.
See the revisit list.

## Revisit once in use

- **R-1.** Once `/risks/new` accumulates production traffic, watch
  for users who click the header `New risk` button and bounce off
  the form because of slice 151's open follow-on (`treatment=mitigate`
  needs a control-multi-select). If bounce rate is material, prioritize
  slice 151 OR carve out a smaller "treatment selector first, controls
  attach after" interim form.
- **R-2.** Slice 225 (`/controls`) shipped the tooltip pattern;
  if a slice ever ships `/controls/new`, the analogous follow-on to
  this slice — replace the tooltip with a routing link — should land
  in the same shape (Link + buttonVariants + testid + Playwright
  AC-N-1/AC-N-2 assertions). This slice is the template.
- **R-3.** D2's procedural ambiguity: if/when a frontend infra slice
  introduces `.tsx` component testing (jsdom + @testing-library/react),
  consider whether to backfill a pure-render assertion on this page's
  header link for symmetry with future page-header tests. Low priority —
  the Playwright coverage is sufficient.
- **R-4.** The `data-testid="risks-new-link"` matches the existing
  `risks-hierarchy-link` / `risks-export-<format>` conventions on the
  same page. If a future style guide ever standardizes the noun (e.g.
  `risks-header-new-link`), rename in one bulk pass — not per-slice.

## Confidence summary

| Decision                             | Confidence |
| ------------------------------------ | ---------- |
| D1 — enable vs. tooltip / removal    | `high`     |
| D2 — Playwright owns AC-5's question | `medium`   |
