# #2794 — evidence filters follow-ups from #2771: decisions log

detection_tier_actual: manual_review
detection_tier_target: playwright

Four follow-ups from #2771 (PR #2790).

## D1 — Control pill: a second anchored control proves narrowing

`fixtures/e2e/evidence-list.sql` adds control `...2755c000a003` behind a second anchor (`0E2E2771-2`) with one record (`...07`). AC-3 now picks `...a001` by value and expects 6 of 7 rows. The new record is in dev, pushed by `test-connector-b`, 45 days old, so the Scope (3), Source (4) and Last-7-days (4) counts do not move. Being older than 30 days, it is also the row that shows "All time" really is all time (D4).

## D2 — Scope GIN index: not added; GIN cannot help this query shape

Measured on a scratch cluster (`scripts/scratch-pg.sh`, Postgres 16, laptop). The data was 2M `evidence_records` for one tenant: 1M in the last 30 days, 1M over the prior 335 days, about 600 B payload each. 10% had no scope, 0.1% sat in a "rare" cell and the rest were spread over 12 cells. Each case ran `ListEvidencePaged`, the verbatim sqlc text, as a prepared statement 40 times. pgx caches prepared statements, so from the sixth run on Postgres uses the generic plan, as production does.

| Case (main schema, no new index) | p50 ms | p95 ms |
| -------------------------------- | ------ | ------ |
| 30d, no Scope filter             | 1458   | 1602   |
| 30d, common cell (~8%)           | 1587   | 1697   |
| 30d, empty cell                  | 1519   | 1655   |
| all time, no Scope filter        | 2461   | 2660   |
| all time, common cell            | 2735   | 2947   |
| all time, rare cell (0.1%)       | 2673   | 2818   |
| all time, empty cell             | 2679   | 2979   |

The Scope filter adds about 5–10% to a read that is already slow without it. With the generic plan, the read uses `idx_evidence_tenant_control_observed`, fetches every row in the window (1M) and does a top-N sort. The `@>` is one more filter on rows already fetched. No index keeps the ledger in `(tenant_id, observed_at)` order.

With `gin (scope_canonical jsonb_path_ops)` added, the generic plan does not use it: the rare-cell EXPLAIN is still an index scan on `idx_evidence_tenant_control_observed`, 1515 ms. The predicate is `($8 IS NULL OR scope_canonical @> …)`, and an OR on a parameter null-check cannot drive an index. The full 40-run timing with GIN was stopped after its first case (p95 3150 ms, run alongside other load). The plan already answered the question.

The index that changes the picture is a btree on `(tenant_id, observed_at DESC, id DESC)`. These are single-run EXPLAIN ANALYZE timings, generic plan:

| Case (with the ordering btree) | ms   |
| ------------------------------ | ---- |
| all time, no Scope filter      | 0.03 |
| all time, common cell          | 0.28 |
| all time, rare cell            | 2481 |
| all time, empty cell           | 5195 |
| 30d, empty cell                | 2453 |

It turns the default read and common cells into a 51-row index walk. A selective or empty cell walks the whole window in index order instead, and gets slower. A complete fix is the ordering btree for the unfiltered path, plus a Scope query written without the `IS NULL OR` (its own statement, or a dynamic predicate) so a GIN index can serve selective cells.

This PR adds neither index. Each is a write-locking build on the append-only ledger: Atlas migrations run in a transaction, so no `CONCURRENTLY`. And the GIN half needs a query change before it does anything. That follow-up is #2811. Building the GIN index out of band now, as the ticket proposed, would cost write amplification on every push for no read benefit.

## D3 — OpenAPI: no change

`GET /v1/evidence` in `docs/openapi.yaml` documents only the generic `Ack` response envelope, with no row schema. The ticket's condition ("if it has a response schema") does not hold, so there is nothing to extend with `source_attribution`.

## D4 — "All time" sends an explicit lower bound; "Last 30 days" is the default

`toFetchOptions` sends `since=1970-01-01T00:00:00Z` (`ALL_TIME_SINCE` in `web/app/(authed)/evidence/filters.ts`) when the Since pill is "All time". Before, it sent no `since`, so the handler's 30-day `defaultWindow` applied under an "All time" label. The fix is in the page, not the handler: the handler's 30-day default also serves the per-control evidence tab and other callers, and changing it would widen their reads too.

The pill's default is now "Last 30 days" (`DEFAULT_FILTERS.since = "30d"`), and "All time" is an explicit choice, kept in the URL as `since_preset=all`. Leaving "All time" as the default would have moved every ordinary `/evidence` load from a 30-day read to a whole-ledger read: per D2, p95 about 1.6 s to about 2.7 s at 2M rows. The 30-day default reads the same window the page always loaded, under a label that says so. Chosen by the dispatcher on review of PR #2808. The page's URL writer now omits a filter when it equals `DEFAULT_FILTERS[key]` rather than a per-key sentinel, so "Last 30 days" leaves the URL clean and "All time" is written out. The e2e AC-1 checks 6 rows on the default load and 7 after picking "All time".

An explicit "All time" read still covers the whole ledger and pays that cost until the ordering index lands. That index, and the Scope query change from D2, are #2811 (on hold: a write-locking build on the ledger, so Matt decides when it runs). Confidence: MEDIUM. This is one laptop, one tenant, synthetic data.
