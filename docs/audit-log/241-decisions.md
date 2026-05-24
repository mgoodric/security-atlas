# Slice 241 — Policies header CTA affordances (build-time decisions)

> JUDGMENT-type slice. Engineer made the build-time calls per the
> `JUDGMENT` slice-development pattern (CLAUDE.md "AI-assist boundary":
> this is about how we build, NOT how the shipped product behaves). The
> product still never publishes audit-binding artifacts without one-
> click human approval — this slice is chrome.

---

## D1 — Path B (label-honesty) chosen over Path A (wire up either CTA)

**Decision:** Apply the label-honesty `<span>` shape (slice 217
pattern) to BOTH disabled buttons. Defer building either capability
(`/policies/new` form, in-app acknowledgment-report generation) until
they each get their own dedicated slices.

**Why:**

- **Destinations do not exist.** Verified by directory listing:

  - `web/app/(authed)/policies/new/` — does NOT exist.
  - No acknowledgment-report-generation route exists anywhere on
    `main` (`grep -r "acknowledgment.report" web/app/(authed)/` returns
    only references to the per-policy ack-rate column shipped by
    slice 107).

  The slice 247 enable-via-`<Link>` shape (which slice 247 applied to
  the formerly-disabled "New risk" button by routing to the already-
  shipped `/risks/new` from slice 105) is not available here — its
  precondition ("destination route already exists") fails for both
  CTAs.

- **Scope match.** The slice spec estimates 0.5d for BOTH buttons
  combined. Path A would require shipping two new in-app surfaces (a
  policy-create form + an ack-report generation pipeline) and at
  minimum (a) `POST /v1/policies` already exists from slice 022 but
  the form UI is non-trivial — needs body_md editor, version
  bumping, owner_role picker, scope_id picker; (b) ack-report
  generation has no backend endpoint at all today. That is at least
  three more slices, far outside this slice's budget.
- **Path B is the slice doc's default.** The spec explicitly names
  the tooltip / label-honesty path as acceptable (AC-1: "EITHER
  removed from the action area OR rendered with a tooltip that
  explains the gap"). Removal would shrink the toolbar's visual
  rhythm in a place where the user has been seeing two CTAs since
  slice 101; the labelled `<span>` preserves the rhythm AND
  closes the gap.
- **Reversibility.** Each `<span>` is reversible in one PR when its
  destination ships: the `<span>` is replaced with a working
  `<Link>` (slice 247 shape) or a working `<Button onClick>`, and
  the relevant constant pair retires from `header-cta-future.ts`.
  When BOTH ship, the entire `header-cta-future.ts` module deletes
  in the second of the two PRs.

**Alternative rejected (Path A, build inline):** scope-blown for both
CTAs. The slice doc's P0-241-1 ("does NOT ship a policy-create
wizard") and P0-241-2 ("does NOT ship an acknowledgment-report
generation surface") explicitly forbid it.

**Alternative rejected (remove the buttons entirely):** the slice
doc's AC-1 allows it ("EITHER removed from the action area OR
rendered with a tooltip"). Removal is cheaper but loses the
signposting affordance — operators who saw the buttons since slice
101 would now see no trace of the capability the platform plans to
ship. The label-honesty `<span>` preserves the discoverability while
making the inertness honest.

**Spillover filed:** Not in this PR. Building `/policies/new` is
the alternate path the slice doc explicitly defers; the maintainer
can file it via the standard idea-to-slice flow when prioritized.
Same for the ack-report generation surface.

**Confidence:** high. The slice 217 / 242 precedent is doing most of
the work here; both surfaces are documented and exactly mirror the
shape this slice applies.

---

## D2 — Shape: non-button `<span>` with `title` + `aria-label`, in-place

**Decision:** Each replacement affordance is a `<span>` carrying both
`title` and `aria-label` attributes (same copy on both, same copy in
the visible text). Test-id tokens:
`policies-ack-report-future` and `policies-new-policy-future`. CSS:
`cursor-help` plus muted-italic styling so they visually read as
informational chrome, not clickable buttons. Both spans sit IN-PLACE
in the same `actions` row of the page header, alongside the working
slice-138 CSV/JSON/XLSX export trio.

**Why:**

- **Slice 217 precedent.** The `/audits` page uses this exact pattern
  for the formerly-disabled "Export OSCAL bundle" button (see
  `web/app/(authed)/audits/oscal-export-future.ts` + `audits/page.tsx`
  lines 392-399). Re-using the established vocabulary keeps the
  codebase coherent.
- **Honesty harness alignment.** Slice 178's
  `captureComingSoonButtons` heuristic (`web/e2e-audit/lib/
heuristics.ts`) specifically flags `button[disabled]` whose text
  matches a coming-soon pattern. A `<span>` is invisible to that
  heuristic, which is the correct behavior: the disclosure IS the
  affordance, so the gap is closed — both visually (no greyed-out
  dead button) and at the audit-harness level (no disabled button
  to flag).
- **No new dependency.** A shadcn `tooltip.tsx` primitive does NOT
  exist in this repo's `web/components/ui/`. The version-footer
  module set the precedent: "we don't pull in `@base-ui/react/
popover` purely for this surface". Same reasoning applies here.
- **A11y carries.** `aria-label` is set in addition to `title`
  because some screen readers ignore `title` on non-interactive
  elements. Setting both means the disclosure reaches every
  accessibility surface.
- **In-place beats banner.** Slice 184 chose a page-level Alert
  banner for its page-level limitation (rows are not clickable);
  slice 217 chose an in-place `<span>` for its action-level
  limitation (one specific button). Slice 241's gap is also
  action-level — two specific buttons, both in the same toolbar
  row. The in-place pattern matches the granularity of the
  limitation.

**Alternative rejected — shadcn `Popover` with a small explainer
panel.** The Popover primitive doesn't exist in this repo's `ui/`
either, and pulling it in for two surfaces is the same anti-pattern
the version-footer rejected.

**Alternative rejected — shadcn `Alert` banner above the table.**
That is the slice 184 pattern for page-level limitations. Slice 241's
limitations are action-level — pulling them up to a page banner would
overstate the scope and leave the buttons themselves un-replaced.

**Confidence:** high. Pattern lifted directly from slice 217, with
the same harness-alignment reasoning.

---

## D3 — Disclosure copy: future-tense, no slice number, action-hinted

**Decision:** Two single-source-of-truth constants in
`header-cta-future.ts`:

- **Ack-report:** `"The in-app acknowledgment report ships with a
future slice — until then, per-policy ack rates surface in the
Acknowledgment column on this page."`
- **New-policy:** `"The in-app policy-create form ships with a future
slice — until then, policies can be drafted via the platform API
(POST /v1/policies)."`

**Why:**

- **Future-tense framing.** Per slice 184 D3 / slice 217 D3 / slice
  242 D3, the disclosure names what WILL happen, not what is broken.
  Failure-framing words ("disabled", "unavailable", "not working",
  "error") are banned and pinned by vitest.
- **No placeholder slice number.** Per slice 184 D3, naming a
  specific tracking-issue number in user-facing copy is itself a
  HONESTY-GAP if the number gets re-shuffled or the slice's scope
  shifts. The capability names ("acknowledgment report", "policy-
  create form") are stable; slice numbers are not. Tests assert no
  `#NNN` or `slice NNN` patterns appear.
- **Action hint baked in for both.**
  - Ack-report: points the operator at the per-policy
    Acknowledgment column already shipped by slice 107 + 238.
    Today, ack data IS surfaced — just not as a generated report.
    The disclosure converts dead chrome into a signpost to the
    working surface that's two pixels below the button.
  - New-policy: points the operator at the platform API endpoint
    `POST /v1/policies` (shipped by slice 022). This is the SAME
    next-action that slice 242's empty-state disclosure names —
    cross-slice coherence: both surfaces (this header CTA and the
    empty-state) point the operator at the same working API
    endpoint until the in-app form ships. When the form does ship,
    both retire together.
- **"acknowledgment report" + "policy-create form" are load-bearing.**
  AC-4's Playwright spec asserts the visible text contains the
  capability phrase. Vitest pins both substrings. A future copy
  rewrite that drops either phrase trips vitest immediately.
- **"POST /v1/policies" is load-bearing for the new-policy
  disclosure.** Vitest pins it — the action hint is the difference
  between dead chrome and a signpost.

**Alternative rejected — "Coming soon".** Pure cliché. No information
content. The slice 178 honesty heuristic itself flags this pattern as
a placeholder.

**Alternative rejected — "Acknowledgment report ships in v2".** Too
vague. "v2" is a calendar concept that may slip; the capability name
is concrete.

**Confidence:** medium on exact wording (small subjective call); high
on the substance (future-tense + capability-named + action-hinted).

---

## D4 — Vitest covers the constants; Playwright covers the DOM

**Decision:** Vitest spec at `header-cta-future.test.ts` pins twelve
copy + testid invariants as pure-logic checks across both
disclosures. Playwright spec at `web/e2e/policies-list.spec.ts` (two
new test cases) asserts the DOM contract (visibility, `title`
attribute, no disabled button surviving). Playwright spec is
quarantined behind the slice 082 seed harness like the rest of that
file — bodies left commented as a reviewable contract.

**Why:**

- **Vitest config is node-env, no JSX.** Per `web/vitest.config.ts`
  (slice 069 P0-A3), `@testing-library/react` is NOT a dependency at
  this workspace. Component-DOM tests live in Playwright at this
  project. The slice 183 / 217 / 242 pattern (pure-logic helper
  module + vitest for the helper + Playwright for the DOM) is the
  established precedent.
- **Coverage shape.** Twelve vitest assertions guard for BOTH
  surfaces:
  - testid token literals (AC-4) — one per surface.
  - sentence-shape of the copy — one per surface.
  - load-bearing capability substring — one per surface.
  - no-failure-framing discipline — one per surface.
  - no-slice-number discipline — one per surface.
  - "POST /v1/policies" signpost on the new-policy disclosure (slice
    242 D2 precedent).
  - testid distinctness across the two surfaces (no collision).
- **Playwright on a future-harness gate.** The policies-list spec
  file is already quarantined behind slice 082 — adding two more
  test cases inside that quarantine is the cheapest correct path.
  When the harness lands, all the contracts (this slice's plus the
  slice 238 / 242 contracts already in the file) become live gates.

**Alternative rejected — install `@testing-library/react` + jsdom and
write a JSX render test.** Out of scope. Slice 069's P0-A3 says no
React component rendering in vitest at this workspace; that's the
project-level commitment, not a per-slice negotiable.

**Confidence:** high. This is the exact pattern slice 217 and 242
used.

---

## D5 — `Button` import retired from `page.tsx`

**Decision:** Drop the `Button` import from `page.tsx` (keep
`buttonVariants`).

**Why:** Before slice 241, `Button` was imported on line 66 and used
on lines 387 + 390 (the two now-removed disabled buttons). Verified
by `grep -n "Button\b" page.tsx`: the only two usages were the two
buttons being removed. Removing the import keeps the file clean and
avoids a tsc lint warning. `buttonVariants` stays — it's used on line
380 to style the CSV/JSON/XLSX export anchors.

**Confidence:** high. Quick grep, two surviving usages, both removed.

---

## D6 — Other disabled-CTA patterns to apply this decision to (going-forward audit)

**Decision:** Per AC-3 D3, name other disabled-CTA patterns in the
codebase that this slice's decision should apply to going forward.

**Findings:**

- **`/policies` is now CLEAN** — this slice was the last lying-CTA
  on the policies page.
- **`/audits` was CLEANED by slice 217** — same pattern, same shape.
- **`/risks` was CLEANED by slice 247** — different shape (enable-
  via-Link because destination existed), same honesty discipline.
- **`/controls` was CLEANED by slice 225** — silent disabled "New
  control" button retired.
- **`/audits` ROW-CLICK was CLEANED by slice 184** — page-level Alert
  banner because the limitation was page-level, not action-level.
- **`/risks` ROW-CLICK was CLEANED by slice 185** — explicit per-row
  link + page-level Alert banner.

**Going-forward rule (this slice's contribution to the pattern
language):**

When a CTA is permanently disabled because the destination doesn't
exist yet, apply the following decision tree:

1. **Does the destination route already exist?** → enable the
   button via a `<Link>` wrapper around `buttonVariants(...)`
   (slice 247 pattern).
2. **Does the destination NOT exist?** → replace the button with a
   `<span>` carrying `title` + `aria-label` + a stable testid +
   `cursor-help` styling (slice 217 / 241 pattern).
3. **Is the limitation page-level (not action-level)?** → use a
   shadcn `Alert` banner above the table (slice 184 / 185 pattern).

This rule is documented here, in slice 217's decisions log, and in
slice 247's decisions log. Future slices that touch disabled CTAs
should reference one of these three logs as the precedent.

**Confidence:** medium. The rule is consolidated from three slices'
worth of judgment; it may need refinement when applied to
surfaces with multiple inert affordances + a working destination
hidden somewhere.

---

## Revisit once in use

- **R1 — Copy length at narrow viewports.** Two disclosures + three
  export-button groups in the same toolbar row may wrap awkwardly at
  < 1280px. If the maintainer sees a poor render, two paths forward:
  (a) shorten the copy to a 2-3 word label with the full sentence in
  `title` only (e.g. "Ack report — future slice"); (b) move the
  disclosure to a small info-icon button that opens a Popover (would
  require pulling in the Popover primitive — out of scope for this
  slice).
- **R2 — Slice 178 manifest update.** The slice-178 mockup-diff
  manifest may need entries confirming `policies-ack-report-future`
  and `policies-new-policy-future` are the expected testids on
  `/policies` so the harness's mockup-vs-live diff treats them as
  expected-and-present, not unexpected-or-missing. Worth a check-in
  on the next slice 178 refresh.
- **R3 — Path A may become viable sooner than expected.** If a
  maintainer files the `/policies/new` slice and ships the in-app
  policy-create form, the `<span>` for new-policy here flips to a
  `<Link>` (slice 247 pattern) and the `POLICIES_NEW_POLICY_*`
  constants delete from `header-cta-future.ts`. When the ack-report
  surface ships, same flip for the ack-report disclosure. When BOTH
  ship, the entire `header-cta-future.ts` module deletes.
- **R4 — Cross-page disclosure inventory.** If a maintainer wants
  to audit the codebase for disabled-CTA debt going forward, the
  three-pattern decision tree in D6 is the right starting point.
  A `grep -rn "<Button[^>]*disabled" web/app/` is the right tool.

---

## Verification

- **AC-1.** Both `Acknowledgment report` and `New policy` buttons
  are rendered with a label-honest disclosure (Path B chosen, see
  D1). Visible copy reads as the future-tense sentence in each
  span. Disabled `<Button>` elements with these labels no longer
  exist in `web/app/(authed)/policies/page.tsx`. ✅
- **AC-2.** Tooltip path chosen → each `<span>` carries `title` +
  `aria-label` (both set to the same disclosure text). Tab focus is
  not applicable to non-interactive `<span>` — but the disclosure
  IS the visible text, so all of screen-reader, pointer-hover, and
  visual surfaces carry the same content. ✅
- **AC-3.** Decisions log lives at
  `docs/audit-log/241-decisions.md` with D1 (path chosen) + D2
  (shape) + D3 (copy) + D4 (test split) + D5 (Button import) + D6
  (going-forward audit pattern). ✅
- **AC-4.** Vitest spec (`header-cta-future.test.ts`) pins twelve
  invariants across both disclosures. Playwright spec
  (`web/e2e/policies-list.spec.ts`) carries two new test cases —
  one per disclosure — asserting (a) the disclosure is visible,
  (b) its text contains the load-bearing capability substring,
  (c) its `title` matches the same regex, (d) no disabled button
  with the original label survives. Quarantined behind slice 082's
  seed harness like the rest of the file. ✅
- **AC-5 (out of scope).** The Export CSV/JSON/XLSX trio (slice 138) is untouched. The page diff is exactly two `<Button
disabled>` → `<span>` swaps + one import edit + the explanatory
  block comment. The `actions =` JSX still renders the three
  export anchors first. ✅
- **AC-6.** Pre-commit clean run + DCO sign-off + Co-Authored-By
  trailer — captured in PR description. ✅
- **P0-241-1 (no policy-create wizard shipped).** N/A — Path A
  explicitly deferred. Vitest's no-failure-framing test pins that
  the disclosure never reads "disabled". ✅
- **P0-241-2 (no ack-report generation surface shipped).** N/A —
  Path A explicitly deferred. ✅
- **P0-241-3 (slice 138 exports unchanged).** Neither the
  `policies-export-buttons` block nor the three `policies-export-
{csv,json,xlsx}` anchors were touched. ✅
- **P0-241-4 (no vendor-prefixed fixture tokens).** N/A — this
  slice has no test fixtures. ✅
- **Local CI parity** (per `feedback_local_ci_parity.md`):
  - `npx vitest run 'app/(authed)/policies/'` — 83 / 83 green
    (12 new + 71 pre-existing).
  - `npx tsc --noEmit` — zero errors in touched files
    (`policies/page.tsx`, `policies/header-cta-future.ts`,
    `policies/header-cta-future.test.ts`, `e2e/policies-list.spec.ts`).
    Pre-existing tsc errors in `scripts/capture-readme-screenshots.
test.ts` are out of scope (NODE_ENV ProcessEnv type drift,
    untouched by this slice).
  - `pre-commit run --files <touched>` — run before commit, see PR
    description for the captured output.
