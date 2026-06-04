# 222 — Board-pack posture coverage-definition caption missing

**Cluster:** frontend
**Estimate:** 0.5d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced during slice 204 audit fleet (board-pack page), captured as
follow-up per continuous-batch policy.

The mockup §01 closes with a small footer paragraph
(`Plans/mockups/board-pack.html` lines 146–148):

> "Coverage definition: weighted SCF-anchored evidence pass rate
> intersected with each framework's scope predicate, over the
> period. Methodology unchanged from prior quarter."

This methodology disclosure has audit-trail value — board members
and external auditors both benefit from a one-line statement of
how the headline coverage number is computed (board members get
context; auditors get methodology assurance). It also keys directly
into constitutional invariant 5 (FrameworkScope intersects with
control applicability).

The live `PostureTiles` component
(`web/components/board-pack/posture-tiles.tsx`) renders the four
framework tiles but no methodology caption. The section card
container has no trailing caption either.

This is a small omission with disproportionate value: a single
sentence below the tiles closes the gap and explicitly documents
the methodology the platform actually applies.

## Threat model

**Verdict.** **no-mitigations-needed.** Pure presentational change
in the posture section card — no auth, no data-fetch, no RLS
surface. The text is static and constitutionally accurate.

## Acceptance criteria

- **AC-1.** The posture section (rendered by `SectionCard` wrapping
  `PostureTiles`) renders a trailing caption below the tiles
  reading: "Coverage definition: weighted SCF-anchored evidence
  pass rate intersected with each framework's scope predicate,
  over the period."
- **AC-2.** The caption is styled consistently with other
  section captions (small text, slate-500 tone, matches mockup
  visual density).
- **AC-3.** The caption renders only for the `posture` section;
  other sections are unaffected.
- **AC-4.** A Vitest or Playwright spec asserts the caption is
  rendered on the posture section.
- **AC-5.** Slice 178 audit harness surfaces no new HONESTY-GAP
  finding on `/board-packs/[id]` after merge.

## Constitutional invariants honored

- **Invariant 5 (FrameworkScope intersects with control
  applicability).** The methodology sentence explicitly names the
  intersection — making the load-bearing invariant visible to the
  reader.
- Anti-pattern rejected: vanity dashboards. The caption is a
  methodology disclosure, not a marketing claim.
- Slice 204 spillover discipline.

## Canvas references

- `Plans/canvas/05-scopes.md` §5.5 — FrameworkScope intersection
- `Plans/canvas/07-metrics.md` — board reporting first-class
- `Plans/mockups/board-pack.html` lines 146–148 — caption source

## Dependencies

- **#204** (UI parity audit fleet) — `in-progress`. Surfacing
  parent; not a build-time prerequisite.
- **#043** (board-pack detail view) — `merged`. The page this
  slice modifies.

## Anti-criteria (P0 — block merge)

- **P0-222-1.** Does NOT add the caption to non-posture sections.
- **P0-222-2.** Does NOT modify the coverage methodology itself
  (this slice documents what the platform does; it does not
  change behavior).
- **P0-222-3.** Does NOT add per-framework methodology variants —
  one caption, all frameworks.

## Skill mix (3-5)

1. React component refactor — append static caption to PostureTiles
   or SectionCard
2. Vitest component test or Playwright spec update
3. Tailwind small-text styling for parity with other captions
