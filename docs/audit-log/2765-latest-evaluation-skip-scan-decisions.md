# #2765 — latest evaluation per control by index skip scan: decisions log

detection_tier_actual: manual_review
detection_tier_target: integration

Follow-up from #2698 (PR #2758, D4). `ListActiveControlsWithState` found each control's latest evaluation per scope cell with a `DISTINCT ON` over every `control_evaluations` row in the tenant. The `/controls` page and needs-attention both call it.

## D1 — Skip scan over the existing index, not a maintained latest table

The ticket offered two options: an index-driven `LATERAL … LIMIT 1` per control, or a `control_latest_evaluation` table maintained on insert. The index option was chosen.

- `idx_control_evaluations_latest (tenant_id, control_id, scope_cell_id, evaluated_at DESC)` (slice 012) already has the right shape. No migration is needed.
- A maintained table is a second copy of derived state next to an append-only ledger (invariant #2). It would need a trigger with its own RLS write policies, a backfill, and handling for rows removed by the owner role (AC-7 replay deletes the ledger and re-runs the engine, and `TRUNCATE` fires no row trigger). Any gap in that handling would show stale state on the page, and nothing would flag it.

Postgres 16 has no native skip scan, so the per-control cells are found by a recursive CTE (`eval_cells`). It seeds the first non-NULL cell of each active control, then steps to the next cell above it, `LIMIT 1` each time. The latest row for each (control, cell) is then one `ORDER BY evaluated_at DESC, created_at DESC LIMIT 1` probe. Cost scales with active controls × cells per control, not ledger rows. Confidence: HIGH.

## D2 — The NULL whole-tenant cell is its own branch

`scope_cell_id = x` can't match NULL, and `IS NOT DISTINCT FROM` is not indexable, so the NULL cell gets one probe per active control in a separate `UNION ALL` branch. That probe's `ORDER BY` leads with `scope_cell_id`. `IS NULL` does not make the column a pathkey constant the way `=` does, so without the lead column the planner read every NULL-cell row of the control and top-N sorted them. On the seeded tenant that branch alone took 14 ms of an 18.7 ms run. With the lead column it reads the index in order and the run takes 4.6 ms.

## D3 — Only active controls are probed

The old CTE also read superseded controls' history, and the outer query then dropped it. The new seed starts from `controls WHERE superseded_by IS NULL`, so that history is never read. The output is identical. A superseded control was never in the result.

## D4 — `CountActiveControls` gets the same CTE

It repeats the list's rollup and is called on every page load. Unfiltered, the planner already removed its join to the rollup (0.06 ms before and after). With a state or freshness filter it is the worst case: the planner re-ran the `DISTINCT ON` CTE inside a nested loop (`loops=360`).

## D5 — Measurements

One tenant: 160 controls (40 superseded), 20 scope cells, each control on 5 cells plus the NULL cell, 220 runs each. That is 211,200 `control_evaluations` rows. `EXPLAIN (ANALYZE, BUFFERS)` as `atlas_app` under the tenant GUC, warm cache, Postgres 16, default sort, limit 50:

| Query                | Before                                                   | After                       |
| -------------------- | -------------------------------------------------------- | --------------------------- |
| List, unfiltered     | 96.3 ms, parallel seq scan 211,200 rows, temp read 2,466 | 4.9 ms, index only, no temp |
| List, `result=fail`  | 100.7 ms, same seq scan and temp spill                   | 6.8 ms                      |
| Count, unfiltered    | 0.06 ms (join removed)                                   | 0.07 ms                     |
| Count, `result=fail` | 10,440 ms, seq scan re-run 360 times, temp read 282,203  | 8.5 ms                      |

## D6 — Equivalence test

`TestListWithState2765_MatchesDistinctOnOracle` (`internal/api/controls/list_state_latest_2765_integration_test.go`) holds the replaced `DISTINCT ON` CTE and rollup verbatim as an oracle. The fixture has several cells plus the NULL cell, `evaluated_at` ties broken by `created_at` in both a real cell and the NULL cell, a NULL-only control, a never-evaluated control, a superseded control with evaluations, and a deleted one. Across ten filter combinations (scope cell, result, freshness), every row and every total `ListWithState` returns must equal the oracle.

I checked that the test fails when the query is wrong. Disabling the recursive step (only the first cell found per control) failed 12 assertions. Flipping the `created_at` tiebreak failed both tie checks. Both edits were reverted.

## D7 — Not folded in: the #2758 review items

The rate limit and logger items refer to the needs-attention large-read WARN, which PR #2758 adds. That PR was still open when this work started, so neither item has code on `main` to change. They should land with #2758 or after it merges.
