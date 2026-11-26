# Issue #2911: the evaluation instant in SQL and Rego evidence queries

Build-time calls made for
[#2911](https://github.com/mgoodric/security-atlas/issues/2911), which also
closes [#2890](https://github.com/mgoodric/security-atlas/issues/2890). Matt's
decision (2026-11-21): the SQL check sandbox gets the evaluation instant, and
ADR 0028 d1 records the amendment.

`detection_tier_actual = manual_review` (raised while writing the cc4_1 check
in #2822) · `detection_tier_target = unit` (a fixture with pinned dates would
have failed against `now()` the day its window passed).

## D1: a constant column, not a bound parameter

The ticket allowed either. `evaluated_at` is a column of the `evidence` CTE,
bound once as `$2::timestamptz`, so every row carries the same value.

- An author reads it by name, next to `observed_at`, and the static guard
  already accepts `evidence.<column>`. A bare `$2` in an author's SQL is
  positional and says nothing.
- It works inside an aggregate without a subquery:
  `bool_and(observed_at >= evaluated_at - interval '24 hours')`.
- Cost: over an empty record set there is no row to read it from. A query
  already has to handle that case itself (cc6_1's `count(*) = 0` branch), and
  a window check has nothing to compare then anyway.

## D2: which instant

`queryInstant(evaluatedAt, asOf)`: the evaluation's own time, or the as-of
horizon when that is earlier. Live evaluation passes `FarFuture`, so it gets
the evaluation time. A replay or an as-of evaluation gets its horizon. The
fixture runner passes the case's `evaluated_at` (or the runner's `now`).

The ticket says "the engine's `evaluatedAt` (the as-of time for replays)".
On `main` those were two values: `evaluatedAt` is always the wall clock, and
`asOf` only bounds which evidence is read. So the call is the earlier of the
two, which is the as-of time for a replay and `evaluatedAt` otherwise.

**Not changed:** the freshness window (`inWindowRecords`, `computeFreshness`)
is still measured from `evaluatedAt`, the wall clock, as before. A replay as
of an instant older than the class's window therefore still finds no
in-window evidence. Moving the freshness window to the same instant changes
what replays and audit-period re-evaluation report and is outside this
ticket. It is raised as a follow-up, not done here.

## D3: the wall-clock guard covers stock bundles only

`eval.SQLUsesWallClock` matches `now()`, `current_timestamp`,
`current_date`, `current_time`, `localtimestamp`, `localtime`,
`clock_timestamp()`, `statement_timestamp()`, `transaction_timestamp()`,
`timeofday()` and the `'now'` / `'today'` / `'tomorrow'` / `'yesterday'`
input literals. `TestStockSQLChecksNeverReadTheWallClock` runs it over every
SQL query under `controls/soc2/`.

It is not applied to a tenant's query at upload or at evaluation. Refusing at
evaluation would turn an already-uploaded tenant control inconclusive on
deploy. Refusing at upload would be a behaviour change to the upload API that
the ticket did not ask for ("consider a static guard ... in stock SQL
bundles"). The spec tells authors to use `evaluated_at`. The regex is lexical,
so a JSON key literally named `'now'` would trip it. No stock bundle has one.

## D4: Rego in the same change (#2890)

`evalRegoQuery` takes the same instant and passes it as `rego.EvalTime`, so
`time.now_ns()` in a policy is the evaluation instant. `observed_at` is
normalised to UTC before it reaches the policy. The input shape is otherwise
unchanged (ADR 0028 d2/d3): no `evaluated_at` field is added to Rego's input,
because `time.now_ns()` already carries it.

## Tests

| Test                                                   | Tier        | Shows                                                                       |
| ------------------------------------------------------ | ----------- | --------------------------------------------------------------------------- |
| `TestEvalRegoQuery_TimeNowIsTheEvaluationInstant`      | unit        | a 2020-pinned record passes at a 2020 instant and fails two days later      |
| `TestEvalRegoQuery_ObservedAtIsUTC`                    | unit        | a UTC+5 `observed_at` reaches Rego ending in `Z`                            |
| `TestEvaluateFixture_RegoPinnedDatePassesIndefinitely` | unit        | a bundle-test case with pinned dates passes through `EvaluateFixture`       |
| `TestQueryInstant`                                     | unit        | live, earlier, equal and later as-of horizons                               |
| `TestSQLUsesWallClock`                                 | unit        | each wall-clock form matches; `evaluated_at` and look-alike names don't     |
| `TestStockSQLChecksNeverReadTheWallClock`              | unit        | no stock SQL query reads the wall clock                                     |
| `TestEvaluateFixture_SQLPinnedDatePassesIndefinitely`  | integration | the SQL `evaluated_at` column is the fixture's `now`, and windows follow it |
| `TestEvaluateControl_AsOfReplayUsesTheAsOfInstant`     | integration | SQL and Rego: a 5-day-old record fails live and passes as of 1 h after it   |

Each was run failing first: with `rego.EvalTime` removed and `queryInstant`
returning the wall clock, the Rego, fixture, `queryInstant` and replay tests
fail, and pass once restored.
