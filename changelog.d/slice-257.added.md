- **frontend:** slice 257 — `/controls/{id}` header chrome parity
  (regression protection). Closes finding F-204-C-5 from the slice 204
  UI honesty audit fleet. The four chrome elements the finding flagged
  as missing on `/controls/{id}` — audit-period pill, user avatar,
  tenant breadcrumb, global ⌘K search — were all shipped in the shared
  authed-shell topbar by adjacent slices: the in-progress audit pill +
  user avatar by slice 213, and the breadcrumb + ⌘K search by slice 223. The shipping slices' e2e specs cover their own routes
  (`/audits` + `/dashboard` for the pill + avatar; `/controls` (list)
  - `/audits` for the breadcrumb and search; slice 235 added
    `/evidence`) but none assert the chrome on `/controls/{id}` (detail).
    Slice 257 adds a single new Playwright spec at
    `web/e2e/control-detail-top-bar.spec.ts` exercising all three
    components on `/controls/${seeded.controlId}`: the in-progress pill
    renders the seeded period name, the breadcrumb renders `Demo Tenant
    > Controls`(proving the slice 223 first-segment rollup keeps detail
routes mapped to their section name via`web/lib/page-names.ts`), and
the global search input renders with the mockup placeholder + ⌘K kbd
hint with a working ⌘K focus shortcut and a debounced `/api/search`round-trip (using slice 274's auto-waiting pattern via`page.waitForRequest`). No new components, no new BFF routes, no new
platform endpoints, no source-code changes — pure regression
protection for four load-bearing chrome elements on the one authed
detail route that had no e2e coverage of them. The spec reuses the
slice 213 `audits-header`fixture (audit period) + the slice 223`controls-top-bar`fixture (tenants row) — both are idempotent so
sequencing them in`beforeAll` adds no coupling cost (slice 235
established the precedent). See
[`docs/audit-log/257-control-detail-top-bar-regression-decisions.md`](docs/audit-log/257-control-detail-top-bar-regression-decisions.md)
D1 (subset shipped — all four components were already in production
via slices 213 + 223; the genuine remaining gap was the missing
`/controls/{id}` assertion) and D4 (two-segment shipped-shape > supersedes the slice 257 spec's three-segment proposed-shape).
