# Slice 691 — decisions log (vendor detail refresh after recording a review)

- detection_tier_actual: manual_review
- detection_tier_target: playwright

AFK-type quality fix with a JUDGMENT scope expansion. The refresh gap was
originally surfaced by the slice-424 e2e (which deferred the status-flip
assertion to this slice). During implementation, code reading surfaced a
SECOND review-recording surface (slice 688's `reviews/new` form) that the
slice doc did not mention — it had the identical gap. That was caught by
manual*review (reading the vendors tree before writing), not by a test;
the \_target* tier is playwright, and the new `reviews/new` e2e test added
here is what would have caught it. No production bug shipped.

## Decisions made

### D1 — Fix BOTH review-recording surfaces, not only the edit form named in the slice doc

The slice doc (`docs/issues/691-vendor-detail-refresh-after-review.md`)
described the gap purely in terms of the edit form
(`web/app/(authed)/vendors/[id]/edit/page.tsx`, the slice-424 path that
sets `last_review_date` via PATCH) and said "add the `invalidateQueries`
call on the vendor mutation success path."

- **Why the doc under-specified.** The slice was filed as a slice-424
  spillover. Slice 688 subsequently landed a _dedicated_ "Record review"
  form at `web/app/(authed)/vendors/[id]/reviews/new/page.tsx` that POSTs
  to the `vendor_reviews` ledger and routes back to the detail — and its
  own preamble explicitly defers "the broader 'recording a review doesn't
  refresh' concern (slice 424)" to slice 691. So the slice title ("after
  recording a review") and the audit intent cover this form, even though
  the spillover text predates it.
- **Options considered.** (a) Fix only the edit form (literal slice-doc
  scope). (b) Fix both surfaces.
- **Chosen: (b).** Both surfaces append/record a review and `router.push`
  back to the read-only detail; both had the identical missing
  invalidation; the dedicated `reviews/new` form is the _more_
  semantically-correct "record a review" surface the slice title names.
  Fixing only the edit form would leave the named bug live on the primary
  surface — exactly the kind of symptom-level stop the project's
  root-cause discipline rejects. The `reviews/new` form is frontend-only
  and in the vendor-detail blast radius, so it is in scope, not a spill.
- **Confidence: high.**

### D2 — Which query keys to invalidate, and which to await

The detail page (`[id]/page.tsx`) reads two keys: `["vendor", id]` (the
summary + derived status badge + Last-review field) and
`["vendor-reviews", id]` (the slice-688 history timeline). The list page
(`vendors/page.tsx`) reads `["vendors", criticality, overdueOnly]` and
`["vendors-burndown", criticality]`.

- **Chosen.** Invalidate all four. **Await** the two detail/history keys
  (`Promise.all`) so their refetch is in flight _before_ the navigation;
  fire the two list/burndown keys with `void` (fire-and-forget) since the
  operator is navigating to the detail, not the list, so blocking nav on
  them buys nothing.
- **Prefix matching.** The list/burndown keys carry filter params, so the
  invalidations use the bare prefixes `["vendors"]` / `["vendors-burndown"]`
  — TanStack `invalidateQueries` matches by partial key prefix by default,
  marking every filter variant stale (AC-3: no stale row on return to
  `/vendors` regardless of the active filter).
- **Why await the detail key matters (the slice-424 race).** With the
  global 60s `staleTime` (`web/lib/queryClient.tsx`), a plain `router.push`
  with no invalidation serves the cached pre-review body on nav-back —
  this is the bug. Awaiting `invalidateQueries(["vendor", id])` while the
  edit/record page is still the active observer triggers an immediate
  refetch of the now-reviewed row; the await resolves once that refetch
  settles, so the detail page mounts against a fresh cache. This is
  deterministic — no fixed timeout, which the slice-424 memory note
  flagged as the failure mode of an earlier attempt.
- **Confidence: high.**

### D3 — Invalidation, not `router.refresh()`

Both surfaces already `router.push("/vendors/{id}")` (a client
navigation), and the detail reads its data via client-side TanStack
queries (not server components for the vendor body). A `router.refresh()`
re-runs server components but does not re-run a TanStack query whose
`staleTime` window is still open. So `invalidateQueries` is the correct
lever (it bypasses `staleTime` and forces the refetch); `router.refresh()`
would not reliably refresh the client-fetched detail body.

- **Confidence: high.**

## Tests

- Extended the slice-424 e2e `web/e2e/vendor-review-workflow.spec.ts`:
  - The existing `AC-2 + AC-3` (edit-form) test now asserts the deferred
    derived-status flip (`overdue` → `on time`) and the updated
    Last-review field after the save (AC-1/AC-2 — was previously a
    deliberately-skipped non-contractual assertion).
  - New `slice 691: recording a review on reviews/new refreshes the
detail` test drives the slice-688 record-review form end-to-end with a
    stateful hermetic mock (the `/api/vendors/{id}` GET flips
    overdue→on-time and the `/api/vendors/{id}/reviews` GET flips
    empty→one-row after the POST), asserting the flipped badge, updated
    Last-review date, and the new timeline row appear without a hard
    reload (AC-1/AC-2/AC-4). The two route patterns do not overlap (the
    bare-id glob has no trailing wildcard, so it does not match the
    `/reviews` suffix).
- No vitest added: the change lives in React page components, which the
  project's vitest tier (node-only, `.test.ts`) does not cover by design
  (slice 353 Q-3); the e2e tier is the component-behavior tier.

## Anti-criteria honored

- Frontend-only — `git diff --stat origin/main...HEAD -- internal/
migrations/ proto/ cmd/` is empty. No schema change (the badge re-derives
  off the refetched row; the server-side `last_review_date` recompute
  already exists at `internal/vendor/reviews.go`).
- Out of scope and untouched: calendar (`web/components/calendar/*`,
  `internal/api/calendar`), dashboard (`web/components/dashboard/*`) — owned
  by the parallel slice 732.
