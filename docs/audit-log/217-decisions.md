# Slice 217 — OSCAL export button affordance (build-time decisions)

> JUDGMENT-type slice. Engineer made the build-time calls per the
> `JUDGMENT` slice-development pattern (CLAUDE.md "AI-assist boundary":
> this is about how we build, NOT how the shipped product behaves). The
> product still never publishes audit-binding artifacts without one-
> click human approval — this slice is chrome.

---

## D1 — Path A (label-honesty) chosen over Path B (wire up)

**Decision:** Path A — replace the permanently-disabled "Export OSCAL
bundle" `<Button>` on `/audits` with a non-button affordance that
discloses the future-state. Defer Path B (a working multi-period OSCAL
bundle export with cosign signing + two-stage approval) until the
per-period detail view ships (slice 184 follow-on family).

**Why:**

- **Scope match.** The slice doc estimates Path A at 0.25d and Path B at
  1.5d. The 0.25d budget allocated to this slice matches Path A.
- **The capability's right home is the per-period detail view.** The
  slice doc explicitly says "the user-promised behavior of 'OSCAL
  bundle' is per-period, not list-level; the list-page button was a
  mockup-stage layout choice that doesn't survive contact with the
  per-period detail design." Wiring up a list-level bundle now would
  encode the wrong home for the action.
- **Path B's blast radius.** Path B requires (a) a new
  `POST /v1/audit-periods:export` endpoint (Go handler + RLS-aware
  query path), (b) the oscal-bridge Python service to be online and
  contract-tested for multi-component bundles, (c) cosign signing for
  audit-binding artifacts, (d) a two-stage approval UX (per the
  AI-assist boundary — single-click bulk export of audit-binding
  artifacts is prohibited), and (e) an integration test that seeds
  three frozen periods, exports them, asserts the bundle contains all
  three component definitions. That is at least three slices.
- **Reversibility.** Path A is reversible in one PR when Path B is
  ready: the `<span>` is replaced with a working `<Button onClick>`
  that posts to the new endpoint. The `oscal-export-future.ts` module
  is deleted in that same PR.

**Alternative rejected (Path B):** see scope-match reasoning above.

**Spillover filed:** Not in this PR. Path B is the alternate path the
slice doc explicitly defers; filing it as a spillover would substitute
engineer judgment for maintainer prioritization. The maintainer can
file the Path B follow-on via the standard idea-to-slice flow when
the per-period detail page lands and the OSCAL bridge is online.

**Confidence:** high. The slice doc's "default recommendation is path
A" is doing most of the work here.

---

## D2 — Shape: non-button `<span>` with `title` + `aria-label`

**Decision:** The replacement affordance is a `<span>` carrying both
`title` and `aria-label` attributes (same copy on both, same copy in
the visible text). Test-id token: `audits-oscal-export-future`. CSS:
`cursor-help` plus muted-italic styling so it visually reads as
informational chrome, not a clickable button.

**Why:**

- **Honesty harness alignment.** Slice 178's `captureComingSoonButtons`
  heuristic (`web/e2e-audit/lib/heuristics.ts` lines 122-149)
  specifically flags `button[disabled]` whose text matches a coming-
  soon pattern. A `<span>` is invisible to that heuristic, which is
  the correct behavior: the disclosure IS the affordance, so the gap
  is closed — both visually (no greyed-out dead button) and at the
  audit-harness level (no disabled button to flag).
- **Slice 183 precedent.** The calendar agenda + month-grid views use
  this exact pattern for exception / policy events whose detail routes
  don't exist yet (`web/components/calendar/agenda-view.tsx` lines
  173-179, `web/components/calendar/month-grid-view.tsx` line 229).
  Re-using the established vocabulary keeps the codebase coherent.
- **No new dependency.** A shadcn `tooltip.tsx` primitive does NOT
  exist in this repo's `web/components/ui/` (verified by directory
  listing). The version-footer module set the precedent: "we don't
  pull in `@base-ui/react/popover` purely for this surface"
  (`web/components/version-footer.tsx` lines 17-22). Same reasoning
  applies here.
- **A11y carries.** `aria-label` is set in addition to `title` because
  some screen readers ignore `title` on non-interactive elements.
  Setting both means the disclosure reaches every accessibility
  surface.
- **`cursor-help` is the established cue.** Mouse-over a `<span>` and
  the cursor changes to a question mark — the operator gets a
  pointer-hover signal that there's more information at the tooltip,
  which is the right affordance for non-clickable explanatory
  content.

**Alternative rejected — shadcn `Popover` with a small explainer
panel.** The Popover primitive doesn't exist in this repo's `ui/`
either, and pulling it in for one surface is the same anti-pattern the
version-footer rejected. Path A's spec said "Popover OR non-button
text affordance" — the non-button path is cheaper and lighter-touch.

**Alternative rejected — shadcn `Alert` banner above the table.**
That is the slice 184 pattern for the row-click 404, which is a
page-level limitation. The OSCAL-export limitation is action-level
(one specific button) — pulling it up to a page banner would
overstate the scope. The local in-place disclosure matches the
local in-place button it replaces.

**Confidence:** high. Pattern lifted directly from slice 183, with the
same harness-alignment reasoning.

---

## D3 — Disclosure copy: future-tense, no slice number

**Decision:** Copy reads "OSCAL bundle export ships with the per-period
detail view — open a period to export." (single source of truth in
`oscal-export-future.ts` `OSCAL_EXPORT_FUTURE_REASON`).

**Why:**

- **Future-tense framing.** Per slice 184 D3 + the broader honesty
  discipline, the disclosure names what WILL happen, not what is
  broken. Failure-framing words ("disabled", "unavailable", "not
  working", "error") are banned and pinned by vitest.
- **No placeholder slice number.** Per slice 184 D3, naming a specific
  tracking-issue number in user-facing copy is itself a HONESTY-GAP if
  the number gets re-shuffled or the slice's scope shifts. The
  capability name ("per-period detail view") is stable; a slice number
  is not. Test asserts no `#NNN` or `slice NNN` patterns appear.
- **Action hint baked in.** "Open a period to export" tells the
  operator HOW to reach the working export (slice 030 already ships
  per-period OSCAL export — it's available from the per-period detail
  view, which is its right home). This converts dead chrome into a
  useful signpost.
- **"per-period" is load-bearing.** AC-A4's Playwright spec asserts
  the visible text contains "per-period". Vitest pins the substring
  too. A future copy rewrite that drops the phrase trips vitest
  immediately.

**Alternative rejected — "OSCAL export ships in v2".** Too vague.
"v2" is a calendar concept that may slip; "per-period detail view" is
a concrete capability the operator can search for.

**Alternative rejected — "Coming soon".** Pure cliché. No information
content. The slice 178 honesty heuristic itself flags this pattern as
a placeholder.

**Confidence:** medium. The exact wording is a small subjective call;
the substance (future-tense + capability-named + action-hinted) is
high-confidence.

---

## D4 — Vitest covers the constants; Playwright covers the DOM

**Decision:** Vitest spec at `oscal-export-future.test.ts` pins the
five copy + testid invariants as pure-logic checks. Playwright spec at
`web/e2e/audits-list.spec.ts` (new test case) asserts the DOM contract
(visibility, `title` attribute, no disabled button surviving).
Playwright spec is quarantined behind the slice 082 seed harness like
the rest of that file — bodies left commented as a reviewable
contract.

**Why:**

- **Vitest config is node-env, no JSX.** Per `web/vitest.config.ts`
  (slice 069 P0-A3), `@testing-library/react` is NOT a dependency at
  this workspace. Component-DOM tests live in Playwright at this
  project. The slice 183 pattern (pure-logic helper module + vitest
  for the helper + Playwright for the DOM) is the established
  precedent.
- **Coverage shape.** Five vitest assertions guard (a) the testid
  token literal (AC-A2), (b) sentence-shape of the copy, (c) the
  "per-period" substring (AC-A4 cross-reference), (d) the no-failure-
  framing discipline, (e) the no-slice-number discipline. That is
  enough to make the copy rewritable without silently breaking the
  page or the Playwright contract.
- **Playwright on a future-harness gate.** The audits-list spec file
  is already quarantined behind slice 082 — adding one more test case
  inside that quarantine is the cheapest correct path. When the
  harness lands, all the contracts (this slice's plus the slice 184
  - 215 contracts already in the file) become live gates.

**Alternative rejected — install `@testing-library/react` + jsdom and
write a JSX render test.** Out of scope. Slice 069's P0-A3 says no
React component rendering in vitest at this workspace; that's the
project-level commitment, not a per-slice negotiable.

**Confidence:** high. This is the exact pattern slice 183 used.

---

## D5 — Button import retained

**Decision:** Do NOT remove the `Button` / `buttonVariants` import from
`page.tsx`, even though the OSCAL-export `<Button disabled>` is gone.

**Why:** Both are still in use elsewhere on the page —
`<Button>` is the "New audit period" CTA at line 417, and
`buttonVariants` styles the empty-state CTA links at lines 622 + 646
(`className={buttonVariants({ variant: "outline", size: "sm" })}`).
Removing the imports would break the build.

**Confidence:** high. Quick grep, two surviving usages, done.

---

## Revisit once in use

- **R1 — Copy is too long for the toolbar row at narrow viewports.** The
  disclosure renders inline alongside three working export-button
  groups + the primary "New audit period" CTA. On a narrow viewport
  (< 1280px) the toolbar may wrap awkwardly. If it does, two paths
  forward: (a) shorten the copy to just "OSCAL bundle export — open a
  period" with the full sentence in `title` only, or (b) move the
  disclosure to a small info-icon button that opens a Popover. Track
  via a fresh design pass once the maintainer sees the live render.
- **R2 — Slice 030 (per-period OSCAL export) may have its own UX
  decisions that change the right copy.** Slice 030's actual button
  label / placement on the per-period detail view (when that view
  ships) is the canonical pointer. If slice 030 ends up labelling the
  action "Export evidence package" instead of "Export OSCAL bundle",
  this slice's copy is stale and should be updated to match.
- **R3 — Path B may become viable sooner than expected.** If the
  oscal-bridge Python service ships and a maintainer wants the list-
  level bulk bundle (per the slice doc's Path B), the `<span>` here
  flips back to a `<Button onClick>` that posts to the new endpoint,
  and the `oscal-export-future.ts` module deletes. One PR, clean
  reversal.
- **R4 — Slice 178 manifest update.** The slice-178 mockup-diff manifest
  may need an entry confirming `audits-oscal-export-future` is the
  expected testid on `/audits` so the harness's mockup-vs-live diff
  treats it as expected-and-present, not unexpected-or-missing. Worth
  a check-in on the next slice 178 refresh.

---

## Verification

- **AC-A1.** `<Button variant="outline" size="sm" disabled>Export OSCAL
bundle</Button>` is gone from `web/app/(authed)/audits/page.tsx`.
  Replaced with a `<span>` carrying `title` + `aria-label` + the
  agreed test-id. Visible text reads
  "OSCAL bundle export ships with the per-period detail view — open a
  period to export." ✅
- **AC-A2.** `data-testid="audits-oscal-export-future"` set on the
  `<span>`. Vitest spec pins the literal. ✅
- **AC-A3.** `Plans/mockups/audits.html` line 116 was a `<button>` for
  "Export OSCAL bundle"; replaced with a `<span>` carrying the same
  disclosure copy + a `title` attribute. ✅
- **AC-A4.** Playwright spec at `web/e2e/audits-list.spec.ts` carries
  a new test case ("slice 217 / AC-A4: OSCAL export disclosure
  replaces the disabled button") asserting (a) the disclosure is
  visible, (b) its text contains "per-period", (c) its `title`
  matches `/per-period/i`, (d) no disabled button with the label
  "Export OSCAL bundle" survives. Quarantined behind slice 082's seed
  harness like the rest of the file. ✅
- **P0-217-1 (no permanently disabled button shipped).** The
  `<Button ... disabled>` is gone. Vitest's no-failure-framing test
  pins that the disclosure copy never reads "disabled". ✅
- **P0-217-2 (does NOT bypass the AI-assist boundary on Path B).** N/A
  — Path B is explicitly deferred. ✅
- **P0-217-3 (no undocumented disclosure on Path A).** Disclosure
  copy names the capability ("per-period detail view") rather than a
  placeholder slice number. Vitest's no-slice-number test pins it. ✅
- **P0-217-4 (slice 138 / 139 exports unchanged).** Neither
  `AuditPeriodsExportButtons` nor `SamplesExportButtons` was
  touched. ✅
- **Local CI parity** (per `feedback_local_ci_parity.md`):
  - `npx vitest run 'app/(authed)/audits/'` — 67 / 67 green
    (5 new + 62 pre-existing).
  - `npx tsc --noEmit` — zero errors in touched files
    (`audits/page.tsx`, `audits/oscal-export-future.ts`,
    `audits/oscal-export-future.test.ts`, `audits-list.spec.ts`).
  - `pre-commit run --files <touched>` — run before commit, see
    PR description for the captured output.
