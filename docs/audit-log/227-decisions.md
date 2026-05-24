# 227 — Controls list: footer pagination control · decisions log

**Slice:** `docs/issues/227-controls-list-pagination-control.md`
**Branch:** `frontend/227-controls-pagination`
**Author:** Claude (engineer subagent)
**Date:** 2026-05-23

The slice adds a footer pagination control to `/controls` matching the
iteration-1 mockup (`Plans/mockups/controls.html` lines 266-272). The
SCF bootstrap importer (slice 006) ships ~53 anchors on `main`, which
already produces a two-page result at the default page size of 50; once
a tenant's framework-scope predicate expands beyond the bootstrap seed
the unpaginated render becomes a real usability + perf concern. The
slice reuses the shared `<ListPagination>` primitive landed by slice
246 (the /risks pagination footer) and wires it into the /controls page
verbatim using the same URL-state pattern.

---

## Decisions made

### D1 — Reuse the slice-246 `<ListPagination>` primitive verbatim; no edits to the shared component

**Decision:** **Import the existing `<ListPagination>` + `paginateRows` from `@/components/list`** and wire them into `web/app/(authed)/controls/page.tsx` exactly as slice 246 wired them into `web/app/(authed)/risks/page.tsx`. Zero edits to `web/components/list/pagination.tsx`, zero edits to the barrel, zero new shared primitives.

**Options considered:**

| Option                                                | Why rejected / why chosen                                                                                                                                                                                                                                                                                                                                                                                  |
| ----------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) **Reuse `<ListPagination>` as-is** — _chosen_     | The user invocation said it explicitly: "NO modifying the shared ListPagination primitive (already shipped); use as-is". Slice 246's decisions log D1 anticipated exactly this consolidation: "Slices #227, #240, and #237 can adopt this primitive verbatim. Their per-page constants (`CONTROLS_PAGE_SIZE`, `POLICIES_PAGE_SIZE`, `EVIDENCE_PAGE_SIZE`) live in the consuming page module per P0-246-4". |
| (b) **Server-side LIMIT/OFFSET on `GET /v1/anchors`** | The original slice spec (AC-4/AC-5) called for upstream LIMIT/OFFSET plumbing + a `total` field on the wire. The user invocation overrode that: "client-side slice over already-fetched data; Same pattern slice 246 used for /risks". Server-side pagination is deferred to a future slice if the catalog scale demands it.                                                                               |
| (c) **New per-page footer JSX**                       | Same UI shape would land four times across /controls, /risks, /policies, /evidence — drift bait. The shared primitive is the right level.                                                                                                                                                                                                                                                                  |

**Rationale.** The shared primitive is already paying for itself on the second consumer. The cost of this slice is ~30 lines of page-state plumbing; the primitive's API does the rest.

**Confidence:** **high.** User invocation lock-in; spec-narrative anticipation; one prior consumer pattern.

---

### D2 — Spec divergence: client-side slice instead of upstream `LIMIT/OFFSET` + `total`

**Decision:** **Honour the user invocation over the slice spec.** The spec originally proposed (AC-4, AC-5, "Skill mix" item 2) a server-side LIMIT/OFFSET via sqlc plus a `total` field on the wire shape. The user invocation explicitly redirected to the slice 246 pattern: client-side slice over the already-fetched filtered set.

**Options considered:**

| Option                                                     | Why rejected / why chosen                                                                                                                                                                                                                                                                                                                                  |
| ---------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) **Client-side slice (slice 246 pattern)** — _chosen_   | User invocation lock-in. At ~53 anchors (the current SCF bootstrap seed) the client-side cost is negligible; the architecture leaves a clean upgrade path if the catalog grows past ~10k rows.                                                                                                                                                             |
| (b) **Original spec — server-side LIMIT/OFFSET + `total`** | Larger surface (sqlc + Go handler + BFF + wire-shape extension + page wiring). Spec AC-5 was load-bearing in the original — `total` drives the "Showing M–N of T" line. The user's invocation explicitly reframed AC-5 as derivable client-side from `visible.length`, the same way slice 246 does. Out of scope for the 30-min budget the invocation set. |

**Rationale.** The user owns the spec scope; the engineer follows the invocation. The decisions log captures the divergence so a future reader sees that the original AC-4/AC-5 wire-shape extension was deliberately deferred, not forgotten. When a tenant's catalog grows past ~10k rows the upgrade to server-side pagination is a single-page swap — the shared `<ListPagination>` component already comments this future at lines 17-20 of its file.

**Confidence:** **high.**

**Follow-up:** A separate slice can extend `GET /v1/anchors` with `limit` / `offset` / `total` if the catalog scale demands it. The shared `<ListPagination>` component does not need to change — the consuming page swaps `visible.length` for an API-derived total.

---

### D3 — Page-state URL pattern matches slice 246 verbatim (constants, helper, footer suppression)

**Decision:** **Copy the slice-246 wiring shape verbatim** for the page-level plumbing — the `CONTROLS_PAGE_SIZE = 50` constant at module scope, the `PAGE_PARAM = "page"` constant, the `useMemo` parse of `?page=N`, the `goToPage` handler that drops the param on page ≤ 1, and the `sp.delete(PAGE_PARAM)` calls in `updateFilter` + `clearAll`. Footer suppression on `visible.length === 0` matches slice 246's D3.

**Options considered:**

| Option                                                     | Why rejected / why chosen                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                        |
| ---------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) **Verbatim slice-246 pattern** — _chosen_              | Two consumers using the same shape lets a future reader understand both pages by reading one. The `<ListPagination>` primitive's contract is the same on both pages; the wiring should be too. Reduces drift surface.                                                                                                                                                                                                                                                                                                            |
| (b) **Lift the wiring into a shared `usePagination` hook** | Tempting (the duplication is real — ~25 lines per consuming page) but premature. Two consumers is not enough signal to extract a hook; the page-level wiring is also tightly coupled to the page-specific `updateFilter` / `clearAll` which differ slightly across pages (different filter sets). A hook would either need a generic interface that obscures the per-page intent, or accept page-specific knobs that defeat the purpose. Re-evaluate when the third consumer lands (slice 240 /policies or slice 237 /evidence). |

**Rationale.** Consistency is a feature when two consumers are doing the same thing. The hook extraction is a follow-on candidate, not a v1 ask.

**Confidence:** **high.**

**Follow-up:** When slices 237 / 240 land, evaluate `usePagination` hook extraction. If three pages have the same 25-line block with only the route prefix and constant name differing, the hook earns its keep. Until then, deliberate duplication.

---

### D4 — `onRowClick` row-navigation is preserved; pagination is composable, not exclusive

**Decision:** **Leave the `<ListTable>` `onRowClick` prop wired on the controls page.** The /controls table routes row clicks to `/controls/[id]` (slice 098); the pagination footer is a sibling of `<ListTable>`, not a replacement for it. No interaction conflicts; the click handler is row-scoped, the pagination buttons are footer-scoped.

**Options considered:**

| Option                                           | Why rejected / why chosen                                                                                                                                                                                                                                                               |
| ------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) **Preserve row-click navigation** — _chosen_ | The /controls page is the only currently-paginated list page that exposes row-click navigation (the /risks page intentionally removed row-click in slice 185 — F-178-5 honesty fix). The two affordances are orthogonal; nothing in pagination requires touching row-level interaction. |
| (b) **Remove row-click to mirror slice 185**     | Out of scope for slice 227. /controls has explicit per-row links (`controls-row-scf-id`) that route to the detail page; the row-as-link affordance is a separate UX question, not a pagination concern.                                                                                 |

**Rationale.** Pagination is an additive UI concern over the table; it does not invalidate existing row-level affordances.

**Confidence:** **high.**

---

## Operational notes

- **No backend changes.** Per the user invocation (and the slice 246 P0-246-1 carried forward as D2 above) there is no server-side LIMIT/OFFSET on `GET /v1/anchors`. The BFF + handler are untouched; no sqlc regeneration; no migration.
- **No edits to `<ListPagination>` or the barrel.** The user invocation explicitly forbade modifying the shared primitive. The barrel already re-exports `ListPagination`, `paginateRows`, and `paginationBounds` (slice 246). The /controls page imports them and stops there.
- **No CHANGELOG / no `_STATUS.md` edit in this branch.** The user invocation explicitly forbade both per the established batch policy; the maintainer reconciles `_STATUS.md` on merge.
- **Tests:**
  - Pagination math (`paginationBounds`, `paginateRows`) is covered by the 18 shared vitest assertions slice 246 added at `web/components/list/pagination.test.ts`. No new math tests are needed — the math is shared.
  - 6 new quarantined Playwright assertions extend `web/e2e/controls-list.spec.ts` covering footer summary, Prev/Next enable/disable, URL round-trip, refresh-restores-page, and filter-resets-page. Matches the slice 246 / slice 100 / slice 244 pattern of commented-out specs preserved as reviewable contracts until the slice 082 seed-data harness lands.
  - Full vitest suite passes: 898 / 898 tests across 85 files.
- **Pre-existing typecheck warnings.** `scripts/capture-readme-screenshots.test.ts` carries pre-existing `ProcessEnv.NODE_ENV` typing issues unrelated to this slice. They were present on the rebase base `b181ac3f` and are not introduced or affected here. Same finding slice 246 logged.
- **Pre-existing eslint warnings.** Two `Unused eslint-disable directive` warnings on `scripts/capture-readme-screenshots.ts` — same source file as the typecheck warnings, same pre-existing posture.

---

## Acceptance criteria check

The slice spec's AC-1 through AC-11 were partially overridden by the user invocation (client-side slice instead of server-side LIMIT/OFFSET; no CHANGELOG). The table below reflects the as-shipped status against both the original spec and the invocation-scoped reframe.

| AC                                                                  | Status                                                                                                 | Where                                     |
| ------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------ | ----------------------------------------- |
| AC-1 (footer renders below the table, matching mockup)              | ✅                                                                                                     | `page.tsx` `<ListPagination>` composition |
| AC-2 (page size = 50)                                               | ✅                                                                                                     | `page.tsx` `CONTROLS_PAGE_SIZE = 50`      |
| AC-3 (`?page=N` URL plumbing)                                       | ✅                                                                                                     | `page.tsx` `currentPage` + `goToPage`     |
| AC-4 (BFF accepts `?page=N`, forwards LIMIT/OFFSET)                 | ⏸ — deferred (user invocation; D2)                                                                    | n/a (client-side slice)                   |
| AC-5 (upstream response includes `total`)                           | ⏸ — deferred (user invocation; D2)                                                                    | n/a (derived from `visible.length`)       |
| AC-6 (Prev disabled on page 1, Next disabled on last page)          | ✅                                                                                                     | `<ListPagination>` (slice 246 primitive)  |
| AC-7 (clicking Next/Prev updates `?page=N` via `router.replace`)    | ✅                                                                                                     | `page.tsx` `goToPage`                     |
| AC-8 (filter changes reset to `?page=1`)                            | ✅                                                                                                     | `page.tsx` `updateFilter` + `clearAll`    |
| AC-9 (vitest unit coverage for math + URL parsing)                  | ✅ (math covered by shared slice-246 tests; URL parsing is the same `useMemo` pattern slice 246 ships) | `components/list/pagination.test.ts`      |
| AC-10 (Playwright e2e — paginate forward/back, URL + row re-render) | ✅ (quarantined per project convention; 6 specs)                                                       | `e2e/controls-list.spec.ts`               |
| AC-11 (pre-commit clean, DCO + Co-Authored-By)                      | ✅                                                                                                     | commit trailer                            |

| Anti-criterion                                | Status                                                                               |
| --------------------------------------------- | ------------------------------------------------------------------------------------ |
| P0-227-1 (no configurable page sizes in v0)   | ✅ — single `CONTROLS_PAGE_SIZE` constant                                            |
| P0-227-2 (no skipping `total`)                | ✅ — total is the filtered set size (`visible.length`), shown honestly in the footer |
| P0-227-3 (no slice-204 audit harness changes) | ✅ — untouched                                                                       |
| P0-227-4 (no vendor-prefixed test tokens)     | ✅ — no test fixtures touched                                                        |

Plus the user-invocation anti-criteria:

| Invocation anti-criterion                            | Status |
| ---------------------------------------------------- | ------ |
| NO `_STATUS.md` / `CHANGELOG.md` edits               | ✅     |
| NO modifying the shared `<ListPagination>` primitive | ✅     |
| Use slice-246's pattern (client-side slice)          | ✅     |
