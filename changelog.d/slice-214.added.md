- **frontend:** slice 214 — sidebar item count badges (Controls +
  Risks). Closes the parity gap surfaced by slice 204's audit fleet:
  the mockup at `Plans/mockups/audits.html` (lines 63-76) shows
  right-aligned mono badges on the `Controls` row (muted "82" — total
  count) and the `Risks` row (rose "3" — high-severity tier). The live
  shared sidebar (`web/components/shell/sidebar.tsx`) rendered bare
  text. This slice adds two leaf client components in a new module
  `web/components/shell/sidebar-counts.tsx` (`ControlsCountBadge`,
  `RisksCountBadge`), both driven by TanStack Query against the
  existing `/api/controls` + `/api/risks` BFFs (P0-214-1: no new
  platform endpoint). 60s `staleTime` + 60s `refetchInterval` per
  AC-3; subtle `animate-pulse` only during the refresh tick. Renders
  null on loading / error / zero count (P0-214-2: silent absence over
  a `0` badge). The Risks badge counts severity ≥ 15 (the canonical
  "high" / rose tier from slice 100's `severityBand`) — the
  maintainer JUDGMENT call (recorded in
  `docs/audit-log/214-sidebar-item-counts-decisions.md` D1) is that
  the spec's "open critical" phrasing maps to this tier because the
  risk wire shape carries neither a `status` column nor a `critical`
  band. Pure helper `countHighSeverityRisks` factored out and unit-
  tested in `sidebar-counts.test.ts` (6 cases pinning the boundary
  - empty + mixed list paths). Playwright spec
    `web/e2e/audits-header.spec.ts` extended with two assertions: the
    Controls badge appears on `/audits` via the shared sidebar
    (proves end-to-end wiring + shared-shell coverage) and the badge
    consumes the existing `/api/controls` BFF (proves P0-214-1). Slice
    186's role-conditional admin gate is unchanged — NAV items just
    gained an optional `slot` ReactNode and Controls/Risks rows mount
    the badges into it.
