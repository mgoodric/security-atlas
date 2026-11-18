# Framework scope activation cannot backdate across a freeze — decisions log (#2078)

Found in the review of #2076 (#1792). `Activate` accepted any
`effective_from`, and the frozen-period SSP picked a revision by
`effective_from` alone. A revision activated after a freeze, with
`effective_from` before it, changed what a re-export of the frozen period
reported. Matt's decision on the ticket (2026-11-15): refuse it. `Activate`
returns 409 for an `effective_from` earlier than the latest frozen audit
period's freeze on the framework version. Frozen-period exports read the
revision active at freeze time.

detection_tier_actual: manual_review
detection_tier_target: integration

## Decisions

**D1: "Active at freeze time" is `effective_from <= frozen_at AND
activated_at <= frozen_at`.** The refusal alone leaves a race. An activation
that reads "no frozen period" can commit after a freeze whose `frozen_at` is
earlier than the activation's `effective_from`, and the effective_from-only
pick would then rewrite the frozen period. The new `activated_at` column
records when the revision came into existence. A frozen period sees only a
revision that existed at its freeze. The refusal is the operator-facing
answer, and the column is the guarantee.

**D2: The refusal compares against the latest frozen period on the same
framework version, strictly earlier.** `effective_from == frozen_at` is
accepted. D1 keeps such a revision out of that frozen period, because it was
activated after the freeze. Open periods, and frozen periods on other
framework versions, do not block. The error is
`frameworkscope.ErrEffectiveBeforeFreeze` (HTTP 409). The refused revision
stays `approved`, so it can be activated again with a later `effective_from`.

**D3: The `activated_at` rule applies to the frozen-period SSP read only.**
`ListExcludedRequirementsAsOfForExport` (the audit-period SSP, which is the
read pinned to a `frozen_at`) carries it. `GetFrameworkScopeAsOf` keeps
selecting by `effective_from`. That covers the general `?as_of=` reads: the
scope list, applicability, and the SoA export's `as_of`. Applied there, the
rule broke four existing tests that model planning history with a backdated
`effective_from` (applicability, contradiction, predicate-frozen, SoA
export). It also contradicts backdating for planning, which the ticket keeps.
With D2 in place, those reads at any frozen period's `frozen_at` already
return the revision in force then. The one exception is the D1 race. The SoA
export has no audit-period selector today, so it has no frozen-period read to
pin. If a frozen-period SoA export is added, it should use the SSP query's
predicate.

**D4: Backfill.** Existing `activated` and `superseded` rows take the latest
`activate` entry in `framework_scope_workflow_log` (#1786 onwards), which is
the recorded activation time. Rows older than the log take
`LEAST(effective_from, updated_at)`. `updated_at` is at or after the real
activation, so the value is never later than the activation. Because it is
also never later than `effective_from`, the as-of result for every legacy row
is exactly what it was before the migration. A legacy row that was already
backdated across a freeze stays visible to that freeze. The migration cannot
tell such a row apart, and choosing `updated_at` instead would hide correctly
dated legacy rows from freezes between their activation and their last
update. Draft, review and approved rows stay NULL. The UPDATE only touches
NULL rows, so a re-run is a no-op. The predicate-frozen trigger
(`framework_scopes_predicate_frozen`) refuses any column change on an
activated or superseded row, so the migration disables it for the backfill
statement only.

**D5: A trigger stamps `activated_at`, not the activate query.**
`framework_scopes_stamp_activated_at` sets `now()` on the move into
`activated`, and on any direct INSERT of an activated or superseded row.
Seeds (`internal/demoseed`) and several integration fixtures insert such
rows directly. The trigger keeps them valid under the new CHECK
(`framework_scopes_activated_at_set`: an activated or superseded row has
`activated_at`) without touching each writer. The predicate-frozen trigger's
allow-list does not include the column, so `activated_at` cannot be edited on
an activated or superseded row.

**D6: #2076's fixture activates rev A before the freeze.** The old fixture
activated rev A after the freeze with `effective_from = freeze − 1h`, which
is now refused. Rev A is activated while the period is open, and the period
is then frozen at rev A's `activated_at`. This still covers a backdated
revision that existed at the freeze being seen. The old post-freeze
backdated activation is now its own test, which expects
`ErrEffectiveBeforeFreeze` and an unchanged export. The re-export test
activates rev D after the freeze with `effective_from = frozen_at` and checks
the export is unchanged. It was observed failing with the `activated_at`
predicate removed, because rev D's exclusion replaced rev A's.
