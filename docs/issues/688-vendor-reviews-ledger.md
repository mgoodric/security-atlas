# 688 — vendor_reviews ledger (per-review history surface)

**Cluster:** vendor
**Estimate:** M (1-2d)
**Type:** JUDGMENT (ledger shape + history-section UX)
**Status:** `not-ready` — depends on slice 686 (read-only vendor detail
page), which is the surface that would render the history. Slice 686 is
in review; this slice is the AC-4 follow-on it explicitly defers.

## Narrative

Surfaced during slice 686 (read-only vendor detail page), captured as a
follow-up per continuous-batch policy.

Slice 686 AC-4 asked whether a vendor "review history" was in scope. The
JUDGMENT call (decisions log D3, `docs/audit-log/686-vendor-detail-decisions.md`)
was to record the scalar-only reality and file the ledger as its own
slice rather than bolt a migration onto a read-only frontend slice:

- The vendor row carries a single `last_review_date` scalar (slice 024
  vendor lite). There is no per-review record — no reviewer, no outcome,
  no notes-per-review, no cadence-adherence trail.
- A true "review history" needs an append-only `vendor_reviews` ledger
  (one row per completed review) so the detail page can render a
  timeline, and so the board-pack burndown can eventually draw from a
  real review series rather than the single most-recent date.

Slice 686's detail page (`web/app/(authed)/vendors/[id]/page.tsx`)
already carries a "Review history" card that honestly names the gap
("v1 records a single last-review date … a per-review timeline arrives
with the vendor-review ledger"). This slice replaces that placeholder
with the real history once the ledger exists.

## Threat model

New tenant-scoped table → RLS is mandatory (invariant #6). A
`vendor_reviews` row is tenant-scoped exactly as `vendors` is; the four
RLS policies (select/insert/update/delete predicated on
`app.current_tenant`) must land with the migration, and the integration
suite must assert cross-tenant denial (the slice 033 / connector-pattern
RLS shape). The write path (recording a completed review) is a new
mutation surface — it goes through the RLS-scoped store, never a
caller-supplied `tenant_id`. No new external IO.

## Acceptance criteria

- [ ] **AC-1.** A `vendor_reviews` table (migration, idempotent +
      reversible) with at minimum: `id`, `tenant_id`, `vendor_id` (FK),
      `reviewed_at`, `reviewer` (email), `outcome`, `notes`,
      `created_at`. Append-only semantics (no in-place edit of a past
      review). Four RLS policies predicated on `app.current_tenant`.
- [ ] **AC-2.** `last_review_date` is derived from / kept consistent with
      the ledger (the most-recent `reviewed_at`), OR the migration
      back-fills one ledger row per existing non-null `last_review_date`
      so no history is silently lost. JUDGMENT — record in decisions log.
- [ ] **AC-3.** A read path (`GET /v1/vendors/{id}/reviews`, RLS-scoped)
      and its BFF (`/api/vendors/{id}/reviews`) returning the review
      series newest-first.
- [ ] **AC-4.** The slice-686 "Review history" card renders the real
      timeline (reviewer + date + outcome per row) when the ledger has
      rows, falling back to the honest scalar message when empty.
- [ ] **AC-5.** A write path to record a completed review (UI affordance + BFF + RLS-scoped store write).
- [ ] **AC-6.** Integration tests: RLS cross-tenant denial on read AND
      write; the back-fill produces exactly one row per pre-existing
      `last_review_date`; the read path orders newest-first.
- [ ] **AC-7.** Playwright (hermetic route-mock per `web/e2e/README.md`):
      the history card renders a multi-row timeline from a mocked series.

## Constitutional invariants honored

- **Invariant 6 (RLS):** new tenant-scoped table ships with the four RLS
  policies; integration suite asserts cross-tenant denial on read + write.
- **Invariant 2 (append-only ledger spirit):** review records are
  append-only — a past review is never edited in place.

## Canvas references

- `Plans/canvas/06-risk.md` (vendor linkage) / vendor lite (slice 024)
- `Plans/canvas/07-metrics.md` (board-pack vendor burndown — future
  consumer of a real review series)
- `CLAUDE.md` Invariant 6

## Dependencies

- Slice 686 (read-only vendor detail page) — the surface that renders
  the history; its "Review history" placeholder card is what this slice
  fills in. (in review — UNMET until merged)

## Notes

Source: surfaced during slice 686 AC-4, captured as a follow-up per
continuous-batch policy. Slice 686's decisions-log D3 records the
deferral rationale and confidence.
