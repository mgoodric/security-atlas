# 684 — Risks page count is inconsistent (header "N of M risks" vs footer "Showing M–N of N")

**Cluster:** frontend
**Estimate:** XS (<0.5d)
**Type:** JUDGMENT (counter semantics / copy)
**Status:** `ready`

## Narrative

The `/risks` list page has the **identical header/footer count contradiction**
that slice 666 fixed on `/controls`. The header (`web/app/(authed)/risks/page.tsx`
~line 352) reads **"Showing {visible.length} of {rows.length} risks"** while the
table footer — the shared `<ListPagination>` primitive — reads **"Showing M–N of
TOTAL"**. Both strings share the verb "Showing" but mean different things (header
= filtered subset of the total; footer = the current page's row range), so read
together the header implies all rows are on screen while the footer paginates
only the first page.

Surfaced during slice 666 (the `/controls` fix), captured as a follow-up per the
continuous-batch spillover policy. The fix is the same copy/semantics change:
the footer keeps the "Showing M–N of TOTAL" page-range phrasing; the header drops
the verb "Showing" and becomes a plain count of the filtered set
("N of M risks" / "M risks").

## Threat model

None — UI copy/consistency.

## Acceptance criteria

- [ ] **AC-1.** Header and footer use consistent, non-contradictory semantics —
      header = filtered count ("N of M risks" / "M risks"), footer = current page
      range ("Showing M–N of N"). The header no longer uses the verb "Showing".
- [ ] **AC-2.** The header no longer implies "all rows shown" when pagination is
      showing only the first page.
- [ ] **AC-3.** Counts remain correct under filtering (the filtered total drives
      the header).
- [ ] **AC-4.** Reuse the slice-666 pattern: extract a pure `risksCountLabel` (or
      a shared generic) with vitest coverage + a per-file coverage floor; the
      shared `web/components/list/pagination.tsx` footer is NOT modified.

## Anti-criteria

- Does NOT change pagination page size or the underlying counts — copy/semantics
  only.
- Does NOT modify the shared `<ListPagination>` primitive (it is correct and
  shared across /controls, /risks, /policies).

## Dependencies

- The Risks list page (`web/app/(authed)/risks/page.tsx`).
- Pattern reference: slice 666 (`web/app/(authed)/controls/count-label.ts` +
  `docs/audit-log/666-controls-count-semantics-decisions.md`).

## Notes

Consider whether the count-label logic should be generalized into a shared
`web/components/list/count-label.ts` consumed by both pages rather than a
per-page copy. Slice 666 deliberately kept it page-local (matching the
filters.ts/selection.ts per-page seam convention); a shared extraction is a
reasonable JUDGMENT call for this slice if a third consumer appears.
