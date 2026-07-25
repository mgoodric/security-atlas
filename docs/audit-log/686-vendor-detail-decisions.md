# 686 — Read-only vendor detail page · decisions log

JUDGMENT slice. The subjective calls below were made at build time per the
slice-development workflow (`Plans/prompts/04-per-slice-template.md`
"Slice types"). Recorded here for post-deployment iteration; this log does
NOT block merge.

- detection_tier_actual: none
- detection_tier_target: none

(No bug surfaced during the slice. The work was a clean frontend split +
new read-only view; typecheck, lint, full vitest suite, and the
production build all passed first time. The Playwright tier runs in CI.)

## Decisions made

### D1 — Route shape: `[id]` read-only + `[id]/edit` form (NOT a toggle)

**Options considered.**

1. `/vendors/{id}` = read-only detail; `/vendors/{id}/edit` = the form
   (relocate the existing edit page into an `edit/` subroute).
2. A single `/vendors/{id}` page with a client-side view/edit toggle
   (no second route).
3. Keep `/vendors/{id}` as the form and add a new `/vendors/{id}/view`
   read-only route.

**Chosen:** Option 1.

**Rationale.** Every other detail surface in the app already uses
`[id]` = read-only: `risks/[id]` (slice 681, the freshest precedent),
`policies/[id]` (slice 672), `controls/[id]`. Landing on a vendor should
show a summary, not drop the operator into an editable form — that is the
exact complaint slice 679 AC-3 logged and slice 686 exists to fix.
Option 2 (toggle) keeps the form perpetually one state-flip away, which
re-introduces the "the detail IS the form" smell and complicates the
read-only AC-1 assertion (the form inputs still live in the component
tree). Option 3 makes the canonical `[id]` route the _form_, which is
backwards from every sibling and breaks the muscle memory that `[id]` is
the safe read landing. The relocation is mechanical: the edit page moved
to `[id]/edit/page.tsx` with its relative imports re-pointed
(`../../vendor-form`, `../../actions`, `../../delete-vendor-button`); the
shared form/actions/delete components stay at the vendors root and are
reused unchanged. The list row's existing `href={/vendors/${v.id}}` now
lands on the read-only detail with no list edit needed (AC-2). The
edit-form post-save redirect was changed from `/vendors` to
`/vendors/{id}` so saving returns the operator to the detail they were
editing; delete still routes to `/vendors` (the row is gone).

**Confidence:** high.

### D2 — Owner mailto reuses the slice-679 `isEmail` predicate (AC-3)

**Options considered.**

1. Reuse `isEmail` from `web/lib/email.ts` (slice 679, already tested +
   coverage-floored) to gate the `mailto:` link.
2. Author a fresh email check inside the detail component.

**Chosen:** Option 1, wrapped in a small `ownerMailto()` helper in
`detail-view.ts`.

**Rationale.** Slice 679 made the owner field email-validated with
exactly this predicate; the detail view must agree with the form on what
"an email" is, or a value the form accepted could fail to linkify (or
vice versa). Reusing the predicate guarantees that agreement and avoids a
second regex drifting from the first. A role string ("Head of Security")
or a blank owner renders as plain text, never a broken `mailto:`. The
`mailto:` scheme is fixed in code; only the validated local+domain comes
from data, and `EMAIL_RE` forbids whitespace and stray `@`, so there is
no header-injection surface in the href.

**Confidence:** high.

### D3 — Review history: record the scalar-only reality, file the ledger as slice 688 (AC-4)

**Options considered.**

1. Render a history section now by adding a `vendor_reviews` ledger
   (migration + store + read path) in this slice.
2. Record that v1 carries only the `last_review_date` scalar, surface it
   honestly, and file the ledger as a follow-on slice (the prompt's
   DEFAULT LEAN).

**Chosen:** Option 2. Filed **slice 688** (`docs/issues/688-vendor-reviews-ledger.md`,
status `not-ready` — depends on this slice merging).

**Rationale.** A true per-review history needs an append-only,
RLS-scoped `vendor_reviews` table — a backend + migration change that is
materially larger than a read-only frontend slice and carries its own
RLS-policy + back-fill + integration-test surface (invariant #6). The
slice 686 anti-criterion is explicit: do NOT add a migration unless a
ledger lands, and the ledger is its own slice. Faking a timeline from a
single scalar would be dishonest (the "continuous monitoring that's
actually 24-hour polling" anti-pattern, applied to history). The detail
page therefore carries a "Review history" card that names the gap
plainly: "v1 records a single last-review date (<date>) … A per-review
timeline arrives with the vendor-review ledger." Slice 688 replaces that
placeholder with the real timeline.

**Confidence:** high (the deferral is the documented default; the only
open shape is slice 688's own — ledger columns + back-fill semantics).

### D4 — A "Review history" placeholder card vs. nothing

**Options considered.**

1. Render an explicit "Review history" card that names the v1 limitation.
2. Render nothing (the last-review date already appears in the summary).

**Chosen:** Option 1.

**Rationale.** The slice 686 AC list and slice 679's deferral both frame
"review history" as a first-class expectation. Silently omitting it would
leave the next reader (or the maintainer) re-asking "where's the
history?" — the question this slice exists to settle. A named card that
points forward to the ledger is the honest, self-documenting surface and
gives slice 688 an obvious render target. The card reuses the summary's
`last_review_date` so it stays consistent with the field above it.

**Confidence:** medium (the exact copy is a UX call the maintainer may
re-word once real operators see it).

## Revisit once in use

- **D1 redirect-after-save.** Re-check that returning to `/vendors/{id}`
  (the detail) after a save is the flow operators want, vs. returning to
  the list. If operators routinely edit-then-edit-another, the list might
  be the better landing. Low-cost to flip.
- **D3 → slice 688.** This is the top of the iteration backlog: once a
  real review cadence runs against real vendors, the single-scalar
  `last_review_date` will feel thin. Pick up slice 688 (ledger +
  back-fill + history render) at that point. Decide there whether
  `last_review_date` becomes a derived view over the ledger or stays a
  denormalized cache.
- **D4 placeholder copy.** Re-word the "Review history" card once the
  ledger lands (it becomes a real timeline) or once an operator reads the
  v1 message and finds it confusing.
- **Summary field completeness.** The detail renders name, domain,
  criticality, contract start/end, DPA status, review cadence, last
  review, owner, notes, and (when present) linked SOW. If operators want
  scope-cell bindings or created/updated timestamps surfaced here, that
  is a cheap additive follow-up.

## Confidence summary

| Decision                                           | Confidence |
| -------------------------------------------------- | ---------- |
| D1 route shape (`[id]` read-only + `[id]/edit`)    | high       |
| D2 owner mailto reuses `isEmail`                   | high       |
| D3 defer ledger → slice 688, record scalar reality | high       |
| D4 named "Review history" placeholder card         | medium     |
