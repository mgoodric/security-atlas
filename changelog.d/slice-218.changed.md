- **frontend:** slice 218 — board-pack detail breadcrumb chain. The
  sticky export bar at `/board-packs/[id]` now opens with a 2-segment
  breadcrumb (`Board packs` → period label) at the left edge,
  replacing the slice-043 `← All packs` link (semantically identical
  to the breadcrumb's parent segment — keeping both would be redundancy
  AC-2 warned against). New
  `web/components/board-pack/pack-breadcrumb-segments.ts` exposes
  `periodLabel(periodEnd)` (hoisted out of `pack-header.tsx` as the
  canonical implementation, now shared by both surfaces) plus
  `packBreadcrumbSegments(periodEnd)`; new
  `web/components/board-pack/pack-breadcrumb.tsx` renders the nav
  chrome. Two of the mockup's three segments (`Sentinel Labs` +
  `Board reports`) are deliberately dropped — the first was a
  fake-tenant fixture name, the second a dead-anchor parent route
  that doesn't exist on main (P0-218-2). `Plans/mockups/board-pack.html`
  updated to match (slice 217 D2 / AC-A3 paired-edit pattern).
  Vitest spec (11 cases) pins segment shape + period-label parsing +
  no-fabricated-segment regression guards; new Playwright spec
  `web/e2e/board-pack-detail.spec.ts` asserts the DOM contract,
  quarantined behind the slice 082 seed harness like the rest of the
  e2e directory. Pure presentational chrome — no backend or wire-shape
  change. Decisions log at
  [`docs/audit-log/218-decisions.md`](docs/audit-log/218-decisions.md).
