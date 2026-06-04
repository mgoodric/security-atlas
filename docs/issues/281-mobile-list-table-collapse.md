# 281 — Mobile-aware list-table collapse (`<ListTable>` → card-stack at `< md`)

**Cluster:** Frontend
**Estimate:** 1d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced during slice 277, captured as follow-up per continuous-batch
policy. The slice 277 audit
([`docs/responsive-audit.md`](../responsive-audit.md)) records `no`
verdicts for three top-traffic list pages at 375px viewport width:

- `/controls` — `<ListTable>` with ~7 columns horizontal-scrolls; the
  primary affordance (anchor click) requires scrolling right to reach.
- `/risks` — `<ListTable>` with severity / treatment / owner / age
  columns horizontal-scrolls at 375px.
- `/evidence` — `<ListTable>` with provenance / kind / observed_at /
  actor columns. Same shape.

All three pages compose the same primitive
(`web/components/list/list-table.tsx`). The fix lives at the primitive
layer: a `mobileMode="cards"` prop (or similar) that at `< md` collapses
each table row into a card with the column labels as field labels
(`<dt>`/`<dd>` semantically). At `≥ md` the existing table rendering is
unchanged (the slice 277 P0-277-1 desktop-UX-no-regression invariant
extends to this slice).

Slice 056's hierarchical risk dashboard establishes the pattern: at
`< md` the table collapses to a card stack, one card per row, each cell
rendered as a label/value pair inside the card. This slice extends the
same shape to the `<ListTable>` primitive that powers `/controls`,
`/risks`, `/evidence` (and several others identified in the audit doc).

### What ships in this slice

1. **`<ListTable>` mobile-mode prop** — `mobileMode?: "table" | "cards"`
   (default `"table"` for backward-compat with non-list pages). At
   `< md` with `mobileMode="cards"`, each row renders as a `<div>` card
   with the column headers as inline labels.
2. **Per-page wiring** — `/controls` + `/risks` + `/evidence` pass
   `mobileMode="cards"`. The audit doc is re-verdicted from `no` → `yes`
   for these three rows on the same PR.
3. **Vitest coverage** — the rendering branch is pure logic; tests pin
   the card vs table shape based on the prop. No new Playwright
   scenarios — the slice 277 e2e mobile-baseline spec extends to add
   one assertion per page (3 new assertions, not a separate spec file).
4. **Audit-doc update** — three `no` rows flip to `yes`. Slice 281 row
   added to the "Spillover slices filed" section showing `merged`.

### Scope discipline (deliberately OUT)

- Other tables (`/policies` / `/vendors` / `/exceptions` / etc.) — the
  audit doc rows above stay `partial`. Each follow-on lands as its own
  slice. This slice's load-bearing claim is "the priority three pages
  get the fix"; broader application is per-page slice fan-out.
- Touch-friendly multi-select / sort affordances on the card stack —
  that's a UX polish slice, not the baseline collapse.
- Admin table card-collapse — admin routes are deliberately desktop-
  first per the slice 277 audit verdict.

## Threat model

Pure-presentational frontend change. Same threat surface as slice 277:
zero new authn/authz/RLS/audit-log surfaces, RLS unchanged, no new
endpoint. STRIDE pass: **no-mitigations-needed.**

## Acceptance criteria

- [ ] **AC-1.** `<ListTable>` accepts `mobileMode?: "table" | "cards"`.
      Default `"table"` preserves the slice 277 / pre-281 rendering at
      every viewport.
- [ ] **AC-2.** With `mobileMode="cards"` and viewport width `< md`,
      each row renders as a card (CSS card; `<dl>` or similar semantic
      shape) with column header as field label + cell content as field
      value.
- [ ] **AC-3.** With `mobileMode="cards"` and viewport width `≥ md`,
      the existing `<table>` rendering is byte-identical to today. **No
      desktop UX regression.** (P0-281-1.)
- [ ] **AC-4.** `/controls` passes `mobileMode="cards"`.
- [ ] **AC-5.** `/risks` passes `mobileMode="cards"`.
- [ ] **AC-6.** `/evidence` passes `mobileMode="cards"`.
- [ ] **AC-7.** Vitest coverage pins the prop-conditional rendering.
- [ ] **AC-8.** Slice 277's `web/e2e/mobile-baseline.spec.ts` extended
      with one assertion per page: at 375px, the card-stack shape
      renders (a known cell renders as a labelled field, not a `<td>`).
- [ ] **AC-9.** [`docs/responsive-audit.md`](../responsive-audit.md)
      re-verdicted: three rows flip from `no` → `yes`.
- [ ] **AC-10.** CHANGELOG entry under `## [Unreleased]` → `### Changed`.

## Constitutional invariants honored

- **Invariant 6 (tenant isolation via RLS).** Frontend-only;
  presentation layer. No RLS path touched.
- **AI-assist boundary.** None.
- **No fabrication.** Card-stack mirrors the canonical table content.

## Dependencies

- **#277** (mobile-responsive baseline) — must be `merged`. Slice 281
  consumes the breakpoint discipline + audit-doc shape 277 establishes.

## Anti-criteria (P0 — block merge)

- **P0-281-1.** Does NOT regress desktop UX at any width `≥ md`. The
  existing `<table>` rendering at desktop is unchanged.
- **P0-281-2.** Does NOT introduce a separate `<MobileListTable>`
  component. Single primitive with a prop.
- **P0-281-3.** Does NOT add a new top-level dependency.
- **P0-281-4.** Does NOT touch the `<ListTable>` consumers beyond
  `/controls`, `/risks`, `/evidence`. Other consumers stay on the
  default `mobileMode="table"`; they get per-page slices.
- **P0-281-5.** Does NOT change the wire shape (props that consumers
  pass to row renderers). Only the OUTER table-vs-cards switch.

## Skill mix (3-5)

1. Tailwind v4 breakpoint discipline (slice 277's rubric)
2. shadcn-style primitive evolution (additive prop, default preserves
   behavior)
3. Vitest pure-component coverage
4. Playwright extending an existing spec rather than spawning a new one

## Provenance

Filed 2026-05-25 by the slice 277 implementer per the continuous-batch
spillover-as-slice policy. The three pages flagged `no` at 375px
viewport were the load-bearing finding of the slice 277 audit; they
share a primitive and a one-prop fix, so they cluster as a single
follow-on rather than three separate slices. Subsequent per-page
card-collapse slices (admin tables, policies, vendors, etc.) land
individually.
