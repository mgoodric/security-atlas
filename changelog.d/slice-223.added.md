- **frontend:** slice 223 — shared-shell top-bar parity (breadcrumb
  - global ⌘K search). Closes the `/controls` page audit's chrome
    parity gap (slice 204 spillover) by adding the last two affordances
    the mockup at `Plans/mockups/controls.html` promised but the live
    shell omitted: a read-only `<tenant> > <page>` breadcrumb chip
    (left side, distinct from the interactive TenantSwitcher) and a
    global ⌘K search input that calls the slice 268 unified
    `/v1/search` endpoint via a new BFF at `web/app/api/search/route.ts`.
    Both components render in the shared `web/components/shell/topbar.tsx`
    so they appear on every authed page (parallels slice 213's
    in-progress pill + user avatar pattern). The breadcrumb is a
    client component reading `usePathname()` for the right-hand label
  - `/api/me/tenants` (slice 192 BFF) for the tenant name; the pure
    `derivePageName` helper lives at `web/lib/page-names.ts` for
    table-driven unit coverage. The global search is a client component
    with a 250ms debounced fetch + popover results grouped by entity
    type (Controls / Risks / Evidence) + keyboard navigation (arrows /
    Enter / Esc). Result rows route via the established per-primitive
    conventions (`/controls/<id>` for the existing detail page;
    `/risks/hierarchy?focus=<id>` for the slice 100 alias; `/evidence`
    for the list page — no fabricated detail destinations). 51 new
    vitest tests cover the four pure helpers (`derivePageName`,
    `pickCurrentTenantName`, `groupByType`, `hrefForHit`,
    `isShortcutTrigger`) plus the BFF route handler (cookie/upstream
    forwarding, error paths). Playwright e2e at
    `web/e2e/controls-top-bar.spec.ts` covers the integrated positive
    cases (breadcrumb segments on `/controls` + `/audits`, ⌘K focus,
    typing → grouped popover, Enter navigates). No new platform
    endpoint (P0-223-1 — search reuses slice 268's `/v1/search`;
    breadcrumb reuses slice 192's `/v1/me/tenants`). No
    `internal/api/` modifications. JUDGMENT calls in
    `docs/audit-log/223-controls-top-bar-chrome-decisions.md` (D1:
    ship both elements + supersede spillovers 271 + 272; D2: client
    component; D3: popover not modal; D4: result routing convention;
    D5: cascading-setTimeout lint compliance; D6: tenants-row fixture
    seed; D7: no `_STATUS.md` touch).
