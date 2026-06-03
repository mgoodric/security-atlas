# 361 — A11y global-search combobox ARIA wiring

**Cluster:** Frontend / a11y
**Estimate:** 1d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Slice 331's a11y audit
(`docs/audits/331-a11y-wcag-audit.md` finding A11Y-3, severity
High) surfaced that the global ⌘K search at
`web/components/shell/global-search.tsx` (lines 122-441) does not
implement the WAI-ARIA 1.2 combobox pattern.

Current state:

- The popover container carries `role="listbox"` (good).
- Each result row is a `<Link>` with `role="option"` and
  `aria-selected` (good — though see note below about
  `<Link>` + `role="option"`).
- The input itself carries `aria-label="Global search"`.
- The input does NOT carry: `role="combobox"`,
  `aria-haspopup="listbox"`, `aria-expanded`, `aria-controls`,
  `aria-activedescendant`.

The screen-reader consequence: an SR user focuses the input, hears
"Global search · search box." They type. The popover opens. The SR
hears nothing — no announcement that results are available, no
count, no indication that ArrowDown will navigate. The user presses
ArrowDown; the visible highlight moves; the SR hears nothing
because no `aria-activedescendant` is set on the input. The user
presses Enter; the underlying `<Link>` navigates. From the SR's
perspective, the popover never existed.

WCAG SC 4.1.2 Name, Role, Value (Level A): "For all user interface
components, the name and role can be programmatically determined."
A combobox without a `role="combobox"` and without programmatic
linking to its listbox fails this criterion.

### What ships

1. **Input ARIA wiring.** Add to the `<input>`:

   - `role="combobox"`
   - `aria-haspopup="listbox"`
   - `aria-expanded={showPopover}`
   - `aria-controls="global-search-listbox"` (stable id)
   - `aria-activedescendant={activeOptionId}` (id of the active
     row, computed from the flat index)

2. **Listbox stable id.** Replace the `role="listbox"` div's
   anonymous mounting with a stable `id="global-search-listbox"`
   so `aria-controls` resolves.

3. **Option stable ids.** Each row gets an id of the form
   `global-search-option-{type}-{id}` so `aria-activedescendant`
   resolves to the active row.

4. **`<Link>` vs `role="option"` reconciliation.** WAI-ARIA's
   combobox-listbox pattern expects `<li role="option">` with the
   click handler driving navigation, not `<Link>` with the
   navigation built in. Two paths:

   - **Path 1 (minimal):** Keep `<Link>`; add `id` + `role="option"`

     - `aria-selected` (already present). Accept the
       `<Link>`-as-option semantic stretch. WAI-ARIA does not
       forbid it; sound for one-shot popover usage.

   - **Path 2 (canonical):** Replace the `<Link>` with a
     `<li role="option" id=...>` carrying the click handler that
     calls `router.push()`. The visible click target is the `<li>`;
     the keyboard target is the `<li>` via `aria-activedescendant`.
     `<Link>`'s native cmd-click / right-click semantics are lost;
     accept (or re-implement).

   Decisions log records the choice. Path 1 is faster; Path 2 is
   canonical. Recommend Path 1 unless an SR-user test surfaces a
   regression.

5. **Live region for result count.** When results return, mount a
   visually-hidden `aria-live="polite"` region announcing the
   result count (e.g. "12 results"). This is what closes the SR
   gap: the user typing hears "12 results" without having to
   ArrowDown into the popover.

6. **Unit + Playwright coverage.** Existing tests at
   `web/components/shell/global-search.test.ts` cover the helper
   functions. Add a Playwright spec assertion for the combobox
   ARIA wiring (Playwright can assert `aria-expanded` flips
   on input).

### Why this matters

The global search is the project's primary search affordance —
visible on every authed page, ⌘K-discoverable, the only way to
search across controls / risks / evidence. Locking SR users out
of search is a load-bearing barrier; the fix is purely additive
ARIA + ids.

## Threat model

ARIA-only change. STRIDE pass:

- **S / T / R / D / E:** No surface changes.
- **I:** None.

## Acceptance criteria

- [ ] **AC-1.** Input carries `role="combobox"` +
      `aria-haspopup="listbox"` + `aria-expanded` +
      `aria-controls` + `aria-activedescendant`.
- [ ] **AC-2.** Listbox carries a stable id matching
      `aria-controls`.
- [ ] **AC-3.** Each option carries a stable id; the active
      option's id matches `aria-activedescendant` after
      ArrowDown.
- [ ] **AC-4.** A visually-hidden `aria-live` region announces
      result count on update.
- [ ] **AC-5.** Existing unit tests pass; new Playwright spec
      asserts combobox wiring.
- [ ] **AC-6.** Decisions log records Path 1 vs Path 2 for
      `<Link>` vs canonical `<li>` and the engineer's reasoning.
- [ ] **AC-7.** `pre-commit run --all-files` passes.

## Anti-criteria (P0 — block merge)

- **P0-361-1.** Does NOT change the visual / interaction shape of
  the popover. ARIA + ids only.
- **P0-361-2.** Does NOT add a new dependency (no headless-ui
  combobox swap-in).
- **P0-361-3.** Does NOT widen scope to other comboboxes
  (tenant-switcher / filter-pills). Those are separate audits.

## Dependencies

- **#331** (a11y audit) — `merged` (closing this slice).
- **#223** (global search) — `merged`. The component foundation.
- **#268** (`/v1/search` upstream) — `merged`.

## Notes

The slice 178 harness could grow a combobox-wiring assertion to
prevent regression. File as a harness extension follow-up if
desired; not in scope here.
