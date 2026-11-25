# 3071 — A replay is not a control's latest state: decisions log

Issue: mgoodric/security-atlas#3071 (D3 from #3056 / PR #3070).

## Detection-tier classification

- **detection_tier_actual:** manual_review. The #3070 review saw that a
  replay row, stamped `evaluated_at` = the clock of the run, sorts as the
  control's latest row everywhere "latest" is read. No production code calls
  `Engine.Replay` or passes a horizon other than `FarFuture` (checked:
  `demoseed`, `api/controls/attest.go`, `eval/consumer.go`,
  `eval/exception_change.go`, `risk/residual_store.go`), so nothing shipped
  was wrong.
- **detection_tier_target:** integration
  (`TestReplay_OfAnEarlierInstantIsNotTheLatestState`,
  `TestFrameworkPosture_ReplayOfAnEarlierInstantIsNotTheLatestRun`,
  `TestExport_ReplayRunsRankByTheirJudgedInstant`,
  `TestReconcile_ReplayOfAnEarlierInstantDoesNotMoveThePlan`), each
  observed failing against main's queries or function body, plus a unit guard
  (`TestQueries_ControlEvaluationReadsHandleReplayRows`) for the next query
  that forgets.

## Decisions

### D1 — filter replay rows out of latest-state reads, not a separate lineage

The ticket offered two shapes: latest-state reads skip rows with
`evaluated_as_of IS NOT NULL`, or replays write to their own run lineage.
Chose the filter.

- **The AR needs replay rows in the same table.** Part 2 of the ticket is
  that the AR exposes `evaluated_as_of` on replay-derived observations. A
  separate lineage would keep replays out of the AR entirely, and the AR
  would need a union over two tables to see them.
- **The filter is exactly "judged now".** `evaluated_as_of` is set only when
  the run's horizon is before its clock (#3037 D2, #3056 D3). A live run
  passes `FarFuture` and writes NULL. So the predicate is not "trigger is
  replay" but "this row describes an earlier instant", which is the thing
  that must not be current state.
- **AC-7 still holds for recovery.** Deleting every row and replaying at a
  horizon at or after now writes NULL `evaluated_as_of`, so the rebuilt rows
  are live state again. Replaying an earlier horizon reproduces that
  horizon's state in its own rows, which is what the AC-7 and #3037 tests now
  read (`runStates`), instead of reading it back through `ControlState`.
- **A second table would duplicate the insert path, RLS policy, indexes and
  the append-only guard** for rows that differ from live rows by one column.
  The cost of the filter is that each latest-state read must carry it. The
  guard test makes forgetting it fail a unit test: every sqlc query that
  reads `control_evaluations` names `evaluated_as_of` or is on a short
  allowlist with its reason.

No API shape changes: every endpoint returns the same fields; what changes
is which row they come from.

### D2 — the reads that filter

Every read that picks a control's latest row or latest run, or counts rows
as current activity:

| Read                                                                 | Surface                                                  |
| -------------------------------------------------------------------- | -------------------------------------------------------- |
| `ListLatestControlEvaluations`                                       | `ControlState`, `GET /v1/controls/{id}/state`            |
| `ListActiveControlsWithState`, `CountActiveControls`                 | controls list, needs-attention                           |
| `ListInconclusiveStreaks`                                            | needs-attention "inconclusive since"                     |
| `ListControlEvaluationsForEffectiveness`                             | rolling 30-day effectiveness                             |
| `FrameworkPosture` (all four latest CTEs)                            | dashboard posture, coverage, trend, covered-by-exception |
| `ListFailingEvaluationsForPack`                                      | board pack open findings                                 |
| `ListCalendarEvents`                                                 | periodic-review "last evaluated"                         |
| `ListPassingControlsForDay`                                          | freshness drift                                          |
| `ListFrameworkScopeContradictions`                                   | framework-scope exclusions that still pass               |
| `ListSCFAnchorsLatestWithState`, `ListSCFAnchorsForVersionWithState` | anchor rollups                                           |
| `program_effectiveness` evaluator (inline SQL)                       | metrics                                                  |
| `action_plan_linked_controls_pass` (migration `20261312000000`)      | action-plan completed <-> verified                       |

The last one matters beyond display: the engine calls
`ReconcileVerification` after every run, replays included, and the function
read the latest run. A replay of T could have verified or un-verified a
plan on how its controls stood at T.

The `eval_cells` skip scans that enumerate a control's cells are left
unfiltered. A cell seen only in replay rows has no live row, so the
per-cell probe that follows returns nothing and the cell drops out.

### D3 — reads that keep replay rows

- `ListControlEvaluationHistoryPaged` (control detail history): the ledger
  listing, newest first, and it already returns each row's `trigger`. It is
  history, not state. It does not return `evaluated_as_of`; adding it would
  change the history API's shape, so it is left for a follow-up if wanted.
- `ListFailingEvaluationsAsOf`, `ListExceptedEvaluationsAsOf` (OSCAL AR and
  POA&M): see D4.

### D4 — the AR ranks runs by the instant each judged

The AR's `control_latest_run` now orders by
`COALESCE(evaluated_as_of, evaluated_at) DESC`, then `evaluated_at`, then
`created_at`. The `evaluated_at <= horizon` bound stays: a run must have
been written by the frozen horizon to be in the AR at all.

So a replay of an earlier instant written after a live run does not
displace it. A replay whose judged instant is the latest (no live run since)
is the control's latest run, and its excepted cells carry
`ExceptedCell.evaluated_as_of` (proto field 6), which the bridge renders as
an `evaluated-as-of` prop on the observation. `collected` stays
`evaluated_at`, when the row was written. An auditor reading an excepted
observation collected now that names an exception revoked before now sees
that the row judged an earlier instant, when that exception was in force.

Filtering replays out of the AR as well was rejected: the AR would then
never carry a replay-derived observation, and the ticket asks that it carry
one with its instant.

### D5 — existing tests that read a replay through ControlState

`TestReplay_ReproducesIdenticalStateFromLedger` (AC-7),
`TestReplay_AppliesExceptionsInForceAtTheHorizon` (#3037) and
`TestEvaluateControl_AsOfReplayUsesTheAsOfInstant` (#2911) read a replay's
result back through `ControlState(FarFuture)`, the exact behaviour this
ticket removes. Their assertions about what the replay computed are
unchanged; they now read the replay run's own rows (`runStates`). The #3037
test also asserts `ControlState` still serves the live run at T. The AC-7
test's initial run pins the same past horizon, so it is an as-of run too and
is read the same way.

The four as-of cases in `internal/control/stock_sql_checks_integration_test.go`
("replay measures from the as-of instant" and its siblings) evaluate with a
past horizon and then read `ControlState`. They now read the as-of row
directly (`evaluatedState`). Their live cases still go through
`ControlState`.

`isa/probes/ledger-replay.sh` (ISC-3) reads rows by trigger, not through
`ControlState`, and is unaffected.
