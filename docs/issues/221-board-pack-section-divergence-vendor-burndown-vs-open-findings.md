# 221 — Board-pack section divergence: vendor-burndown (mockup) vs open-findings (live)

**Cluster:** frontend (option A — update mockup) · board (option B — add backend section)
**Estimate:** 0.5d (option A) · 2.5d (option B)
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced during slice 204 audit fleet (board-pack page), captured as
follow-up per continuous-batch policy.

The mockup (`Plans/mockups/board-pack.html`) ships seven board-pack
sections in this order:

1. §01 Program posture
2. §02 Top risks · aging
3. §03 Control coverage trend
4. §04 Operational metrics
5. §05 Investment vs coverage
6. **§06 Vendor risk burndown**
7. §07 Asks of the board

The live `BOARD_PACK_SECTION_KEYS` constant (`web/lib/api.ts` line
1707, mirroring `internal/board/pack.go SectionKeys`) ships these
seven sections in this order:

1. `posture`
2. `top_risks`
3. `coverage_trend`
4. **`open_findings`**
5. `operational_metrics`
6. `investment`
7. `asks`

Two divergences:

- Live has `open_findings`; mockup does not.
- Mockup has `vendor_burndown`; live does not.

The implementations and mockup have drifted apart. This is a real
parity gap — the mockup describes a vendor-burndown panel that the
v1 product does not ship, and the v1 product ships an open-findings
panel the mockup does not preview.

The fix has two paths:

- **Option A (0.5d).** Update `Plans/mockups/board-pack.html` to
  match the v1 implementation:

  - Drop §06 "Vendor risk burndown".
  - Add a new section after §03 (or after §07, whichever flows
    better) titled "Open findings" with a small findings table
    matching `web/components/board-pack/findings-list.tsx`.
  - Renumber the section headings.

- **Option B (2.5d).** Ship a `vendor_burndown` section in the
  backend pack generator (slice 032 plumbing — add the section
  key, the SectionData shape, the templated text generator, the
  component, the spec coverage). The vendor module already has
  the data (`/v1/vendors/burndown` exists per slice 122) so this
  is a wire-up exercise, not a data-modeling exercise.

Defaulting AC shape to Option A — Option B is filable as a
follow-on if the maintainer decides vendor burndown belongs in the
board pack.

## Threat model

**Verdict.** **no-mitigations-needed.** Option A is a mockup file
edit. Option B reads existing vendor-burndown data through the
existing tenant-scoped endpoint — no new RLS surface, no new
external IO.

## Acceptance criteria (Option A — chosen path)

- **AC-1.** `Plans/mockups/board-pack.html` removes §06 "Vendor
  risk burndown" entirely.
- **AC-2.** `Plans/mockups/board-pack.html` inserts a new "Open
  findings" section in the canonical position (after §03 coverage
  trend, before §04 operational metrics) so the mockup order
  matches `BOARD_PACK_SECTION_KEYS`.
- **AC-3.** Section heading numbers are renumbered to keep §01–§07
  contiguous.
- **AC-4.** A new comment block in the mockup HTML notes that
  section order is the authoritative list from `BOARD_PACK_SECTION_KEYS`
  and references slice 032 as the source of truth.

## Constitutional invariants honored

- Anti-pattern rejected: mockup-stale text. The mockup should
  preview the v1 product, not a divergent design.
- Slice 204 spillover discipline.

## Canvas references

- `Plans/canvas/07-metrics.md` — board reporting first-class
- `Plans/mockups/board-pack.html` — the file being edited
- `docs/audit-log/032-quarterly-board-pack-decisions.md` — section
  set authority

## Dependencies

- **#204** (UI parity audit fleet) — `in-progress`. Surfacing
  parent; not a build-time prerequisite.
- **#032** (quarterly board pack) — `merged`. Authoritative section
  set source.
- **#043** (board-pack detail view) — `merged`. Section components
  the mockup is being aligned to.
- **#122** (vendor burndown endpoint) — `merged`. Only relevant for
  Option B.

## Anti-criteria (P0 — block merge)

- **P0-221-1.** Does NOT modify the live `BOARD_PACK_SECTION_KEYS`
  set or backend pack generator in Option A.
- **P0-221-2.** Does NOT add new sections beyond the seven canonical
  keys.
- **P0-221-3.** Does NOT delete the §07 "Asks of the board" section —
  that one IS in the live set; only §06 is dropped.

## Skill mix (3-5)

1. HTML + Tailwind (mockup file edit)
2. Cross-reference hygiene (the mockup is now annotated with the
   source-of-truth pointer)
