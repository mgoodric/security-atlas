# Slice 361 — A11y global-search combobox ARIA wiring — decisions log

**Parent slice:** 331 (`docs/audit-log/331-a11y-wcag-audit-decisions.md`) finding **A11Y-3** (severity High)
**Branch:** `frontend/361-a11y-global-search-combobox`
**Type:** JUDGMENT (per `Plans/prompts/04-per-slice-template.md` slice types)
**Date:** 2026-05-28

This slice closes the High-severity finding A11Y-3 from slice 331's
WCAG audit: the global ⌘K search at
`web/components/shell/global-search.tsx` does not implement the
WAI-ARIA 1.2 combobox-listbox pattern. Screen-reader users get no
programmatic announcement that results are available, no count, no
indication that ArrowDown navigates, and no way to know which option
is currently highlighted.

This log captures the build-time judgment calls.

---

## D1 — Path 1 (Link-keep) over Path 2 (canonical `<li role="option">`)

**Decision:** **Path 1.** Keep the existing `<Link>` rendering each
row; add `id={optionIdFor(type, hit.id)}` next to the already-present
`role="option"` + `aria-selected`. Accept the `<Link>`-as-option
semantic stretch.

**Rationale.**

1. **P0-361-1 compliance.** The slice doc's first anti-criterion is
   "does NOT change the visual / interaction shape of the popover —
   ARIA + ids only." Path 2 (replacing `<Link>` with `<li role="option">`
   carrying a click handler that calls `router.push()`) loses
   `<Link>`'s native cmd-click / right-click semantics — power users
   who routinely cmd-click a search result to open the row's detail
   page in a new tab would lose that capability. Re-implementing
   those semantics on a `<li>` (manually detecting `metaKey` /
   `ctrlKey` / `button === 1` and falling back to `window.open` for
   middle-click) is non-trivial and a clear interaction-shape change.
   Path 1 honors the anti-criterion cleanly.

2. **WAI-ARIA does not forbid it.** The WAI-ARIA 1.2 Authoring
   Practices recommend `<li role="option">` as the canonical option
   shape, but the spec itself does not require it. `role="option"`
   on an interactive descendant (`<a>` / `<Link>`) is allowed when
   the option is reachable via the listbox's
   `aria-activedescendant` flow — which is exactly what slice 361
   wires up. The author-recommended shape is a strong default, not
   a normative constraint.

3. **One-shot popover usage.** The global search popover dismisses
   on Escape / outside-click / Enter-to-navigate; the listbox is
   never persistently focused. The interactive-descendant concern
   (focus management ambiguity) does not apply because focus stays
   on the input throughout — ArrowDown shifts the *visible*
   highlight via `activeIndex` + `aria-activedescendant`, not the
   keyboard-tab focus. The input is the focused control; the
   listbox rows are *named*, not *focused*.

4. **No SR-user validation available.** Path 2 is the canonical
   shape per the WAI-ARIA Authoring Practices; the slice doc
   explicitly recommends Path 1 "unless an SR-user test surfaces a
   regression." No SR-user test is in scope for this slice. Ship
   Path 1; if a future audit (slice 331-style) surfaces a real
   regression on a major SR engine (NVDA, JAWS, VoiceOver,
   Orca), file a follow-up to migrate to Path 2.

5. **Reversal cost is low.** If Path 2 becomes necessary later,
   the migration is one component-internal change: swap the `<Link>`
   for `<li role="option" onClick={...}>` and wire `router.push()`
   on click. The DOM ids, the input's ARIA attributes, the listbox
   id, and the live-region all stay identical — they are the
   load-bearing wiring, not the option-element-tag choice.

**Reversal trigger:** an SR-user (NVDA / JAWS / VoiceOver) test on a
shipped build of the global search reports the popover rows are not
announced when ArrowDown highlights them, AND the cause traces to
`role="option"` on `<a>` rather than `<li>` (rule out missing
`aria-activedescendant` wiring first).

## D2 — aria-live announcement format: "X result" / "X results" / "No results"

**Decision:** Use a minimal three-branch helper
(`resultCountAnnouncement(count)`) that returns:

- `"No results"` when `count === 0`
- `"1 result"` (singular) when `count === 1`
- `"{count} results"` (plural) when `count > 1`

**Rationale.**

- **Length discipline.** Screen readers announce live-region text
  verbatim; longer strings interrupt the user's flow. The format
  matches the de facto convention used by Stripe Dashboard search
  ("3 results"), Linear's ⌘K ("12 results"), and GitHub's global
  search dropdown. Operators familiar with any of those will not
  experience cognitive friction.
- **Singular/plural matters for voice naturalness.** "1 results"
  (or "1 result(s)") is a small but noticeable parser glitch on
  screen-reader voices like VoiceOver's Samantha or NVDA's eSpeak
  cadence. The branch is one line of code and removes the glitch.
- **"No results" branch rationale.** The slice doc explicitly calls
  out the SR gap: typing → silence. The `"No results"` branch is
  what closes that gap when the search returns zero hits. The
  live-region is *only* mounted when `showPopover` is true AND
  `!loading`, so an SR user does not hear "No results" on every page
  load — only when they have typed enough to trigger a search and
  the search has come back empty.
- **Rejected alternatives:**
  - `"X results found"` — wordy; the verb adds no information.
  - `"Found X results"` — same.
  - `"X matches"` — semantically equivalent to "results" but
    "results" is the more common SR-tested phrase.
  - Per-group counts ("3 controls, 1 risk") — interesting but the
    live region would announce on every keystroke during typing,
    creating a stuttering verbose-mode flood. Could be revisited
    in a future slice if user research validates it.

## D3 — Live region placement: outside the listbox, role="status" + aria-atomic="true"

**Decision:** The `aria-live="polite"` region is a sibling of the
listbox, not a child. Carries `role="status"` (implicit
`aria-live="polite"` + AT-affordance for status-message handling)
*and* an explicit `aria-live="polite"` (defense-in-depth for older
AT engines that do not derive aria-live from role=status). Carries
`aria-atomic="true"` so the entire announcement re-fires when the
count changes (vs partial-diff announcements that some SR engines
default to).

**Rationale.**

- **Placement outside the listbox.** Live regions inside the
  listbox are announced only when the user focuses into the
  listbox. Outside-placement means the SR announces the count
  while focus stays on the input — which is the slice's whole
  point.
- **role="status" + aria-live="polite".** WCAG 4.1.3 Status
  Messages (Level AA) is satisfied by `role="status"`; the
  explicit `aria-live="polite"` is belt-and-suspenders for the
  long tail of AT engines that ship outdated ARIA resolvers.
- **aria-atomic="true".** Without it, "1 result" → "2 results"
  would announce as "2" on some SR engines (diff-only). With it,
  the full text re-fires. Cheap insurance for a noticeable UX win.
- **Gated on `showPopover && !loading`.** Mounting the region only
  when the popover is open and not in the loading flash prevents
  spurious announcements on page load + during the 250ms debounce
  window when the popover is rendering the "Searching…" state.

## D4 — Added `aria-autocomplete="list"` on the input (out of scope but free)

**Decision:** Added `aria-autocomplete="list"` to the input alongside
the four mandatory attributes the slice doc names.

**Rationale.**

- The WAI-ARIA 1.2 combobox pattern recommends `aria-autocomplete`
  to communicate the suggestion model. `"list"` is the correct
  value for the global search (a fixed list of results appears
  below the input; the input value itself is NOT auto-completed
  by the suggestions — selecting a row navigates rather than
  populating the input).
- Zero code cost; meaningful for SR users who otherwise have to
  infer the suggestion model from behavior.
- Not strictly required by the slice's ACs, but the slice doc's
  intent ("the WAI-ARIA combobox pattern") encompasses it. Surfaced
  here so the maintainer can see it was a deliberate addition, not
  drift.

## D5 — `<input>` `id` not added; `aria-controls` is one-directional only

**Decision:** The input is NOT given an `id`. The listbox is NOT
given `aria-labelledby` pointing at the input.

**Rationale.**

- The slice doc lists the ACs explicitly: input → listbox via
  `aria-controls`. It does NOT specify the reverse linkage.
- The listbox is given an explicit `aria-label="Search results"`
  instead — clearer than naming the input's placeholder ("Search
  controls, evidence, risks…") which is too long for an SR
  region-label utterance.
- Adding an `id` to the input would create the risk of two global
  searches in the same page colliding on the id. The shell renders
  a single global search; the risk is currently zero. Surface in
  the PR body so the maintainer can decide whether to add an id
  via slice 178-style harness assertion.

## D6 — No follow-up filed; engineer-as-collaborator notes for the PR body

**Decision:** No spillover slices filed. The slice doc's "Notes"
section ("the slice 178 harness could grow a combobox-wiring
assertion to prevent regression") is captured in the PR body as a
future-audit candidate. Per the engineer-as-collaborator pattern
(memory `feedback_engineer_claim_verification`) the engineer noted
two adjacent comboboxes that are likely under-wired (tenant
switcher, role/select dropdowns) but P0-361-3 prohibits widening
scope — they are surfaced in the PR body for a future audit slice.

---

## AC tracking

- [x] **AC-1.** Input carries role=combobox + aria-haspopup=listbox + aria-expanded + aria-controls + aria-activedescendant (all five attributes added at `web/components/shell/global-search.tsx`).
- [x] **AC-2.** Listbox carries stable id `global-search-listbox` matching `aria-controls` (constant `LISTBOX_ID`).
- [x] **AC-3.** Each option carries stable id `global-search-option-{type}-{id}` via `optionIdFor()`; active option's id resolves through `aria-activedescendant` (verified by Playwright spec assertion on ArrowDown).
- [x] **AC-4.** Visually-hidden `aria-live="polite"` region announces result count on update (`<div role="status" aria-live="polite" aria-atomic="true" className="sr-only">`).
- [x] **AC-5.** Existing unit tests pass; new unit tests added for `optionIdFor` / `resultCountAnnouncement` / `LISTBOX_ID`; new Playwright assertion added to `web/e2e/controls-top-bar.spec.ts`.
- [x] **AC-6.** Decisions log (this file) records Path 1 vs Path 2 + reasoning.
- [x] **AC-7.** `pre-commit run --all-files` passes (gate at end of build).

## P0 anti-criteria tracking

- [x] **P0-361-1.** No visual / interaction shape change to the popover. All edits are ARIA attributes + DOM ids + one `sr-only` live-region div.
- [x] **P0-361-2.** No new dependency. No headless-ui or similar combobox library was added.
- [x] **P0-361-3.** Scope held to global-search. Tenant-switcher / filter-pills / role-select dropdowns NOT touched (surfaced as future-audit candidates in PR body).
