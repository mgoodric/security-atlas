# 098 — /controls list view (per slice 093 mockup)

**Cluster:** Frontend
**Estimate:** 1-2d
**Type:** AFK
**Status:** `ready`

## Narrative

Implementation slice for one of the six pages designed in slice 093. The mockup at `Plans/mockups/controls.html` is the canonical design reference; the design-decisions doc at `Plans/canvas/12-ui-fill-in-design-decisions.md` captures the patterns (top-nav, empty state, loading skeleton, filter row) that this slice MUST follow.

Today `/controls` 404s in the sidebar (audit finding F-4 in `Plans/canvas/13-ui-mockup-audit-2026-05-16.md`). This slice ships the missing page.

## Acceptance criteria

- [ ] AC-1: `web/app/(authed)/controls/page.tsx` server component renders the tenant's anchor library as a table, joining `GET /v1/anchors` + `GET /v1/controls/{id}/state` per row (or a single batched call if a `GET /v1/anchors?include=state` extension is added — surface as design question if so).
- [ ] AC-2: Columns per design doc §7: `scf_id`, `name`, `family`, `result`, `freshness_status` + `freshness_class`, `last_observed_at`. No column not derivable from `anchorWire` + `stateWire`.
- [ ] AC-3: Horizontal pill filter row above the table (per design doc §8) — filterable by framework + scope + state (passing/drifted/exception/n/a).
- [ ] AC-4: Empty state per design doc §2 — "No controls match these filters" + `Clear filters` button. Filter-induced (not zero-state — every tenant has SCF anchors).
- [ ] AC-5: Loading skeleton per design doc §3 — 3 shimmer rows mirroring the column widths.
- [ ] AC-6: Row click navigates to `/controls/[id]` (existing slice-041 detail page).
- [ ] AC-7: Vitest unit tests for the row-join logic + filter-state computation.
- [ ] AC-8: Playwright spec `web/e2e/controls-list.spec.ts` covering: list renders, filter narrows results, empty state appears on no-match, row click navigates.

## Constitutional invariants honored

- **Invariant 6 (tenant isolation):** all reads go through the tenant-bound BFF layer (read `web/app/api/controls/route.ts` if exists, or follow slice 094 calendar-BFF pattern).
- **AI-assist boundary:** pure render of values; no auto-narration.

## Canvas references

- `Plans/mockups/controls.html` (the design reference)
- `Plans/canvas/12-ui-fill-in-design-decisions.md` §1, §2, §3, §7, §8 (patterns)
- `internal/api/anchors/handlers.go` (`anchorWire` — wire shape source)
- `internal/api/controlstate/handlers.go` (`stateWire` — wire shape source)
- Slice 040 `/dashboard` implementation (BFF + TanStack Query pattern reference)
- Slice 094 `/calendar` implementation (recent list-view pattern with filter row)

## Dependencies

- **093** (mockups + design-decisions doc) — merged
- **005** (Next.js scaffold) — merged
- **006** (SCF catalog) — merged
- **008** (UCF graph traversal API) — merged
- **012** (control evaluation engine — provides state per anchor) — merged

## Anti-criteria (P0 — block merge)

- **P0-A1:** Does NOT invent columns the backend doesn't return (design doc §7 binding).
- **P0-A2:** Does NOT use a left filter sidebar — horizontal pill row only (design doc §8).
- **P0-A3:** Does NOT use a generic centered spinner — skeleton rows only (design doc §3 anti-pattern).
- **P0-A4:** Does NOT use Lorem Ipsum or filler copy — real placeholder data only.
- **P0-A5:** Does NOT use vendor-prefixed tokens in test fixtures — neutral `test-*` only.

## Skill mix

- Next.js App Router server component + TanStack Query BFF proxy pattern
- shadcn/ui Table + filter row composition (no calendar/datagrid library)
- Vitest unit tests for pure-render logic
- Playwright spec authoring against the seed-data harness (slice 082 dep — quarantined spec OK)

## Notes

- The 5 list-view slices (098, 099, 100, 102, 103) share a near-identical skeleton (table + filter row + empty + loading). Extracting a `<ListView>` shell component during the FIRST slice (this one) lets the remaining 4 reuse it — file as a spillover slice if you do, or inline the shared bits as `web/components/list/*.tsx` during this slice.
- For the row-fan-out concern: if `GET /v1/anchors` returns ~1,400 anchors per tenant and each state call adds latency, surface the `?include=state` extension as a backend follow-on slice rather than making 1,400 calls.
