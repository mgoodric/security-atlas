# 218 — UI honesty: board-pack detail breadcrumb chain missing

**Cluster:** frontend
**Estimate:** 0.5d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced during slice 204 audit fleet (board-pack page), captured as
follow-up per continuous-batch policy.

The mockup top bar
(`Plans/mockups/board-pack.html` lines 27–33) ships a three-segment
breadcrumb chain — `Sentinel Labs › Board reports › Q1 2026` — that
gives the operator a clear location signal inside the nested
board-pack hierarchy. The live detail-page `ExportBar`
(`web/components/board-pack/export-bar.tsx`) renders only a single
back-link `← All packs` at the left edge of the sticky bar.

On the detail page the user has no chrome cue beyond the page title
that they are inside a specific board pack (vs. the list view), and
no path to higher-order navigation (e.g., "Board reports"). Adding a
minimal breadcrumb (`Board packs › <period_label>`) closes the chrome
parity gap without inventing nav segments the app does not have
(there is no parent "Board reports" landing page distinct from
`/board-packs`).

## Threat model

**Verdict.** **no-mitigations-needed.** Pure presentational change in
the sticky top bar — no auth, no data-fetch, no RLS surface.

## Acceptance criteria

- **AC-1.** The detail page top bar renders a breadcrumb chain with
  at least two segments: `Board packs` (linking to `/board-packs`)
  and the current pack's period label (e.g. `Q1 2026`,
  derived from `periodLabel(periodEnd)` already exported by
  `pack-header.tsx`).
- **AC-2.** The existing `← All packs` link is either removed (in
  favor of the breadcrumb) or moved to coexist without redundancy.
- **AC-3.** A Playwright spec (`web/e2e/board-pack-detail.spec.ts`
  or equivalent) asserts the breadcrumb segments are rendered and
  the parent link routes to `/board-packs`.
- **AC-4.** The existing slice-178 audit harness does not surface a
  new HONESTY-GAP finding on `/board-packs/[id]` after this slice
  merges.

## Constitutional invariants honored

- Anti-pattern rejected: chrome that ships unverifiable location
  segments (no fake `Sentinel Labs` tenant-name segment unless the
  tenant name is loaded from the session).
- Slice 204 spillover discipline: one finding, one slice.

## Canvas references

- `Plans/canvas/07-metrics.md` — board reporting first-class
- `Plans/mockups/board-pack.html` — mockup parity target

## Dependencies

- **#204** (UI parity audit fleet) — `in-progress`. Surfacing
  parent; not a build-time prerequisite.
- **#043** (board-pack detail view) — `merged`. The page this slice
  modifies.

## Anti-criteria (P0 — block merge)

- **P0-218-1.** Does NOT modify the board-pack data wire shape or
  backend.
- **P0-218-2.** Does NOT fabricate breadcrumb segments — every
  segment links to a real route or is plain text derived from
  pack data.
- **P0-218-3.** Does NOT touch the export buttons or publish flow.

## Skill mix (3-5)

1. Next.js App Router — sticky-bar layout primitives
2. shadcn/ui breadcrumb component (or hand-rolled equivalent)
3. Playwright spec authoring
