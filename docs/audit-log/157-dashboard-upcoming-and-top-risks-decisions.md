# 157 — Dashboard re-point: upcoming + top-risks (slice 147 follow-on) — decisions log

Slice 157 is the second half of slice 147 — the spillover that closes
the loop on the two slice-066 follow-on panels slice 147 didn't touch
(it scoped itself to the two `MissingEndpointPanel`-rendering panels;
the remaining two rendered real data with a labelled "partial data" gap
footer). The diagnosis was done in slice 147; the fix here is the
mechanical re-point.

This log records the build-time JUDGMENT calls so a maintainer can
re-evaluate.

## D-157-1 — Keep `getExpiringExceptions` and `ExpiringExceptionsResponse` in lib/api.ts

The slice-040 wiring for the upcoming panel called
`getExpiringExceptions(bearer, "30d")`. After this slice the dashboard
BFF no longer calls it — the panel now consumes the unified
`/v1/upcoming` rollup.

**Decision:** keep both `getExpiringExceptions` and
`ExpiringExceptionsResponse` in `web/lib/api.ts` for now.

**Why:** they are public exports — removing them now is a separate
breaking-change pass. Slice 147 took the same conservative path (it
added `getFrameworkPosture` and `getActivity` without removing the
slice-040 placeholder shims). A dead-code sweep is a future cleanup
slice that can collect both together with whatever else has accreted.

## D-157-2 — `getMitigateRisks` hard-codes `sort=residual,age`

`/v1/risks?treatment=mitigate` (slice 019) and
`/v1/risks?treatment=mitigate&sort=residual,age` (slice 066 AC-3) are
both valid. `getMitigateRisks` could accept an optional sort param.

**Decision:** hard-code the residual,age sort in the fn body. No new
optional argument.

**Why:** the dashboard top-risks panel is the only caller (verified
2026-05-18 — `grep -rnE "getMitigateRisks" web/`). Adding a knob no
caller uses is the kind of speculative-flexibility constitutional
Article VIII tells us not to add (`Plans/canvas/09-tech-stack.md`).
If a second caller appears wanting unsorted output, the right move is
a second fn — `getMitigateRisksUnsorted(bearer)` — not a knob on this
one. The fn's docstring now explicitly names the slice-066 sort
contract so the next reader understands why.

## D-157-3 — Empty-state copy is category-agnostic

The slice-040 empty-state copy was "No exceptions expire in the next 30
days." — accurate when the only data source was
`/v1/exceptions/expiring`. After the re-point the feed spans four
categories.

**Decision:** new empty-state copy is **"Nothing due in the next 30
days."** — category-agnostic.

**Why:** the panel header description already enumerates the four
categories ("Expiring exceptions, policy acknowledgments, vendor
reviews, audit milestones"). Repeating them in the empty-state copy
would be noise. Operators with truly nothing due across all four
categories see a short, unambiguous message.

## D-157-4 — Category badge palette

Each upcoming row now renders a category Badge per the
`upcomingWire.category` value.

**Decision:** map categories to shadcn Badge variants as
`exception -> destructive`, `audit_period -> secondary`, all others
(`policy_ack`, `vendor_review`) `-> outline`.

**Why:** the slice-066 unified rollup mixes four data sources whose
operator-attention urgency is not uniform. Exceptions are the only
category that represents an open accepted-risk window — they are
load-bearing for SOC 2 evidence and warrant a destructive (red) badge
the same way the dashboard's top-risks panel marks `treatment=mitigate`
risks. Audit-period milestones are calendar-fixed but actionable;
`secondary` matches their weight. Policy-ack and vendor-review are
recurring routine work; `outline` keeps them visually quiet so the
critical rows stand out.

## D-157-5 — Status row flip goes ready -> in-review directly

The canonical `_STATUS.md` row 157 (line 2919) was at `ready` at branch
fork. A parallel PR (#318, `chore/status-batch-56-claim-stake`) flips
it to `in-progress` in a separate commit — that PR has not merged at
slice-157 PR-open time.

**Decision:** on this branch, flip the canonical row directly from
`ready` to `in-review`.

**Why:** the in-progress flip is bookkeeping rendered moot once the PR
is up for review — the work shipped past the `in-progress` state
already. Reverting `in-progress` on top of `ready` in a single commit
matches the slice 147 precedent (its canonical row jumped multiple
states in one chore commit when the parallel claim-stake PR was still
in flight). Reconcile-time the maintainer can merge the two histories.

## D-157-6 — Test bearer fixture is `test-bearer-157`

**Decision:** all vitest cases use `test-bearer-157` as the fake bearer
token.

**Why:** the slice-100 secret-scanning convention (GitGuardian)
requires neutral test strings with no vendor token prefixes. Slice 147
used `test-bearer-147`; same convention here, bumped by slice number
for grep-ability.

## D-157-7 — No backend changes, no integration tests

**Decision:** zero edits under `internal/api/`. No new integration
tests.

**Why:** P0-148-1 anti-criterion. Both endpoints already exist and are
tested at the dashboard handler layer:

- `/v1/upcoming` integration test: `internal/api/dashboard/integration_test.go`
  `TestUpcoming_RollupMergesAllFourSourcesDateSorted` (ISC-21)
- `/v1/risks?sort=residual,age` integration test:
  `internal/api/risks/sort_integration_test.go` (ISC-20)
- Empty-tenant integration for `/v1/upcoming`:
  `internal/api/dashboard/empty_set_integration_test.go` (slice 150 —
  asserts `{upcoming: [], count: 0}` on a fresh-install zero-row tenant)

The slice-147 spillover precedent: when the backend is fully covered,
the frontend re-point ships with vitest-only BFF coverage. Adding a
duplicate integration test here would be redundant cycles burned in
CI.
