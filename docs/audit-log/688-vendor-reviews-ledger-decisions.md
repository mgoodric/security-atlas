# 688 — vendor_reviews ledger · decisions log

JUDGMENT slice (ledger shape + history-section UX). The subjective calls
below were made at build time per the slice-development workflow
(`Plans/prompts/04-per-slice-template.md` "Slice types"). Recorded here for
post-deployment iteration; this log does NOT block merge.

- detection_tier_actual: integration
- detection_tier_target: integration

A real bug surfaced during the slice and was caught at the cheapest tier
that could catch it: the OpenAPI coverage-drift guard (a
contract/integration-class CI check, reproduced locally via
`bash scripts/check-openapi-drift.sh`) flagged the two new
`/v1/vendors/{id}/reviews` routes as registered-but-undeclared before
push. Fixed by adding the two `RouteSpecs` entries + regenerating
`docs/openapi.yaml`. This is the documented `feedback_local_ci_parity`
class: the drift would have been a red CI job, and was caught locally
instead. No `target=production` gap; the contract guard did its job.

## Decisions made

### D1 — Ledger shape: append-only `vendor_reviews`, immutable rows, four-policy RLS

**Options considered.**

1. A standalone append-only `vendor_reviews` table — one row per completed
   review — with `id, tenant_id, vendor_id (composite FK), reviewed_at,
reviewer, outcome (enum), notes, created_at`. No `updated_at`; rows are
   immutable. Four RLS policies mirroring `vendors`.
2. A JSONB `review_history` array column on the `vendors` row.
3. Reuse the generic evidence ledger (`internal/evidence`) with a
   `vendor_review` evidence_kind.

**Chosen:** Option 1.

**Rationale.** A first-class table is what AC-1 asks for and what every
downstream consumer wants: the board-pack vendor burndown (canvas §7) will
eventually draw a real review _series_, the detail page renders a timeline,
and RLS isolation is a per-row DB guarantee rather than a JSONB-blob
application concern. Option 2 (JSONB array) cannot be indexed for
newest-first reads without a GIN/expression index, cannot carry per-review
RLS, and re-introduces the in-place-mutation smell the append-only
invariant (canvas #2) exists to prevent — appending to a JSONB array is a
row UPDATE, which is exactly the "evaluation corrupts the record" failure
mode. Option 3 (generic evidence ledger) over-couples a TPRM history to the
evidence-engine machinery (schema-registry `evidence_kind`, receipts,
content-hashing) that a vendor-review row does not need; the evidence
ledger is for _connector-emitted_ evidence, not operator-recorded TPRM
events. The composite FK `(tenant_id, vendor_id) -> vendors(tenant_id, id)`
keeps the cross-tenant door shut at the DB and CASCADE-deletes a vendor's
history with the vendor.

**Immutability.** There is intentionally no `updated_at` column and no
`UpdateReview` / `DeleteReview` store method — a recorded review is
immutable (canvas Invariant 2, append-only spirit). The migration still
ships all four RLS policies (incl. `tenant_update`) for shape-parity with
every other tenant-scoped table — the byte-for-byte mirror of `vendors`
keeps the `audit-rls.sh` invariant-6 guard and future contributors'
muscle memory consistent. The append-only guarantee is a store-layer
invariant, not a DB trigger.

**Outcome enum.** Four buckets — `pass`, `pass_with_findings`, `fail`,
`waived`. `waived` is deliberate: a risk-accepted skip is recorded so the
cadence trail is honest about the gap rather than silently missing.

### D2 — `last_review_date` consistency: keep the scalar in sync + back-fill (AC-2)

**Options considered.**

1. On each recorded review, set `vendors.last_review_date` to the
   most-recent `reviewed_at` on the ledger; AND back-fill one ledger row per
   existing non-null `last_review_date` in the migration.
2. Derive `last_review_date` live from the ledger (view / computed read),
   dropping the scalar.
3. Back-fill only; leave the scalar untouched by the write path (the ledger
   and the scalar drift independently).

**Chosen:** Option 1.

**Rationale.** The scalar is load-bearing: the overdue/burndown SQL
(slice 024 / 122 / 273) reads `vendors.last_review_date` directly, indexed.
Dropping it (Option 2) would force a re-write of every overdue query and
its index — out of scope and risky. Letting it drift (Option 3) would make
the detail page's "Last review" summary disagree with the timeline's
newest row. So the store keeps the scalar consistent: after appending a
review it sets `last_review_date` to `MAX(reviewed_at)` over the ledger
(the just-inserted row participates), which means a _back-dated_ review
never regresses the scalar below an existing newer review. The migration
back-fills exactly one ledger row per pre-existing non-null
`last_review_date` (empty reviewer — the scalar never recorded who; default
`pass` outcome; a notes breadcrumb marking it a back-fill) so no history is
silently lost. AC-6 asserts the one-row-per-scalar back-fill and the
no-regress behavior; both pass against live Postgres.

**Scope note.** This slice does NOT touch the post-recording _refresh_ of
the detail page (the slice-424 "recording a review doesn't refresh"
concern) — that is slice 691's job. The record-review form navigates back
to the detail route on success, which re-runs the detail + reviews
queries; the broader refresh/invalidation story is left to 691 as the
parent prompt directs.

### D3 — History-section UX: timeline-when-rows, honest-scalar-when-empty

**Options considered.**

1. Replace the slice-686 placeholder card with a per-review timeline
   (reviewed date + outcome Badge + reviewer + notes, newest-first) when
   the ledger has rows; fall back to an honest "no per-review history
   recorded yet" message (surfacing the scalar last-review date when one
   exists) when empty. Add a "Record review" affordance routing to a
   dedicated `/vendors/{id}/reviews/new` form (AC-5), mirroring the
   slice-686 read/edit split (write affordances live on their own route,
   not bolted onto the read-only detail).
2. An inline "add review" form embedded directly in the read-only detail
   card.
3. A modal/dialog record-review affordance.

**Chosen:** Option 1.

**Rationale.** The read-only detail page is exactly that — read-only
(slice 686 AC-1 asserts zero form controls in the `vendor-detail`
subtree). Embedding a form (Option 2) would re-introduce form inputs into
the read-only view and break that assertion. A dedicated `reviews/new`
route mirrors the established `[id]` (read) vs `[id]/edit` (write) split,
keeps the detail page honest, and is the lowest-surprise choice for an
operator who already learned that pattern. A modal (Option 3) adds a
dialog primitive the vendor surface does not otherwise use and is harder
to deep-link / test. The timeline is a non-blocking secondary fetch: the
summary renders even if the history is still loading or errors, and an
empty series (or an error) falls back to the scalar message — so a
reviews-endpoint hiccup never blanks the whole detail page. Outcome
renders as a Badge whose variant flags a `fail` destructively (the one
outcome an operator must not miss); the label/variant mapping lives in
pure `detail-view.ts` helpers covered by fast vitest unit tests.

## Constitutional invariants honored

- **Invariant 6 (RLS).** `vendor_reviews` ships the four RLS policies +
  `FORCE ROW LEVEL SECURITY`, mirroring `vendors` byte-for-byte. The
  `audit-rls.sh` invariant-6 guard passes ("all tenant-scoped tables carry
  a policy + FORCE"). The integration suite asserts cross-tenant denial on
  READ (B sees an empty series for A's vendor) AND WRITE (B's record
  against A's vendor maps to `ErrVendorNotFound` via the invisible FK).
- **Invariant 2 (append-only ledger spirit).** Rows are immutable; no
  `updated_at`, no update/delete store path. A past review is never edited
  in place.

## Test evidence (run against live Postgres on this worktree)

- Pure-Go unit: `TestReviewOutcome_Valid`, `TestValidateReview` (table) —
  PASS.
- Integration (`-tags=integration`, real Postgres + atlas_app NOBYPASSRLS):
  `TestRecordReview_RoundTrip`, `TestRecordReview_BackdatedDoesNotRegressScalar`,
  `TestListReviews_NewestFirst`, `TestListReviews_RLSIsolatesCrossTenant`,
  `TestRecordReview_RLSIsolatesCrossTenantWrite`,
  `TestBackfill_OneRowPerLastReviewDate` — all PASS. Full vendor +
  api/vendors integration suite: no regression.
- Frontend vitest: 15/15 in `detail-view.test.ts` (incl. the new
  `reviewOutcomeLabel` / `reviewOutcomeBadgeVariant` helpers) — PASS.
- `tsc --noEmit` clean; `eslint` 0 errors; `gofmt -l` clean;
  `golangci-lint` 0 issues; sqlc regen idempotent + committed; OpenAPI
  drift guard clean (241 routes).
- Playwright `vendor-review-history-688.spec.ts` (hermetic route-mock) runs
  in CI.
