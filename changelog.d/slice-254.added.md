- **frontend:** slice 254 — control-detail tab strip on `/controls/{id}`.
  Re-folders the page behind a sticky seven-tab strip (Overview /
  Evidence / Mappings / Effective scope / Policies / Risks / History)
  per `Plans/mockups/control.html` lines 139-152. Tab state encodes
  in the URL via `?tab=<key>` (search param — see
  [`docs/audit-log/254-tabs-decisions.md`](docs/audit-log/254-tabs-decisions.md)
  D1); the default tab is Overview and unrecognised keys fall through
  to it. Count chips read from each tab's underlying TanStack Query
  payload, rendering `—` while loading or errored (AC-2) and
  comma-formatted full numbers when resolved (D3). Anti-criterion
  P0-254-3 is preserved verbatim: the Overview tab's data layout is
  identical to the pre-tab-strip page, and every data-testid from
  slices 041 / 152 / 253 / 255 / 256 / 257 carries over unchanged so
  the surrounding test suites (and the slice 255 / 256 / 257 specs)
  keep working. Six dedicated per-tab panels host the deep-dive
  views the mockup names; each shares the same query the Overview
  right-rail consumes (no new platform endpoints, no new BFF routes,
  no new shared component primitives — P0-254-1 + P0-254-2). Ships
  with a sibling vitest unit-test for the pure tab-key validator +
  count formatter (15 new tests, 1084 → 1099 totals) and a live
  Playwright e2e spec covering AC-1 (seven tabs in mockup order),
  AC-2 (chip counts), AC-8 (URL hydration + refresh + canonical
  Overview cleanup + unrecognised-key fallthrough), AC-9 (keyboard
  Tab walks tab buttons in DOM order), and the P0-254-3 layout-
  preservation assertion. The new spec uses Playwright auto-waiting
  `expect(...).toBeVisible()` and `expect(page).toHaveURL()`
  exclusively — no debounce polling — per the slice 274 + 223
  pattern. See [`docs/issues/254-control-detail-tab-strip.md`](docs/issues/254-control-detail-tab-strip.md).
