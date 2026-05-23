# 222 — decisions log

> Type: AFK (decisions log included for parity with active-batch discipline,
> even though sign-off is not required).

## Decisions made

### D1 — Caption renders inside `PostureTiles`, not as a `SectionCard` prop

**Options considered.**

1. Add the caption inside `PostureTiles` itself (rendered after the tile grid).
2. Add a generic `caption` prop to `SectionCard` and pass the string from
   `board-packs/[id]/page.tsx` only for the `"posture"` case.
3. Hard-code the caption into the `case "posture":` arm of `SectionStructured`
   in `page.tsx`.

**Chosen.** Option 1. The caption is a property of "what posture means", not
a property of "what a section card displays". Coupling it to `PostureTiles`
makes the caption's render-once-per-posture invariant true by construction
(AC-3): the caption renders if and only if this component is mounted, and
this component is only mounted on `section.key === "posture"` via the
`SectionStructured` switch in `web/app/(authed)/board-packs/[id]/page.tsx`.
Option 2 widens `SectionCard`'s API surface for a single consumer. Option 3
duplicates a constitutional methodology string into a route file, which makes
future drift more likely.

**Rationale.** Locality + AC-3 enforcement-by-construction. Matches the
existing pattern (`PostureTiles` already owns the empty-state copy, the tile
grid, and the data-testid contract for the posture section).

**Confidence.** high.

### D2 — Caption text lives in a sibling pure-TS module, not inline in `.tsx`

**Options considered.**

1. Inline the string as a local const inside `posture-tiles.tsx`.
2. Export the string from `posture-tiles.tsx` itself.
3. Split the string into a sibling `posture-coverage-caption.ts` module.

**Chosen.** Option 3. The vitest config (`web/vitest.config.ts`) is node-env
and explicitly disallows component rendering (P0-A3 in slice 069). Importing
a `.tsx` file into a node-env vitest can be brittle when the JSX runtime
isn't fully resolvable at import time. The slice 183 precedent
(`web/components/calendar/link-for.ts` split out of its `.tsx` siblings) is
the established pattern for this exact situation — pure-logic helper in `.ts`,
test colocated as `*.test.ts`, view module in `.tsx` imports the helper.
Option 1 makes AC-4 untestable under the project's vitest constraints.
Option 2 works in principle but invites the JSX-runtime brittleness above.

**Rationale.** Follows slice 183 precedent verbatim. Keeps the unit-test
surface clean (node-env, no JSX, no React in the import graph for tests).

**Confidence.** high.

### D3 — Render the caption in BOTH the populated and empty states

**Options considered.**

1. Render the caption only when at least one framework tile is shown.
2. Render the caption in both branches (populated tiles + empty state).

**Chosen.** Option 2. The methodology disclosure is constitutional content
(canvas §5.5, invariant 5) — it tells the reader what the coverage number
would be if a framework were attached. Even when no posture rows are
present, the page should disclose that the coverage methodology is
intersected with each framework's scope predicate. An empty posture state
is a "we don't have data yet" message, not a "we're not using this
methodology" message.

**Rationale.** The caption is a property of the posture _concept_, not of
the _data_. Showing it unconditionally keeps the methodology disclosure
stable across all states of the section.

**Confidence.** medium. AC-1 reads "below the tiles" — strict reading is
"only when tiles render". I'm interpreting AC-1's intent as "in the
posture section card", since the empty-state still occupies the posture
section. If a reviewer reads AC-1 strictly and prefers only-when-populated,
the change is one line: move the second `<p>` out of the empty-state
branch. No other code changes.

### D4 — Two assertions in the unit (exact-match + soft regex)

**Options considered.**

1. Single `toBe(expected)` exact-match assertion.
2. Exact-match plus soft regex assertions on `intersected` and `scope predicate`.

**Chosen.** Option 2. The exact-match assertion is the load-bearing
contract — it pins the audit-trail string. The soft regex assertions exist
as a "what-broke" signal: if someone reworks the sentence in a future slice
and accidentally drops the constitutional concept, the failure message
tells them which constitutional concept they lost, not just that the string
differs.

**Rationale.** Belt-and-suspenders. Cheap to add, useful at PR-review time.

**Confidence.** high.

## Revisit once in use

- **Caption placement on the empty state.** D3's interpretation is medium-
  confidence. If a real board-pack with no posture rows ever ships and a
  user reports the caption feels misplaced ("you're disclosing methodology
  for a thing that doesn't exist yet"), move the caption to the populated
  branch only.
- **Per-section caption pattern.** If a second section ever needs a
  methodology caption (e.g., top-risks: "Residual = inherent × control
  effectiveness"), promote the caption pattern to a `SectionCard` `caption`
  prop and migrate `PostureTiles` to use it. Don't pre-build that
  abstraction now (anti-pattern: future-proofing).
- **Mockup-text drift watcher.** If `Plans/mockups/board-pack.html`'s
  caption text ever changes, the unit test will catch it as a `toBe`
  failure. The maintainer must decide: is the mockup the new truth (update
  the constant), or is the production string the truth (update the
  mockup)? Default: the canvas + mockup are the design system of record,
  so the constant follows the mockup unless an ADR says otherwise.
- **i18n.** When the platform internationalizes (no scheduled v1/v2 slice
  yet), the caption string becomes an i18n key, not a const. At that
  point retire `POSTURE_COVERAGE_CAPTION` and replace it with the i18n
  lookup; the test pins the English-source string in the catalog instead
  of the const.

## Confidence summary

| Decision                                               | Confidence |
| ------------------------------------------------------ | ---------- |
| D1 — caption inside `PostureTiles`                     | high       |
| D2 — sibling pure-TS module for caption                | high       |
| D3 — render caption in both populated and empty states | medium     |
| D4 — exact-match + soft regex unit assertions          | high       |
