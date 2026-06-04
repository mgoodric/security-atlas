# 246 — Risks list: pagination control absent from footer

**Cluster:** Frontend
**Estimate:** 0.5d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced during slice 204 audit fleet (`/risks` page), captured as a
follow-up per continuous-batch policy. The mockup at
`Plans/mockups/risks.html` lines 267-273 shows a footer pagination
row at the bottom of the risk-register table:

```
Showing 1–7 of 47   [Previous] [Next]
```

The live `/risks` page has no pagination UI. `<ListTable>` consumes
the full filtered `visible` array directly (`page.tsx` line 471), and
no `?page=` query param is parsed or written. There is no
`LIMIT`/`OFFSET` plumbing to upstream.

At 0 risks today (the bootstrap seed ships an empty register), the
absence is invisible. Once a register grows past ~50 rows, the
unpaginated render becomes a usability issue and a perf concern.

This is the SAME horizontal gap previously flagged on `/controls`
(#227). The remediation surface is the shared `<ListTable>` shell
plus per-page wire-up. The maintainer may collapse #227 + #246 (and
any future list-view pagination spillovers) into a single
list-shell slice; the per-page audit keeps the traceability.

## Threat model

**Verdict.** **no-mitigations-needed.** Read-only pagination over
an already-RLS-enforced query path. No new authz surface.

## Acceptance criteria

- **AC-1.** `<ListTable>` shell in `web/components/list/list-table.tsx`
  accepts a `pagination?: { pageSize, currentPage, totalCount }` prop
  - an `onPageChange` callback. When absent, current behavior is
    preserved (no breaking change for other consumers).
- **AC-2.** `/risks` page wires pagination state into the URL via
  `?page=N` (1-indexed). Refresh restores the page; clearing
  filters resets to `page=1`.
- **AC-3.** Page size default: 50. The page slices `visible`
  client-side (the upstream `GET /v1/risks` ships the full list — no
  server-side LIMIT/OFFSET for v1; that is a future slice if the
  register grows beyond ~10k rows).
- **AC-4.** Footer renders "Showing M–N of TOTAL" (1-indexed) with
  Previous + Next buttons. Previous is disabled on page 1; Next is
  disabled on the last page.
- **AC-5.** Pagination works in concert with filtering — filtered
  total is the `of` value; page index resets to 1 when a filter
  changes (preserving the user's mental model).
- **AC-6.** vitest coverage for the pagination math (page slicing,
  edge cases at page 1 and last page, empty-result pagination).
- **AC-7.** Playwright spec extension: paginate forward and back on
  a populated register fixture; assert URL state + footer text.
- **AC-8.** CHANGELOG entry: "Risks list: footer pagination control
  (#246; slice 100 follow-on)".

## Constitutional invariants honored

- **Truth-telling chrome.** "Showing M–N of TOTAL" is computed from
  the actual filtered set; never a stub.
- **No mockup over-promise.** The mockup's pagination is the source
  of truth for the UX shape; this slice matches it.

## Canvas references

- `Plans/canvas/06-risk.md` — risk register linkage

## Dependencies

- **#100** Risks list view — `merged`. The page this slice extends.
- **#227** Controls list pagination — `ready`. Same shared shell;
  maintainer may collapse.

## Anti-criteria (P0 — block merge)

- **P0-246-1.** Does NOT introduce server-side LIMIT/OFFSET on
  `GET /v1/risks`. The page-level slice is client-side over the
  already-fetched list. Server-side pagination is a separate slice
  if the register scale demands it.
- **P0-246-2.** Does NOT alter the slice-100 default sort order or
  the row presentation. Pagination is the only change.
- **P0-246-3.** Does NOT remove the `<ListTable>` shell's existing
  no-pagination consumers' behavior. Pagination is an opt-in prop.
- **P0-246-4.** Does NOT hardcode page size in component code. The
  default (50) lives in a named constant in the page module so it
  is greppable.

## Skill mix (3-5)

1. Next.js App Router — URL state + client pagination
2. shadcn/ui — pagination control primitives (or custom Prev/Next
   buttons matching the mockup)
3. vitest pagination-math tests
4. Playwright spec extension
