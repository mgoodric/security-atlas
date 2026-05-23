# 219 — UI honesty: board-pack header "Author" cell hardcoded to em-dash

**Cluster:** frontend
**Estimate:** 0.5d (option A — drop the cell) · 1.5d (option B — record + render author)
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced during slice 204 audit fleet (board-pack page), captured as
follow-up per continuous-batch policy.

`web/components/board-pack/pack-header.tsx` line 62 hardcodes the
"Author" meta-cell to `value="—"`:

```tsx
<MetaCell label="Author" value="—" />
```

The mockup (`Plans/mockups/board-pack.html` line 70) populates the
same cell with `Sam Rivera (CISO)`. The board-pack backend record
(`internal/board/pack.go`) has no `author` field — there is no way
for the live page to populate that cell honestly, so the slice-043
author left it hardcoded to em-dash rather than removing the
label. This is a small honesty gap: the cell promises an "Author"
the system cannot produce, and the em-dash placeholder reads as
"data is missing" rather than "this field is not modeled".

The fix has two paths:

- **Option A (0.5d).** Remove the "Author" cell from the 4-cell
  meta strip and ship a 3-cell strip (Period end, Generated,
  Approver). No backend change. Smallest, lowest-risk path.

- **Option B (1.5d).** Add an `author` column to the board-pack
  table (the session identity that POSTed `/v1/board-packs`),
  surface it in the pack JSON, and render it in the cell. Carries
  one additive migration and one wire-shape extension.

Defaulting AC shape to Option A — Option B is filable as a
follow-on if the maintainer prefers to record authorship.

## Threat model

**Verdict.** **no-mitigations-needed.** Option A is purely a chrome
change. Option B records the JWT subject already-attached to the
generate request — no new data surface.

## Acceptance criteria (Option A — chosen path)

- **AC-1.** `pack-header.tsx` renders a 3-cell meta strip:
  `Period end`, `Generated`, `Approver`. The hardcoded
  `<MetaCell label="Author" value="—" />` is removed.
- **AC-2.** Existing PackHeader test
  (`web/components/board-pack/pack-header.test.tsx` if present,
  or the closest equivalent) is updated to assert the 3-cell
  shape.
- **AC-3.** Existing Playwright board-pack-detail spec asserts
  that no `Author` label is rendered.
- **AC-4.** Slice 178 audit harness does not surface a new
  HONESTY-GAP finding on `/board-packs/[id]` after merge.

## Constitutional invariants honored

- Anti-pattern rejected: data-bound surfaces that lie. The em-dash
  placeholder reads as "author exists but we don't know who" when
  in fact author is not modeled.
- Slice 204 spillover discipline.

## Canvas references

- `Plans/canvas/07-metrics.md` — board reporting first-class

## Dependencies

- **#204** (UI parity audit fleet) — `in-progress`. Surfacing
  parent; not a build-time prerequisite.
- **#043** (board-pack detail view) — `merged`. The component
  this slice modifies.
- **#032** (quarterly board pack) — `merged`. Defines pack record
  shape; would need extension for Option B only.

## Anti-criteria (P0 — block merge)

- **P0-219-1.** Does NOT add fake author data (e.g., the session
  identity of the viewer rather than the generator).
- **P0-219-2.** Option A does NOT modify backend pack record or
  migration set.
- **P0-219-3.** Does NOT remove the `Approver` cell — that one
  IS data-backed (`pack.published_by`).

## Skill mix (3-5)

1. React component refactor (drop one cell, regrade grid)
2. Vitest component test update
3. Playwright spec update
