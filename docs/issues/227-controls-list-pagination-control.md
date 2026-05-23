# 227 — Add pagination to /controls list

**Cluster:** Quality / UI parity (frontend + small backend)
**Estimate:** 1.5d (UI pagination + URL plumbing + upstream LIMIT/OFFSET)
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced during slice 204 audit fleet (controls page), captured as
follow-up per continuous-batch policy. The mockup at
`Plans/mockups/controls.html` (lines 266-272) ships a pagination
footer:

```html
<div
  class="border-t border-slate-200 px-5 py-2.5 flex items-center justify-between text-xs text-slate-500 bg-slate-50"
>
  <span>Showing 1–7 of 82</span>
  <div class="flex items-center gap-2">
    <button class="..." disabled>Previous</button>
    <button class="...">Next</button>
  </div>
</div>
```

The live `/controls` page renders no pagination UI. The page's
`<ListTable>` consumes the entire filtered set in a single page;
there is no `?page=` query param, no `LIMIT` / `OFFSET` plumbing
to the upstream `GET /v1/anchors`.

Today this is tolerable: the atlas-edge instance has 53 anchors
(bootstrap-seed SCF subset). The full SCF catalog has ~1,400
anchors — see canvas §3.5. Once a tenant's framework-scope
predicate expands beyond the bootstrap seed (or once a community-
contributed framework lands), the unpaginated table becomes a real
usability issue: scroll-fatigue, browser-render cost, and (more
critically) the "Showing X of Y" meta in the filter-pill row
overflows its semantics.

The slice ships:

- Pagination footer (mockup shape).
- `?page=N` URL param.
- BFF + upstream `LIMIT 50 OFFSET (page-1)*50` plumbing.
- Page size = 50 (matching the typeahead cap in slice 224 — a
  consistent "scan a screenful" budget).

## Threat model

| STRIDE                | Threat                                                  | Mitigation                                                                                                                                                      |
| --------------------- | ------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing        | None — read path under existing JWT.                    | n/a                                                                                                                                                             |
| **T** Tampering       | None — read path.                                       | n/a                                                                                                                                                             |
| **R** Repudiation     | None — read path.                                       | n/a                                                                                                                                                             |
| **I** Info disclosure | None — same RLS as the unpaginated query.               | n/a                                                                                                                                                             |
| **D** DoS             | Negative `page`, very large `page`, non-integer `page`. | AC-4: input validated server-side. Negative → 400. Non-integer → 400. `page > ceil(total/limit)` → returns empty result with `page=last` indicator in response. |
| **E** EoP             | None — same authz as the existing pill filters.         | n/a                                                                                                                                                             |

**Verdict.** `no-mitigations-needed-beyond-input-validation`.

## Acceptance criteria

- **AC-1.** Pagination footer renders below the table on
  `/controls`, matching the mockup's "Showing N–M of T" + Previous
  / Next button shape.
- **AC-2.** Page size = 50. Fixed for v0; configurable page sizes
  are a future slice.
- **AC-3.** `?page=N` URL param plumbed through the page's
  `useSearchParams` reader, parity with the existing filter pills'
  URL plumbing in `page.tsx` lines 125-152.
- **AC-4.** BFF `/api/controls` accepts `?page=N` (1-indexed),
  forwards to upstream `GET /v1/anchors?include=state&limit=50&offset=(page-1)*50`.
  Server validates: `page` is a positive integer or 400; otherwise
  defaults to `page=1`.
- **AC-5.** Upstream response shape extended:
  `{ anchors: [...], total: <int>, page: <int>, limit: 50 }`. The
  `total` field drives the "Showing N–M of T" meta and the
  Next-button-disabled-on-last-page logic.
- **AC-6.** Previous button disabled on `page=1`. Next button
  disabled when `page * limit >= total`.
- **AC-7.** Clicking Next / Previous updates `?page=N` via
  `router.replace` (preserves filter pills + scroll position is
  reset to top of table).
- **AC-8.** Filter changes reset `?page=1` (per UX convention; a
  user changing filters expects to land at the first page of the
  new filtered set, not the same page index).
- **AC-9.** Vitest unit coverage: filter+page URL parsing,
  page-bound math, BFF query string construction.
- **AC-10.** Playwright e2e spec: navigate filters + pages, assert
  URL updates, assert table rows re-render.
- **AC-11.** Pre-commit clean, DCO sign-off, Co-Authored-By trailer.

## Constitutional invariants honored

- **Invariant 6 (tenant isolation).** Pagination is a presentation
  concern over an already-RLS-enforced query; no new authz surface.
- **Anti-pattern rejected:** "rendering 1,400 rows in a single
  page" is a real perf-and-usability issue once a tenant's scope
  expands. Pagination is the fix.

## Canvas references

- `Plans/canvas/03-ucf.md` §3.5 — SCF catalog scale (~1,400
  anchors)
- `Plans/mockups/controls.html` lines 266-272 — pagination
  footer mockup
- `docs/audit-log/204-page-audit-controls.md` — parent audit

## Dependencies

- **#204** (UI parity audit fleet) — parent.
- **#100** (controls list page) — merged.
- **#104** (BFF anchors-with-state join) — merged. The slice this
  extends with `limit` / `offset` plumbing.

## Anti-criteria (P0 — block merge)

- **P0-227-1.** Does NOT ship configurable page sizes in v0.
  Page-size selector is a follow-on slice.
- **P0-227-2.** Does NOT skip the `total` field on the upstream
  response — without it the UI cannot honestly render "Showing
  N–M of T". Client-side estimation is the anti-pattern.
- **P0-227-3.** Does NOT touch the slice 204 audit harness.
- **P0-227-4.** Does NOT commit any vendor-prefixed test fixture
  tokens; neutral `test-*` only.

## Skill mix (3-4)

1. Next.js App Router — URL param + router.replace plumbing.
2. sqlc — `LIMIT`/`OFFSET` query + total count.
3. Go API handler — extend `/v1/anchors` response with `total`.
4. Vitest + Playwright — coverage.
