# 220 — Mockup update: board-pack coverage trend → scalar-only

**Slice:** `docs/issues/220-mockup-update-board-pack-coverage-trend-scalar-only.md`
**Type:** AFK (decisions log captured at orchestrator request — not a JUDGMENT slice)
**Branch:** `mockup/220-board-pack-mockup-update`
**Parent:** slice 204 (UI parity audit fleet, board-pack page audit)

## Decisions made

### D1 — Choose Option A (mockup-aligned-to-reality) over Option B (ship time series)

- **Chosen.** Option A — rewrite `Plans/mockups/board-pack.html` §03 to depict
  the three scalar cards (Baseline / Current / Quarter delta) that the live
  `web/components/board-pack/coverage-trend.tsx` component already ships.
- **Considered.** Option B — extend the backend's `coverage_trend` section to
  carry a per-framework time series, populate it from the posture-snapshot
  reader (slice 066), and render a chart against real data. ~3.0d of work,
  with a migration if materialized or a join if computed at generate-time.
- **Rationale.** The slice spec explicitly defaults to Option A. Option A
  aligns the design artifact to shipped reality (the binary v1 success
  criterion), is the cheapest path, and respects slice 043 decision D2
  (which rejected fabricated per-framework series at the component level).
  Option B is a v2 candidate and is callable as a separate slice when a
  maintainer decides the chart is worth the backend cost — the new
  comment block in the mockup file flags it as such.
- **Confidence.** high. Spec defaulted to A; B was explicitly out of scope.

### D2 — Card shape mirrors the live React component exactly

- **Chosen.** Three cards in a `grid-cols-3`: Baseline (muted), Current
  (primary), Quarter delta (emerald-tinted). No per-framework breakdown.
  Caption "As of 2026-03-31" in the header (mirrors the mockup's existing
  quarter framing `2026-01-01 → 2026-03-31`).
- **Considered.** A sparkline-with-scalar placeholder under the cards.
  Rejected — there is no series to sparkline against, and a placeholder
  sparkline would invite the same fabrication anti-pattern at design-review
  time. A textual "as of" caption + the v2 follow-up note in the
  explanatory paragraph carries the same forward-looking intent without
  the visual lie.
- **Considered.** Stripping the legend (the three coloured framework dots
  at the top of the section). Done — the legend has no referent once the
  multi-line chart is gone, so it would be visual noise.
- **Rationale.** Mockups are reference artifacts for design + product
  conversations. The closer the mockup tracks shipped reality, the lower
  the cost of every future "is this what the page actually does?" check.
  Pattern-matched to slice 043's existing scalar-card layout — the mockup
  now reads as a 1:1 design source for the production component.
- **Confidence.** high. Mirrors a live, merged component.

### D3 — Add an HTML comment block citing slice 043 D2 + slice 220 as the audit trail

- **Chosen.** A multi-line `<!-- ... -->` block above the `<section>`,
  capturing: (a) the v1 wire shape (three scalars, no series), (b) the
  reason fabrication is rejected (CLAUDE.md AI-assist boundary + canvas
  §1.6 anti-pattern), (c) the live component path, and (d) the fact that
  the chart can be reinstated if a `coverage_history` series ships.
- **Rationale.** AC-3 of the slice spec requires the cross-reference. An
  in-file comment is the cheapest discoverable form — anyone editing the
  mockup §03 in future will read why before reverting to a chart.
- **Confidence.** high. Direct AC requirement.

## Revisit once in use

- **R1.** If/when a backend `coverage_history` series ships (Option B from
  the slice spec), restore a chart in §03 alongside (or in place of) the
  scalar cards. The HTML comment block flags this future state and is the
  trigger for re-opening the design.
- **R2.** Re-evaluate whether the "As of 2026-03-31" caption belongs in
  the header (current placement) or as a small annotation under the
  "Current" card. The header placement reads naturally for a quarterly
  pack; in a monthly/ad-hoc context it may feel duplicative with the
  cover-page period range.
- **R3.** Slice 043's component (`coverage-trend.tsx`) has a paragraph of
  explanatory text below the cards explaining baseline/delta methodology.
  The mockup now mirrors that text. If the component copy changes,
  re-sync the mockup paragraph.

## Files touched

- `Plans/mockups/board-pack.html` — replace §03 chart + per-framework
  delta cards with three scalar cards mirroring `CoverageTrend` component;
  add explanatory HTML comment block + v2 follow-up note.
- `docs/audit-log/220-decisions.md` — this file.

## Anti-criteria honored

- **P0-220-1.** No fabricated per-framework time-series data in code —
  the slice does not touch any code path. Slice 043 decision D2 stays in
  force.
- **P0-220-2.** §03 mockup section was rewritten, not deleted — the
  section header, anchor, and surrounding sections (§02, §04) are
  untouched.
- **P0-220-3.** No other mockup sections were modified.

## Constitutional invariants honored

- Anti-pattern rejection (canvas §1.6): no fabricated control / coverage
  data shipped through the design artifact.
- AI-assist boundary (CLAUDE.md): irrelevant in this slice — no AI-assist
  surface is touched.
- Mockup-as-reference, not as production (CLAUDE.md "Editing Plans/ vs
  editing code"): the design artifact is being aligned to the system of
  record (live component), not the other way around.
