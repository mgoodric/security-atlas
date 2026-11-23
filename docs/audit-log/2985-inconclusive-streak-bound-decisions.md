# #2985: Bound the inconclusive-streak read; read-time inconclusive (decisions log)

These are the follow-ups to #2915's D2. `ListInconclusiveStreaks` no longer
reads each control's whole evaluation history. This log does not block
merge.

- detection_tier_actual: manual_review (the cost, in #2982's review)
- detection_tier_target: integration

## D1: A partial index and index probes, not a summary column or a lookback

The ticket offered two bounds: a lookback window, or an `inconclusive_since`
column maintained on evaluation write. Neither was used.

- A lookback gives the wrong date for a run older than the window. The
  state endpoint shows the exact start, so it would need a fallback scan
  anyway.
- A summary column has to be computed from the previous row when a row is
  inserted. A row written out of `evaluated_at` order (the demo seeder,
  any backfill) would leave the later rows' values wrong. It would also
  need a backfill `UPDATE` on a ledger that is otherwise append-only.

The query now does two probes per cell, and the result is the same as
before:

1. The last non-inconclusive row at or before `as_of`: one probe on the new
   partial index `idx_control_evaluations_not_inconclusive`, which holds
   only non-inconclusive rows.
2. The first row after that one, at or before `as_of`: one probe on
   `idx_control_evaluations_latest`.

The cells come from the same skip scan `ListActiveControlsWithState` uses
(#2765). The NULL whole-tenant cell is its own branch. The index is the
only schema change. Nothing is written to the ledger, and the as-of bound
works as it did, because both probes are bounded by `as_of`.

Benchmark on a scratch Postgres 16, after `VACUUM ANALYZE`: one tenant, 200
controls, each with the NULL cell and two cells, hourly rows for 365 days
(5,256,000 rows), passing for the first 30 days and inconclusive after
that. All 200 controls passed in.

| Query                   | Execution time | Rows |
| ----------------------- | -------------- | ---- |
| #2915 window, full scan | 2587 ms        | 600  |
| #2985 probes            | 9.3 ms         | 600  |

The two result sets are identical. The integration test
`TestListInconclusiveStreaks_2985_MatchesFullScan` checks the same thing on
a random ledger (ties on `evaluated_at`, cells never broken, cells that
recovered, the NULL cell) at six as-of times. It fails when the
streak-start probe uses `>=` instead of `>` (67 mismatches).

## D2: There is no read-time inconclusive state, so no gap to close

The ticket asked whether a control can be shown inconclusive by something
worked out at read time, such as freshness expiry, while its latest ledger
row is not inconclusive. It can't. Every place that shows a control as
inconclusive reads the result stored on the latest ledger row:

- `ListActiveControlsWithState` (the controls list and needs-attention)
  rolls up `ce.result` from each cell's latest row.
- `GET /v1/controls/{id}/state` (`eval.ControlState`) returns `r.Result`
  from `ListLatestControlEvaluations` unchanged (`stateFromRow`).

Freshness expiry is applied when evaluating, not when reading. The hourly
scheduled recompute (`eval.DefaultRecomputeInterval`) writes a new row when
evidence ages out, so the ledger records the inconclusive result. A
control the band would leave out therefore has no inconclusive state to
show. If a read-time derivation is ever added, it has to feed this query
too, or the band will miss those controls.
