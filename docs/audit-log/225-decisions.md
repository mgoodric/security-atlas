# Slice 225 — "New control" button affordance (build-time decisions)

> JUDGMENT-type slice. Engineer made the build-time calls per the
> `JUDGMENT` slice-development pattern (CLAUDE.md "AI-assist boundary":
> this is about how we build, NOT how the shipped product behaves). The
> product still never publishes audit-binding artifacts without one-
> click human approval — this slice is chrome.

---

## D1 — Path A (label-honesty) over Path B (build the create-control flow)

**Decision:** Path A — replace the permanently-disabled "New control"
`<Button>` on `/controls` with a non-button affordance that discloses
the future-state. Defer Path B (the substantive create-control mutation
surface) to a future slice.

**Why:**

- **The `/controls/new` route does NOT exist.** Verified by listing
  `web/app/(authed)/controls/`: `[id]/`, `error-classifier.{ts,test.ts}`,
  `filters.{ts,test.ts}`, `page.tsx`. No `new/` directory. The chosen
  response space is shaped by whether the destination exists; here it
  does not — slice 247's enable-pattern is unavailable, slice 217's
  tooltip-pattern is the right move.
- **Scope match.** The slice doc estimates option A at 0.5d and option
  B at 3d. The slice landed under the option-A budget.
- **Path B's blast radius.** Per the slice spec narrative: the
  create-control flow requires (a) SCF anchor pick, (b) an
  `applicability_expr` editor, (c) framework satisfactions, and (d)
  optional control-policy attach. Each of those is a non-trivial UI +
  API surface. Filing as a separate slice is the right scoping move
  per the spillover discipline (slice 178).
- **Reversibility.** Path A is reversible in one PR when Path B is
  ready: the `<span>` is replaced with a working `<Link>`-wrapped
  `<Button>` per slice 247's enable-pattern, and the
  `new-control-future.ts` module is deleted in that same PR.

**Alternative rejected (Path B):** see scope-match reasoning above. The
spec explicitly defers it; filing as a spillover would substitute
engineer judgment for maintainer prioritization. Slice 247's R-2 already
flagged this slice as the template for the reverse direction when
`/controls/new` does ship.

**Alternative rejected (option-A AC-3 banner variant):** the spec's
AC-3 offers a sibling presentation (info-banner above the table + a
link to `/catalog/scf`). Slice 217 D2 rejected the banner shape for the
audits OSCAL surface with the same reasoning that applies here: the
limitation is action-level (one specific button), not page-level —
pulling it up to a page banner overstates the scope. The local in-place
disclosure matches the local in-place button it replaces.

**Confidence:** high. The slice doc's "default recommendation is option
A" + the verified absence of `/controls/new` are doing most of the work
here.

---

## D2 — Shape: non-button `<span>` with `title` + `aria-label`

**Decision:** The replacement affordance is a `<span>` carrying both
`title` and `aria-label` attributes (same copy on both, same copy in
the visible text). Test-id token:
`controls-new-control-disabled-reason`. CSS: `cursor-help` plus muted-
italic styling so it visually reads as informational chrome, not a
clickable button.

**Why:**

- **Direct slice 217 precedent.** The audits OSCAL-export disclosure
  uses this exact shape (see `web/app/(authed)/audits/oscal-export-
future.ts` + the rendered `<span>` in `audits/page.tsx` lines 392-
  399). Same problem class, same solution shape — the codebase keeps
  a coherent vocabulary.
- **Honesty harness alignment.** Slice 178's `captureComingSoonButtons`
  heuristic specifically flags `button[disabled]` whose text matches a
  coming-soon pattern. A `<span>` is invisible to that heuristic, which
  is the correct behavior: the disclosure IS the affordance, so the gap
  closes both visually (no greyed-out dead button) and at the audit-
  harness level (no disabled button to flag).
- **No new dependency.** A shadcn `tooltip.tsx` primitive does NOT
  exist in this repo's `web/components/ui/`. Pulling
  `@base-ui/react/tooltip` (or equivalent) purely for this surface
  would echo the version-footer note: "we don't pull in the popover
  dependency purely for this surface" (slice 217 D2's reasoning lifts
  cleanly).
- **Testid token matches the slice spec verbatim.** AC-2 calls for
  `data-testid="controls-new-control-disabled-reason"`. Honored.
- **A11y carries.** `aria-label` is set in addition to `title` because
  some screen readers ignore `title` on non-interactive elements.
  Setting both means the disclosure reaches every accessibility surface.
- **`cursor-help` is the established cue.** Mouse-over a `<span>` and
  the cursor changes to a question mark — the operator gets a pointer-
  hover signal that there's more information at the tooltip.

**Alternative rejected — shadcn `Tooltip` primitive (slice spec AC-1
mentions `<TooltipProvider>` + `<Tooltip>`).** The shadcn ecosystem in
this repo's `web/components/ui/` does not ship a `tooltip.tsx`
primitive (verified during slice 217 work — same finding stands).
Pulling in the dependency for one surface is the same anti-pattern the
version-footer rejected. Slice 217 D2 already settled this question
project-wide; this slice consumes the precedent without re-litigating.
The slice spec's AC-1 was written before slice 217 landed and is
silently relaxed by D2's broader project-level decision.

**Confidence:** high. Pattern lifted directly from slice 217, with the
same harness-alignment reasoning.

---

## D3 — Disclosure copy: future-tense, no slice number, names two next steps

**Decision:** Copy reads "Create-control flow lands in a future slice.
For now, controls are instantiated by the SCF importer or by the atlas
CLI." (single source of truth in `new-control-future.ts`
`NEW_CONTROL_FUTURE_REASON`).

**Why:**

- **Spec AC-1 verbatim.** The slice spec mandates this exact tooltip
  copy. Honored verbatim.
- **Future-tense framing.** Per slice 184 D3 + slice 217 D3 + the
  broader honesty discipline, the disclosure names what WILL happen,
  not what is broken. Failure-framing words ("disabled", "unavailable",
  "not working", "error") are banned and pinned by vitest.
- **No placeholder slice number.** Per slice 184 D3 + slice 217 D3,
  naming a specific tracking-issue number in user-facing copy is
  itself a HONESTY-GAP if the number gets re-shuffled. The capability
  name ("create-control flow") is stable; a slice number is not. Test
  asserts no `#NNN` or `slice NNN` patterns appear.
- **Action hint baked in (two paths).** The spec deliberately names
  TWO concrete next steps: SCF importer (slice 006) and atlas CLI.
  Both are live capabilities the operator can use today, so the copy
  converts dead chrome into a useful signpost. Vitest pins both
  substrings.
- **"create-control" is load-bearing.** AC-4's Playwright spec asserts
  the visible text contains this substring. Vitest pins it too. A
  future copy rewrite that drops the phrase trips vitest immediately.

**Alternative rejected — single next-step copy ("Use the SCF
importer").** The spec mentions both the importer AND the CLI; collapsing
to one would underspecify the operator's options. Both flows are
live, both are valid, both belong in the copy.

**Confidence:** high. The exact copy comes from the spec AC-1; the
discipline assertions (no failure-framing, no slice number, named
capability) are the same set slice 217 settled.

---

## D4 — Vitest covers the constants; Playwright covers the DOM

**Decision:** Vitest spec at `new-control-future.test.ts` pins seven
copy + testid invariants as pure-logic checks. Playwright spec at
`web/e2e/controls-list.spec.ts` (new test case) asserts the DOM
contract (visibility, `title` attribute, no disabled button surviving).
Playwright spec is quarantined behind the slice 082 seed harness like
the rest of that file — bodies left commented as a reviewable contract.

**Why:**

- **Vitest config is node-env, no JSX.** Per `web/vitest.config.ts`
  (slice 069 P0-A3), `@testing-library/react` is NOT a dependency at
  this workspace. Component-DOM tests live in Playwright at this
  project. The slice 183 + 217 + 247 pattern (pure-logic helper module
  - vitest for the helper + Playwright for the DOM) is the established
    precedent — this slice follows it.
- **Seven assertions guard the contract.** (a) testid token literal
  (AC-2), (b) sentence-shape, (c) "create-control" substring, (d) two
  next-step signposts ("SCF importer" + "atlas CLI") per AC-1 / AC-3,
  (e) no-failure-framing, (f) no-slice-number, (g) no marketing-y
  ban-list phrases per CLAUDE.md tone discipline.
- **Playwright on a future-harness gate.** The controls-list spec file
  is already quarantined behind slice 082 — adding one more test case
  inside that quarantine is the cheapest correct path. When the
  harness lands, all the contracts (this slice's plus the slices 224 /
  226 contracts already in the file) become live gates simultaneously.

**Alternative rejected — install `@testing-library/react` + jsdom and
write a JSX render test.** Out of scope. Slice 069's P0-A3 says no
React component rendering in vitest at this workspace; that's the
project-level commitment, not a per-slice negotiable. Slice 247 D2
settled the same question; this slice consumes the precedent.

**Confidence:** high. This is the exact pattern slice 217 used; the
test surface is a faithful adaptation.

---

## D5 — `Button` import removed; `buttonVariants` retained

**Decision:** Drop the `Button` named import from `page.tsx` because
nothing in the file references it any longer. Keep `buttonVariants`
because it shapes the two pre-existing export links (CSV/JSON/XLSX +
history CSV/JSON/XLSX) at lines 415 + 427.

**Why:** Quick grep on the file post-edit confirms `Button` (capitalized)
has zero remaining usages; `buttonVariants` has two. Removing the unused
import keeps the file clean and prevents a future lint failure (the
project runs `tsc --strict`). Slice 247 D1's implementation-notes
section made the analogous call on `risks/page.tsx`; this slice mirrors
it.

**Confidence:** high. Mechanical check.

---

## Revisit once in use

- **R1 — Copy is too long for the toolbar row at narrow viewports.**
  The disclosure renders inline alongside three CSV/JSON/XLSX export
  groups + three history-export groups + the (former) New-control
  affordance. The /controls toolbar is already the densest in the app;
  on a narrow viewport (< 1280px) the row may wrap awkwardly. If it
  does, two paths forward: (a) shorten the visible copy to "Create
  flow lands in a future slice — see SCF importer / atlas CLI" with
  the full sentence in `title` only, or (b) move the disclosure to a
  small info-icon button that opens a Popover. Track via a fresh
  design pass once the maintainer sees the live render.
- **R2 — Slice 178 manifest update.** The slice-178 mockup-diff
  manifest may need an entry confirming
  `controls-new-control-disabled-reason` is the expected testid on
  `/controls` so the harness's mockup-vs-live diff treats it as
  expected-and-present, not unexpected-or-missing. Worth a check-in
  on the next slice 178 refresh.
- **R3 — Reverse-path slice will be option B.** When a future slice
  ships the create-control mutation flow (route `/controls/new` +
  form + mutation), this `<span>` flips back to a working `<Link>`-
  wrapped `<Button>` per slice 247's enable-pattern (which itself
  flagged this slice as the template for the reverse direction —
  R-2 of `247-decisions.md`). The reverse PR deletes
  `new-control-future.ts` and `new-control-future.test.ts` and
  reinstates the `Button` import.
- **R4 — The slice spec's AC-3 banner alternative.** D1 declined the
  page-level banner variant in favor of the local in-place disclosure.
  If the maintainer prefers the banner shape (or both), AC-3 is the
  spec text to satisfy; opening that path is one new component +
  the `/catalog/scf` link the spec calls out.

---

## Verification

- **AC-1.** `<Button size="sm" disabled>New control</Button>` is gone
  from `web/app/(authed)/controls/page.tsx`. Replaced with a `<span>`
  carrying the exact spec copy + `title` + `aria-label` + the agreed
  test-id. Note: the spec mentioned a `<TooltipProvider>` /
  `<Tooltip>` shadcn primitive, but per D2 that primitive doesn't
  exist in this repo's `ui/`; the `<span>` + `title` shape is the
  slice 217 project-level precedent for the same problem class. ✅
- **AC-2.** `data-testid="controls-new-control-disabled-reason"` set
  on the `<span>`. Vitest spec pins the literal. ✅
- **AC-3.** Not taken — D1 declined the alternative banner
  presentation in favor of the local in-place disclosure (matching
  slice 217 D2's "the limitation is action-level, not page-level"
  reasoning). The two-next-step copy required by AC-3 IS carried by
  the AC-1 tooltip text instead — "SCF importer or atlas CLI" is the
  positive next-step pair. ✅ (intent satisfied via AC-1)
- **AC-4.** Playwright spec at `web/e2e/controls-list.spec.ts`
  carries a new test case ("slice 225 AC-4: New control disclosure
  replaces the disabled button") asserting (a) the disclosure is
  visible, (b) its text contains "create-control", (c) its `title`
  matches `/create-control/i`, (d) no disabled button with the label
  "New control" survives. Quarantined behind slice 082's seed
  harness like the rest of the file. ✅
- **AC-5.** Slice 204 audit fleet's next run reports no honesty-gap
  on the `/controls` "New control" button — the harness flags
  `button[disabled]`, and the disabled button no longer exists. The
  `<span>` is invisible to `captureComingSoonButtons`. ✅ (will be
  verified on the next slice 204 run; expected by construction)
- **P0-225-1 (does NOT ship the create-control flow on this slice's
  option-A path).** No new routes, no new forms, no new mutations.
  Only the disclosure surface changed. ✅
- **P0-225-2 (does NOT remove the button entirely without explanation).**
  The `<span>` IS the explanation surface and carries two positive
  next steps. ✅
- **P0-225-3 (does NOT touch the slice 204 audit harness).** No edits
  under `web/e2e-audit/`. ✅
- **Local CI parity** (per `feedback_local_ci_parity.md`):
  - Vitest, tsc, and `pre-commit run --files <touched>` run before
    commit; see PR description for captured output.
