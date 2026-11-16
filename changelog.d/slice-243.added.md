- **frontend:** slice 243 — `/risks` top-bar chrome parity (regression
  protection). Closes the slice 204 UI-honesty audit fleet finding on
  `/risks`. The four chrome elements the finding flagged as missing on
  `/risks` — tenant breadcrumb, audit-period pill, user avatar, and
  global ⌘K search — were all shipped in the shared authed-shell topbar
  by adjacent slices: the in-progress audit pill and user-display row
  by slice 213, and the breadcrumb + ⌘K search by slice 223. Both
  shipping slices' e2e specs cover their own routes (`/audits` +
  `/dashboard` for the pill; `/controls` + `/audits` for the breadcrumb
  and search) and slice 235 closed the same gap on `/evidence` — but
  none asserted the chrome on `/risks`. Slice 243 adds a single new
  Playwright spec at `web/e2e/risks-top-bar.spec.ts` exercising the
  three load-bearing components on `/risks`: the in-progress pill
  renders the seeded period name, the breadcrumb renders
  `Demo Tenant > Risks` (proving the `risks → Risks` entry in
  `web/lib/page-names.ts` is live), and the global search input
  renders with the mockup placeholder + ⌘K kbd hint with a working ⌘K
  focus shortcut and a debounced `/api/search` round-trip (using slice
  274's auto-waiting pattern via `page.waitForRequest`). No new
  components, no new BFF routes, no new platform endpoints, no
  source-code changes — pure regression protection for three
  load-bearing chrome elements on the one authed route that had no
  e2e coverage of them. The spec reuses the slice 213 `audits-header`
  fixture (audit period) + the slice 223 `controls-top-bar` fixture
  (tenants row) — both are idempotent so sequencing them in
  `beforeAll` adds no coupling cost. See
  [`docs/audit-log/243-risks-top-bar-regression-decisions.md`](docs/audit-log/243-risks-top-bar-regression-decisions.md)
  D1 (subset shipped — all three components were already in production
  via slices 213 + 223; the genuine remaining gap was the missing
  `/risks` assertion).
