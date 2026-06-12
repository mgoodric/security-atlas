# 691 — Vendor detail does not refresh after recording a review

**Cluster:** Quality
**Estimate:** 0.5d (S)
**Type:** AFK
**Status:** `ready`
**Priority:** P3

## Narrative

**WHY.** Surfaced during slice 424 (vendor-review e2e), captured as a
follow-up per continuous-batch policy. When the operator records a
vendor review on `/vendors/{id}/edit` (sets the Last-review date and
saves), the edit page's `onSubmit` awaits the PATCH and then
`router.push(`/vendors/{id}`)` back to the read-only detail. But the
detail and edit pages **share** the TanStack `["vendor", id]` query key,
and the mutation does **not** call `invalidateQueries(["vendor", id])`.
On nav-back the detail serves the **cached** pre-review vendor, so the
derived review-status badge (`overdue` / `on time`, computed from
`last_review_date` + `review_cadence`) and the "Last review" field do
**not** reflect the review the operator just recorded until a hard
reload or the cache goes stale and a later remount refetches.

This is a real (low-severity) UX gap: the operator records a review and
the page they land on still shows the old state, which is confusing for
a v1-binary workflow (vendor reviews run by the solo security leader).

**WHAT.** After a successful vendor update, invalidate the
`["vendor", id]` query (and, where relevant, the `["vendors", ...]` list

- `["vendors-burndown", ...]` queries) so the read-only detail the
  operator is redirected to reflects the just-saved record — most visibly
  the derived review-status badge flipping `overdue` -> `on time` and the
  updated Last-review date.

**SCOPE DISCIPLINE.** Production-only fix: add the `invalidateQueries`
call (or a router-refresh) on the vendor mutation success path. Then
restore the slice-424 e2e's derived-status-flip assertion (the spec
currently asserts only the navigation + surface re-render, with a
comment pointing here) so the flip becomes a guaranteed contract.

## Acceptance criteria

- [ ] **AC-1.** After saving a vendor edit, the redirected `/vendors/{id}`
      detail reflects the saved `last_review_date` without a hard reload.
- [ ] **AC-2.** The derived review-status badge updates to match the new
      `last_review_date` (e.g. a fresh review flips `overdue` -> `on time`).
- [ ] **AC-3.** The vendor list + burndown reflect the change on return to
      `/vendors` (no stale row).
- [ ] **AC-4.** The slice-424 e2e (`web/e2e/vendor-review-workflow.spec.ts`)
      is extended to assert the derived-status flip after recording a
      review (the assertion slice 424 deferred to this slice).

## Dependencies

- **#424** (vendor-review e2e) — the spec that surfaced this gap and
  that AC-4 extends.

## Notes for the implementing agent

- The mutation wrapper is `updateVendorFromCookieSession`
  (`web/app/(authed)/vendors/actions.ts`); the call site is
  `web/app/(authed)/vendors/[id]/edit/page.tsx` `onSubmit`. Inject a
  `queryClient.invalidateQueries({ queryKey: ["vendor", id] })` (and the
  list/burndown keys) before/after the `router.push`.
- The status is derived at render time from `last_review_date` +
  `review_cadence` (no stored status column), so the badge flips purely
  on the refetched row — no schema change.

Surfaced during slice 424, captured as a follow-up per continuous-batch
policy.
