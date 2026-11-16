- **frontend:** slice 235 — `/evidence` header chrome parity (regression
  protection). Closes finding F-204-E-3 from the slice 204 UI honesty
  audit fleet. The three chrome elements the finding flagged as missing
  on `/evidence` — audit-period pill, tenant breadcrumb, global ⌘K
  search — were all shipped in the shared authed-shell topbar by
  adjacent slices: the in-progress audit pill by slice 213, and the
  breadcrumb + ⌘K search by slice 223. Both shipping slices' e2e specs
  cover their own routes (`/audits` + `/dashboard` for the pill;
  `/controls` + `/audits` for the breadcrumb and search) but neither
  asserts the chrome on `/evidence`. Slice 235 adds a single new
  Playwright spec at `web/e2e/evidence-header.spec.ts` exercising all
  three components on `/evidence`: the in-progress pill renders the
  seeded period name, the breadcrumb renders `Demo Tenant > Evidence`
  (proving the `evidence → Evidence` entry in `web/lib/page-names.ts`
  is live), and the global search input renders with the mockup
  placeholder + ⌘K kbd hint with a working ⌘K focus shortcut and a
  debounced `/api/search` round-trip (using slice 274's auto-waiting
  pattern via `page.waitForRequest`). No new components, no new BFF
  routes, no new platform endpoints, no source-code changes — pure
  regression protection for three load-bearing chrome elements on the
  one authed route that had no e2e coverage of them. The spec reuses
  the slice 213 `audits-header` fixture (audit period) + the slice 223
  `controls-top-bar` fixture (tenants row) — both are idempotent so
  sequencing them in `beforeAll` adds no coupling cost. See
  [`docs/audit-log/235-evidence-header-chrome-decisions.md`](docs/audit-log/235-evidence-header-chrome-decisions.md)
  D1 (subset shipped — all three components were already in production
  via slices 213 + 223; the genuine remaining gap was the missing
  `/evidence` assertion).
