# 246 — Risks list: footer pagination control · decisions log

**Slice:** `docs/issues/246-risks-list-pagination-control.md`
**Branch:** `frontend/246-risks-pagination`
**Author:** Claude (engineer subagent)
**Date:** 2026-05-23

The slice adds a footer pagination control to `/risks` matching the
iteration-1 mockup (`Plans/mockups/risks.html` lines 267-273). At the
current bootstrap-seed scale (0 risks) the control is invisible; the
mid-term motivation is that once a register grows past ~50 rows, the
unpaginated render becomes a usability + perf concern. Four decisions
landed during the build that are worth capturing.

---

## Decisions made

### D1 — New shared `<ListPagination>` primitive rather than a per-page footer

**Decision:** **Create a new shared component at `web/components/list/pagination.tsx`** exported through the `@/components/list` barrel, alongside the other generic list-shell primitives (`ListPage`, `ListTable`, `FilterPills`, `EmptyState`, `ListLoadingSkeleton`). The `/risks` page consumes it; `/controls` (#227), `/policies` (#240), `/evidence` (#237), and any future list-view follow-on can consume the same primitive.

**Options considered:**

| Option                                                                 | Why rejected / why chosen                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| ---------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) **Shared `<ListPagination>` in `web/components/list/`** — _chosen_ | The spec narrative explicitly says "The remediation surface is the shared `<ListTable>` shell plus per-page wire-up" and "the maintainer may collapse #227 + #246 (and any future list-view pagination spillovers) into a single list-shell slice". The user's invocation likewise said "If creating a shared `<Pagination>` component, place at `web/components/list/pagination.tsx` for reuse by other list pages". A shared shell-level primitive is the right level for this.                                                                                                                           |
| (b) **Inline JSX inside `web/app/(authed)/risks/page.tsx`**            | The same UI shape will land in three other open slices (#227, #240, #237). Inlining a copy four times invites drift. Rejected.                                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| (c) **Extend `<ListTable>` to absorb pagination as an internal prop**  | The spec's AC-1 originally proposed adding a `pagination?` prop to `<ListTable>`. On read this couples a layout primitive (the table chrome) to a control primitive (the pager). They have different DOM siblings — the pager renders BELOW the table at the same z-level as the empty-state fallback. A footer SLOT on `<ListTable>` would also need a no-op default for the four other consumers (per AC-1 "When absent, current behavior is preserved"). Rejected — a sibling primitive is simpler and matches the existing pattern (`<EmptyState>` is also a sibling, not a slot inside `<ListTable>`). |

**Rationale.** The list-shell barrel was built (slice 098) precisely so cross-cutting list features can land once. Pagination is the canonical example. The footer renders as a sibling of `<ListTable>` inside the page's `<ListPage>` content slot — the same place the slice 185 banner renders.

**Confidence:** **high.** The spec narrative anticipates this consolidation; the implementation cost of a separate primitive is trivial.

**Follow-up:** Slices #227, #240, and #237 can adopt this primitive verbatim. Their per-page constants (`CONTROLS_PAGE_SIZE`, `POLICIES_PAGE_SIZE`, `EVIDENCE_PAGE_SIZE`) live in the consuming page module per P0-246-4.

---

### D2 — Page-state lives in the URL, not React state

**Decision:** **Bind the current page to `?page=N` in the URL** via `useSearchParams` / `router.replace`, matching the slice 098 / slice 100 / slice 244 filter-state precedent. React state (`useState`) is not used for the page index.

**Options considered:**

| Option                                                            | Why rejected / why chosen                                                                                                                                                                 |
| ----------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) **URL `?page=N` (1-indexed) via `router.replace`** — _chosen_ | Spec AC-2 explicitly says "wires pagination state into the URL via `?page=N` (1-indexed). Refresh restores the page". Matches the existing filter-state pattern. Bookmarkable. Shareable. |
| (b) **React `useState`**                                          | Refreshing the page would lose the page index. Sharing a link to a paginated view would land on page 1. Spec rejects this path.                                                           |
| (c) **Cookie / localStorage**                                     | Per-page persistence is the wrong granularity — it would mean opening `/risks` in a new tab restores the last-visited page, which is surprising. URL state is the obvious correct level.  |

**Rationale.** URL state is the project-wide pattern for filter / view state on list pages. Pagination is a peer of filters; binding it to the URL is the consistent choice.

**Confidence:** **high.** Spec lock-in.

**Notes on the URL shape:**

- The canonical page-1 URL DOES NOT carry `?page=1` — the param is dropped when the user is on page 1. This keeps the URL clean and ensures the no-pagination URL and the explicit page-1 URL are identical (no shadow URLs).
- Page-mutation handlers (`updateFilter`, `clearAll`) explicitly `sp.delete(PAGE_PARAM)` so any filter change resets the page index to 1 per AC-5. The decision was the only safe one — if a user is on page 3 of a 5-page result and applies a filter that narrows the set to 12 rows (single page), staying on page 3 would render an empty table with no obvious recovery.
- Out-of-range page indices in the URL (`?page=99` on a 2-page result, `?page=-3`, `?page=abc`) clamp to the nearest valid page via the `paginationBounds` helper. This survives stale bookmarks gracefully without throwing.

---

### D3 — Footer suppressed when the filtered set is empty

**Decision:** **Render the pagination footer only when `visible.length > 0`.** An empty filtered set delegates entirely to the `<ListTable>` `emptyFallback` (the `<EmptyState>` zero-state CTA) — no footer chrome at all.

**Options considered:**

| Option                                                       | Why rejected / why chosen                                                                                                                                                                                                                                                                                                |
| ------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| (a) **Suppress footer when empty** — _chosen_                | The `<EmptyState>` already says "No risks match these filters" with a Clear filters CTA. Rendering a "Showing 0 of 0" footer below an empty-state CTA is visually noisy and adds zero information. The footer's truth-telling chrome value is in the M–N range — when there is no range, the empty state is more useful. |
| (b) **Always render footer; show "Showing 0 of 0" on empty** | The `<ListPagination>` component itself DOES handle the empty case correctly (it returns "Showing 0 of 0" and disables both buttons). But the page-level composition is better off suppressing it — the user already sees a CTA banner that explains the zero state more concretely.                                     |
| (c) **Render footer only when totalPages > 1**               | This is the most aggressive "hide when not needed" stance: show no chrome at all on a single-page result. Rejected because the truth-telling "Showing M of TOTAL" value is real on a 7-of-47 register too — the user wants to know how many rows match, even when it's a single page.                                    |

**Rationale.** This is the right balance between truth-telling chrome and UI density. The footer is informative on every populated set, including single-page sets (where it tells the user "yes, this is everything — no hidden rows"). On a true empty result, the empty-state CTA is doing the truth-telling already, so the footer is redundant.

**Confidence:** **medium-high.** Edge case I could see arguing either way; landed on (a) because the empty-state CTA already truthfully describes the zero condition.

---

### D4 — Page-size lives as a module-scope constant, not a prop or env var

**Decision:** **`const RISKS_PAGE_SIZE = 50` at the top of `web/app/(authed)/risks/page.tsx`**, grep-discoverable and per-page configurable. The component-level prop `pageSize` accepts the constant; no global default in the shell.

**Options considered:**

| Option                                            | Why rejected / why chosen                                                                                                                                                       |
| ------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) **Per-page module-scope constant** — _chosen_ | Spec anti-criterion P0-246-4 is explicit: "Does NOT hardcode page size in component code. The default (50) lives in a named constant in the page module so it is greppable".    |
| (b) **Default in `<ListPagination>`**             | Hides the value from the consuming page. Two pages with different size defaults would have to override the shell default, which inverts the where-does-the-truth-live question. |
| (c) **Read from env / settings**                  | No spec or user need. Defer.                                                                                                                                                    |

**Rationale.** Spec lock-in plus a real anti-criterion. The page declares its own page size; the shell takes it as a prop.

**Confidence:** **high.**

**Follow-up:** When #227 / #240 / #237 land, they each declare their own `<PAGE>_PAGE_SIZE = N` constant. The maintainer may consolidate into a single `web/components/list/page-size-defaults.ts` if the constants diverge in a way that suggests a shared default would help, but the v1 pattern is per-page locality.

---

## Operational notes

- **No backend changes.** Per P0-246-1 there is no server-side LIMIT/OFFSET on `GET /v1/risks`. The wire endpoint ships the full filtered set; the page slices client-side. No SQL changes, no sqlc regeneration.
- **No CHANGELOG / no `_STATUS.md` edit in this branch.** The user's invocation explicitly overrode the spec's AC-8 (CHANGELOG entry); the maintainer will reconcile `_STATUS.md` on merge per the established batch policy.
- **Tests:** 18 new vitest assertions for the math helpers (`paginationBounds`, `paginateRows`) at the unit level. 6 new quarantined Playwright assertions at the e2e level (matching the slice 100 / slice 244 precedent of commented-out specs preserved as reviewable contracts until the slice 082 seed-data harness lands). Full vitest suite runs clean (866 / 866).
- **Pre-existing typecheck warnings.** `scripts/capture-readme-screenshots.test.ts` carries pre-existing `ProcessEnv.NODE_ENV` typing issues unrelated to this slice. They were present on base commit `c25e10ac` and are not introduced or affected here.

---

## Acceptance criteria check

| AC                                       | Status                                                                    | Where                                  |
| ---------------------------------------- | ------------------------------------------------------------------------- | -------------------------------------- |
| AC-1 (shared shell pagination prop)      | ✅ via sibling primitive (D1 — divergence from spec language; documented) | `web/components/list/pagination.tsx`   |
| AC-2 (URL `?page=N`)                     | ✅                                                                        | `page.tsx` `currentPage` + `goToPage`  |
| AC-3 (page size 50 default)              | ✅                                                                        | `page.tsx` `RISKS_PAGE_SIZE`           |
| AC-4 ("Showing M–N of TOTAL" footer)     | ✅                                                                        | `pagination.tsx` summary line          |
| AC-5 (page resets to 1 on filter change) | ✅                                                                        | `page.tsx` `updateFilter` + `clearAll` |
| AC-6 (vitest pagination math)            | ✅                                                                        | `pagination.test.ts` (18 tests)        |
| AC-7 (Playwright spec extension)         | ✅ (quarantined per project convention)                                   | `e2e/risks-list.spec.ts`               |
| AC-8 (CHANGELOG entry)                   | ❌ — explicitly overridden by user invocation                             | n/a                                    |

| Anti-criterion                                      | Status                                                                   |
| --------------------------------------------------- | ------------------------------------------------------------------------ |
| P0-246-1 (no server-side LIMIT/OFFSET)              | ✅ — no backend changes                                                  |
| P0-246-2 (sort + row presentation untouched)        | ✅                                                                       |
| P0-246-3 (no-pagination consumers still work)       | ✅ — `<ListPagination>` is a sibling primitive, not a `<ListTable>` slot |
| P0-246-4 (no hardcoded page size in component code) | ✅ — `RISKS_PAGE_SIZE` at module scope                                   |
