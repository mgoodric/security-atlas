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
activated_at <= frozen_at`.** `frozen_at` is the instant the freeze names.
`period.Store.Freeze` takes it before its transaction (or from the caller), so
an activation can commit after that instant but before the freeze commits.
That activation read "no freeze", so the refusal could not stop it. Selecting
by `effective_from` alone would then put it in the frozen period. The new
`activated_at` column records when the revision came into existence, and a
frozen period sees only a revision that existed at its `frozen_at`. The
refusal is the operator-facing answer, and the column is the guarantee.
`TestSSP_ExcludedRequirements_RevisionActivatedAfterFrozenAtNotSeen` builds
this case.

**D2: The refusal compares against the latest frozen period on the same
framework version, and refuses `effective_from <= frozen_at`.** Matt's
decision says "earlier than", and this goes slightly further, as review asked.
At equality the SSP is already safe because of D1. The general `as_of` read
behind the SoA export is not (D3): an SoA at `?as_of=frozen_at` would pick a
revision effective exactly at the freeze and activated after it. Refusing
equality keeps that read on the revision in force at the freeze
(`TestSoAExport_AsOfFrozenAt`). An activation has to be effective strictly
after every freeze on its framework version. Open periods, and frozen periods
on other framework versions, do not block. The error is
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
With D2 and D7 in place, those reads at any frozen period's `frozen_at`
already return the revision in force then. The one exception is the D1 case,
a `frozen_at` taken before an activation that committed first. The SoA
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
`framework_scopes_stamp_activated_at` sets `clock_timestamp()` on the move into
`activated`, and on any direct INSERT of an activated or superseded row.
Seeds (`internal/demoseed`) and several integration fixtures insert such
rows directly. The trigger keeps them valid under the new CHECK
(`framework_scopes_activated_at_set`: an activated or superseded row has
`activated_at`) without touching each writer. The predicate-frozen trigger's
allow-list does not include the column, so `activated_at` cannot be edited on
an activated or superseded row. `clock_timestamp()`, not `now()`: `now()` is
the transaction start, before Activate waits on the D7 lock, so it could
predate a freeze that committed while the activation waited.

**D6: #2076's fixture activates rev A before the freeze.** The old fixture
activated rev A after the freeze with `effective_from = freeze − 1h`, which
is now refused. Rev A is activated while the period is open, and the period
is then frozen at rev A's `activated_at`. This still covers a backdated
revision that existed at the freeze being seen. The old post-freeze
backdated activation is now its own test, which expects
`ErrEffectiveBeforeFreeze` and an unchanged export. The re-export test
activates rev D after the freeze, with `effective_from = frozen_at + 1s`, and
checks the export is unchanged. The D1 case is its own test.

Every new test was observed failing without the code it covers:

- the D1 test, with the SSP's `activated_at` predicate removed;
- `TestSoAExport_AsOfFrozenAt`, with the refusal relaxed back to `<`;
- `TestActivate_SerialisedAgainstFreeze`, with Activate's lock removed.

**D7: Activation and freeze are serialised by an advisory lock.** Namespace 10,
`advisorylock.FrameworkScopeFreeze`, keyed on `hashtext(tenant || '/' ||
framework_version)` and taken by the `LockFrameworkScopeFreeze` query. Activate
takes it before reading the latest freeze. `period.Store.Freeze` takes it after
reading the period, which names the framework version, and before writing
`frozen_at`. Without it, an activation could read "no freeze" while a freeze
on the same version committed, then activate across it. Two tests force the
interleaving. `TestActivate_SerialisedAgainstFreeze` holds the lock in a
transaction that freezes the period, checks that Activate waits, commits, and
checks that Activate then refuses. `TestFreeze_TakesFreezeLock` checks that
Freeze waits on the same lock.

**D8: The backfill runs in one transaction.** `deploy/docker/bootstrap/migrate.sh`
(behind `just migrate-up`, the compose `atlas-migrate` service and
`scripts/scratch-pg.sh`) applies each file with `psql --single-transaction`,
together with its `schema_migrations` row. The DISABLE TRIGGER / UPDATE /
ENABLE TRIGGER sequence is atomic, so a failure leaves the trigger enabled and
the ledger row unwritten. CI's own test loops in `ci.yml` (forward apply,
up→down→up) use plain `psql -v ON_ERROR_STOP=1 -f` against a throwaway
database. There, a failure stops the job, and the half-applied database is
discarded.
