# 3037 — Replay applies the exceptions in force at its horizon: decisions log

Issues: mgoodric/security-atlas#3037 (the replay change, merged in #3054) and
mgoodric/security-atlas#3056 (the follow-ups from the #3054 review, which
asked for this log).

## Detection-tier classification

- **detection_tier_actual:** manual_review. The #3054 review found that a
  replay row's waiving exception was rebuilt at the wrong instant in the
  OSCAL AR, and that nothing in the schema guaranteed the lifecycle stamps
  replay reads. No production caller replays yet, so no exported AR was
  wrong.
- **detection_tier_target:** unit for the attribution
  (`TestExceptedCells_ReplayRowAttributesAtItsInstant`, observed failing
  against the old `excepted.go`), integration for the stamps
  (`TestExceptions_LifecycleStatesCarryTheirStamps`, observed failing with
  the migration reversed).

## Decisions

### D1 — no exception history table (#3037)

A replay of T rebuilds "the exceptions in force at T" from the row's own
lifecycle stamps (`ListExceptionsByControlAsOf`: `activated_at <= T` and
`revoked_at` NULL or after T), then applies the `[effective_from,
expires_at)` window. No separate history table is kept.

This is exact because every transition into or out of `active` stamps a
column and nothing rewrites the window afterwards: `ActivateException` sets
`activated_at` and `effective_from`, `RevokeException` sets `revoked_at`,
`ExpireActiveExceptionsBefore` sets `expired_at`, and no query writes
`expires_at` after the INSERT (#2526). A history table would duplicate those
stamps and could drift from them. If an edit path for `effective_from` or
`expires_at` is ever added, it must record the old window, or this
reconstruction stops being exact; `oscal/excepted.go`'s `inForceAt` carries
the same note.

#3056 makes the stamps a schema guarantee rather than a convention:
`exceptions_activated_has_stamp` (active, expired and revoked rows carry
`activated_at`) and `exceptions_expired_has_stamp` (expired rows carry
`expired_at`). `exceptions_revoked_has_actor` already required `revoked_at`.
Without the first, an active row with a NULL `activated_at` was excepted
live and never on replay.

### D2 — replay is `asOf < evaluatedAt` (#3037)

The engine treats a run as a replay when its horizon `asOf` is before the
database clock it read for `evaluated_at` (`queryInstant`), not by the
`trigger` label. A replay reads the as-of exception set; a live run reads
the current active set. The trigger is a label a caller picks; the
comparison is what determines which instant the run's inputs describe, so
the read follows it. A live run passes `FarFuture`, so it never qualifies.

### D3 — record the instant a replay row judged (#3056)

A replay row is stamped `evaluated_at` = the clock of the run, because
`evaluated_at` orders the ledger. The OSCAL AR rebuilt the waiving
exception at `evaluated_at`, so a replay of T that was `excepted` could
name no exception (one revoked between T and the replay, for instance).

The ticket offered two fixes: record the instant on the row, or keep replay
rows out of "latest state" and the AR. This log takes the first.
`control_evaluations.evaluated_as_of` is set when the run judged an earlier
instant than `evaluated_at` and is NULL on live rows; `exceptedCells` reads
the exception in force at `evaluated_as_of` when it is set. The AR's
`evaluated_at` still reports when the row was written.

Keeping replay rows out of "latest state" would change what the dashboard,
`ControlState` and the AR report after a replay. That is a product-semantics
call and is not made here. As the ledger stands, a replay row becomes the
control's latest state, as it did before #3056, and now its AR attribution
is right.

### D4 — the migration backfills only from the audit log

Existing rows missing a stamp take it from the `exception_audit_log` row
that recorded the transition (`activated`, `expired`). A row with no audit
row is not given an invented stamp such as `updated_at` or `expires_at`: the
migration raises and says how many rows need one. Every write path pairs
the state change with an audit row in one transaction, so only rows written
by hand or by an old fixture can hit this. The demo seeder now stamps its
`expired` rows (`activated_at` and `expired_at`), and the test fixtures that
seeded `active` rows with no `activated_at` now set it.

### D5 — the replay test drives the real revoke and expiry paths

`TestReplay_AppliesExceptionsInForceAtTheHorizon` revokes through
`exception.Store.Revoke` and expires through `Expirer.SweepOnce`, rather than
copying their SQL. To expire without waiting for the wall clock, `Expirer`
gained `WithNow`, matching `IdempotencySweeper.WithNow`. It moves only the
threshold rows are compared against; `expired_at` is still the database
clock. The roughly 2 s sleep is gone.
