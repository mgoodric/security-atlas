# 686 — Read-only vendor detail page with review history

**Cluster:** vendor
**Estimate:** S-M (0.5-1.5d)
**Type:** JUDGMENT (detail-page shape + review-history surface)
**Status:** `ready` — depends only on slice 679 (vendor edit page is the
sole view today), which is in review / merged.

## Narrative

Surfaced during slice 679 (vendor UX/data fixes), captured as a follow-up
per continuous-batch policy.

Slice 679 AC-3 asked to "consider a read-only vendor summary / review
history on the detail page — at minimum the edit page should not be the
only view." Slice 679 deferred this because it is materially larger than
the three defects that slice clustered (ATLAS-030/031/032):

- The vendor "detail" page today **is** the edit form
  (`web/app/(authed)/vendors/[id]/page.tsx` renders `<VendorForm>`). There
  is no read-only view — landing on a vendor drops you straight into an
  editable form.
- A "review history" is not yet a wire surface. `last_review_date` is a
  single scalar on the vendor row; there is no per-review ledger to render
  a history from.

This slice adds the read-only view (and decides whether review history is
in-scope for v1 or a further follow-on once a review ledger exists).

## Threat model

Read-only surface over data the operator already reads through the
vendor list + edit form. No new mutation, no new external IO. The detail
read goes through the existing RLS-scoped `GET /v1/vendors/{id}` (no new
query). No new wire surface unless review-history needs one — see AC-4.

## Acceptance criteria

- [ ] **AC-1.** A read-only vendor detail view renders the vendor's
      summary (name, domain, criticality, contract dates, DPA status,
      review cadence, last review, owner, notes) without the form inputs.
- [ ] **AC-2.** The vendor-list row name links to the read-only detail
      view; the detail view has an explicit "Edit" affordance that routes
      to the existing edit form. (Decide the route shape: e.g.
      `/vendors/{id}` read-only + `/vendors/{id}/edit` form, vs a
      view/edit toggle. JUDGMENT — record in decisions log.)
- [ ] **AC-3.** The owner renders as a `mailto:` link when it is a valid
      email (slice 679 makes the field email-validated).
- [ ] **AC-4.** JUDGMENT (decisions log): review history — either render a
      history section if a per-review ledger is in scope, OR record that
      v1 has only `last_review_date` (a single scalar) and a true history
      needs a `vendor_reviews` ledger filed as a further follow-on.
- [ ] **AC-5.** Tests: the detail view renders read-only (no input
      elements for the summary fields); the name link routes to the detail
      view; the edit affordance routes to the form. Playwright per
      `web/e2e/README.md` (hermetic route-mock).

## Anti-criteria

- The read-only view does NOT introduce a second `GET` shape — it reuses
  the existing `getVendor` BFF path.
- Does NOT add a migration unless AC-4 lands a `vendor_reviews` ledger
  (which, if chosen, is its own slice — keep this one read-only).

## Constitutional invariants honored

- **Invariant 6 (RLS):** the detail read reuses the RLS-scoped vendor
  store path; no new query escapes tenancy.

## Canvas references

- `Plans/canvas/06-risk.md` (vendor linkage) / vendor lite (slice 024)
- `CLAUDE.md` Invariant 6

## Dependencies

- Slice 679 (vendor edit page, owner-email validation, delete control) —
  this slice splits the view from the edit form 679 leaves as the sole
  view.

## Notes

Source: surfaced during slice 679 AC-3, captured as a follow-up per
continuous-batch policy. Slice 679's decisions-log D4 records the
deferral rationale.
