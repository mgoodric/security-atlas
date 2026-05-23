# 220 — Mockup update: board-pack coverage trend is scalar-only in v1

**Cluster:** frontend
**Estimate:** 0.5d (option A — update mockup) · 3.0d (option B — ship time series)
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced during slice 204 audit fleet (board-pack page), captured as
follow-up per continuous-batch policy.

The mockup §03 (`Plans/mockups/board-pack.html` lines 224–298) ships a
rich coverage-trend visual: a multi-line SVG chart over six tick
marks (Jan 6 → today) with three framework lines (SOC 2, ISO 27001,
NIST CSF), plus three per-framework 90-day delta cards (`+7.4 pts`,
`+12.8 pts`, `+4.6 pts`).

The live `CoverageTrend` component
(`web/components/board-pack/coverage-trend.tsx`) renders only three
scalar cards (Baseline / Current / Quarter delta). The component
header comment explicitly documents the gap:

> "The slice-032 coverage_trend section carries scalar fields:
> coverage_pct, baseline_coverage_pct, coverage_delta. There is
> no per-framework time series on the server — fabricating one
> would violate the anti-pattern of made-up data."

That downgrade is honest and consistent with the CLAUDE.md
anti-pattern against fabricated data, but the mockup is now stale
relative to the v1 wire shape. The fix has two paths:

- **Option A (0.5d).** Update `Plans/mockups/board-pack.html` §03
  to depict the v1 scalar reality — three cards (Baseline /
  Current / Quarter delta) instead of the SVG line chart. Cheapest
  path; aligns the design artifact with shipped reality.

- **Option B (3.0d).** Extend the backend's `coverage_trend`
  section to carry a per-framework time series
  (`coverage_history: [{framework_slug, observed_at, coverage_pct}]`),
  populate it from the existing posture-snapshot reader (slice
  066), and render the chart against real data. Heavier;
  requires a migration if the series is materialized, or a join
  if computed at generate-time.

Defaulting AC shape to Option A — Option B is filable as a
follow-on once a maintainer decides the chart is worth the
backend cost.

## Threat model

**Verdict.** **no-mitigations-needed.** Option A edits a docs/HTML
mockup file under `Plans/mockups/`. Option B reads from existing
posture-snapshot tables (slice 066) — no new external IO, no new
RLS surface, all queries already tenant-scoped.

## Acceptance criteria (Option A — chosen path)

- **AC-1.** `Plans/mockups/board-pack.html` §03 is rewritten to
  show three scalar cards (Baseline / Current / Quarter delta)
  matching the live `CoverageTrend` component.
- **AC-2.** The mockup's per-framework 90-day delta cards are
  removed (or replaced with a single "Quarter delta" card).
- **AC-3.** A new comment block in the mockup HTML notes the
  scalar-only v1 shape and references slice 043 decision D2 +
  this slice as the audit log.
- **AC-4.** No live-code changes — the mockup is the design
  artifact being aligned to reality.

## Constitutional invariants honored

- Anti-pattern rejected: fabricating data (per-framework time
  series the v1 server does not produce). Slice 043 already
  rejected this at the component level; this slice extends the
  same rejection to the mockup file.
- Slice 204 spillover discipline.

## Canvas references

- `Plans/canvas/07-metrics.md` — board reporting first-class,
  coverage methodology
- `Plans/mockups/board-pack.html` §03 — the file being edited

## Dependencies

- **#204** (UI parity audit fleet) — `in-progress`. Surfacing
  parent; not a build-time prerequisite.
- **#043** (board-pack detail view) — `merged`. The component
  whose v1 shape the mockup is being aligned to.
- **#032** (quarterly board pack) — `merged`. The section data
  shape being referenced.

## Anti-criteria (P0 — block merge)

- **P0-220-1.** Does NOT fabricate per-framework time series
  data in code (slice 043 decision D2 stays in force).
- **P0-220-2.** Does NOT delete the §03 mockup section entirely —
  rewrite it, don't drop it.
- **P0-220-3.** Does NOT modify other mockup sections beyond §03.

## Skill mix (3-5)

1. HTML + Tailwind (mockup file edit)
2. Markdown documentation hygiene (audit-log cross-reference)
