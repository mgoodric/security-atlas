# #2935 — a1_1 capacity: Datadog as the source for memory, CPU p95 and disk (decisions log)

Parent: [#2826](https://github.com/mgoodric/security-atlas/issues/2826),
which made a1_1 a SQL check over `osquery.host_posture.v1` 1.3.0 and found
that Fleet has no memory or CPU use. Its decisions log has the Fleet API
table (`docs/audit-log/2826-osquery-capacity-antimalware-edr-sql-checks-decisions.md`
D1).

- detection_tier_actual: manual_review
- detection_tier_target: unit

The defect found was in the approved design, not in code. Splitting a1_1
into a disk-only osquery query plus a memory and CPU query would have let
the control pass with no memory or CPU reading (D4). It was found by reading
`internal/eval/engine.go` before writing the query. A bundle test in the
existing pattern with only an osquery record, the case now named
`both-osquery-disk-fine-without-datadog-is-inconclusive`, would have caught
it. That makes it a unit-tier catch: mutation (b) below shows the case fails
on exactly that design.

## Not verified live

No Datadog tenant was available. Every Datadog fact here comes from Datadog's
documentation, read 2026-11-22. The fixtures in
`connectors/datadog/internal/hostcapacity/testdata/` are **built to the
documented response shape, not recorded from a tenant**
(`testdata/README.md`, `gen_fixtures.py`). Matt accepted that on the ticket
thread, on condition that it is labelled in the tests, here and on the PR.
The first live run should capture the four responses and replace the
fixtures.

## D1: Datadog, chosen by Matt from three candidates

The ticket named three candidate sources. Matt chose Datadog, with this
check of the documentation done first:

| Need                | Datadog documentation                                                                                                                       | Source                                                                               |
| ------------------- | ------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------ |
| query API           | `GET /api/v1/query` with `from`, `to` (epoch s), `query`; `series[].scope`, `series[].pointlist` `[ms, value]`; requires `timeseries_query` | <https://docs.datadoghq.com/api/latest/metrics/query-timeseries-points.md>           |
| memory              | `system.mem.pct_usable`, gauge, "usable physical RAM as a fraction of the total"                                                            | <https://docs.datadoghq.com/integrations/system/>                                    |
| CPU                 | `system.cpu.idle`, gauge, percent                                                                                                           | same                                                                                 |
| collection interval | System metrics, Datadog Agent, 15 seconds                                                                                                   | <https://docs.datadoghq.com/extend/guide/data-collection-resolution.md>              |
| percentiles         | documented for distribution metrics only, not gauges                                                                                        | <https://docs.datadoghq.com/metrics/distributions/>                                  |
| rollup              | API default for the past 5 minutes is 2s; `.rollup(avg, N)` sets a larger interval; up to 1,500 points                                      | <https://docs.datadoghq.com/dashboards/functions/rollup/>                            |
| disk                | `system.disk.used`, `system.disk.total`, bytes; Disk check on by default; `system.disk.*` tagged `device`                                   | <https://docs.datadoghq.com/integrations/disk/>, System check "Tags"                 |
| host identity       | Hosts API has `host_name`, `aliases`, an int `id`; no host or system UUID                                                                   | <https://docs.datadoghq.com/api/latest/hosts/get-all-hosts-for-your-organization.md> |

Rejected:

- **Fleet scheduled queries.** osquery has no p95 table, so this would mean
  sampling across connector runs. Nothing documented gives CPU over time.
- **The adopter's own `atlas-cli evidence push`.** This names no producer,
  which fails the first acceptance criterion.

## D2: a new routed kind, `monitoring.host_capacity.v1`, not osquery 1.4.0

Three reasons:

- a1_1's osquery query keys hosts by `host_uuid`, and Datadog has none.
- A Datadog record under the osquery kind would become the host's latest
  record and shadow the Fleet record that carries disk use.
- Datadog data under an `osquery.` name misleads the reader.

The kind is vendor-neutral (`source_vendor` enum `datadog`, room for more),
like `monitoring.alert_firing.v1`. It is routed by type (#2924), so
`--capacity-control` defaults to empty.

`host_role` is an enum of one value, `production`. The connector reads only
the operator's production scope, so it has nothing else to say. Widening
the enum later is additive.

## D3: how each reading is made

- **CPU p95.** Datadog has no percentile on a gauge, so the connector takes
  it. The query is `avg:system.cpu.idle{scope} by {host}.rollup(avg, 15)`
  over [now-5m, now], about 20 points per host.
  - The percentile is nearest-rank: the smallest value with at least 95% of
    the samples at or below it. With 20 points that is the 19th, so a single
    spike does not set it, and two do. This matches how p95 is usually read
    for latency; interpolating would invent a value no sample had.
  - Fewer than 10 points (`MinCPUSamples`, half a full window) leaves the
    reading out. `cpu_sample_count` is emitted either way, so an operator
    can see why.
  - An idle value outside 0..100 is dropped as a point, not clamped.
- **Memory.** (1 - `pct_usable`) \* 100 at the latest non-null point. A value
  outside 0..1 leaves the reading out. The latest point is used rather than
  a p95 because the bound is on memory in use, not on load.
- **Disk.** `system.disk.in_use` is avoided: its docs say both "as a percent
  of the total" and "Shown as fraction", and guessing the scale would make
  a wrong reading look right. Instead the connector divides the two byte
  metrics, grouped `by {host,device}`, and takes the fullest device.
  - If any device of a host has used bytes but no usable total (missing,
    zero, or below used), the host's disk reading is left out, because the
    device left out could be the full one.
  - The reading is the fullest device, not the root volume as Fleet's is.
    This is stricter, and an over-full data volume is a capacity problem
    too.
- **Rounding.** All three readings are rounded to two decimals. A reading
  such as 89.996 becomes 90 and fails, which matches what an operator sees.

## D4: a second query that checks all three readings, osquery query unchanged

**What Matt approved first:** the osquery query keeps disk only, and a new
query checks memory and CPU.

**Why that was wrong:** `internal/eval/engine.go` (`evalQueries`) rolls a
control's queries up as "any fail → fail; else any pass → pass; else
inconclusive". Under that design, with disk fine and no Datadog record, the
disk-only query passes, the other is inconclusive, and the control passes.
#2826 set the rule that a missing reading is never a pass.

**What shipped, approved on the ticket thread:**

- The Datadog record also carries `disk_used_percent`.
- The new query `production_capacity_healthy_monitoring` checks all three
  readings, at the same bounds (85 / 90 / 90, strict) and with the same
  three-valued per-host logic.
- The osquery query is byte-for-byte unchanged.
- A pass therefore needs a query that saw every reading for each production
  host it sees. An over-bound reading from either source fails the control.

**What remains, and is documented in the description:** the two sources are
not joined. A production host in Fleet that Datadog does not see is
unchecked when the Datadog query passes. This is the same gap as a host
that never reported (#2834), and an endpoint inventory is what closes both.

The new query is the old one with `host_uuid` replaced by `host_name` and
`last_seen_at` by `window_end`. The rank, tie and non-number handling are
kept on purpose, so the two queries read the same way.

## D5: opt-in surface, redirect guard, keys from the environment

- **Opt-in.** The capacity pass runs only when `--capacity-production-scope`
  is set. `timeseries_query` is a new permission, and making it mandatory
  would break every existing run whose key lacks it.
  - `datadogauth.RequiredCapacityPermission` is kept out of
    `RequiredScopes()`, whose test holds every entry to a `_read` suffix.
  - `atlas-datadog permissions` lists it with its condition.
- **Scope validation.** The scope is validated as comma-separated tag
  characters before any request. It is spliced into the metric query, so a
  value with `}` or `by {` could otherwise change the query's shape.
- **Redirect guard.** As #2947 did for Confluence, the connector's
  `http.Client` now refuses a redirect that changes host, port or scheme
  (`datadogauth.CheckRedirect`). It applies to all five surfaces, since
  every request carries both keys.
- **Keys.** They are still read from `DATADOG_API_KEY` / `DATADOG_APP_KEY`
  only. `TestCapacityFlagsDefault` fails if a key flag appears.

## D6: supersedes a1_1

The description and the new query change the manifest hash.
`stock_manifests_gen.go` gains one line (`9b5bed40…`), and the generator
dropped nothing. The PR carries `supersedes-stock`. On the next boot the old
version is superseded; owners, exceptions and risk links stay on it.

## Mutation checks

Each check was run against the scratch Postgres, with
`go test -tags=integration ./internal/control/ -run TestStockSQLBundleTests_PassAgainstPostgres`:

| Mutation                                                                                       | Failing case(s)                                                                                                                            |
| ---------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------ |
| (a) the new query's memory bound made inclusive (`<= 90`)                                      | `datadog-memory-at-90-fails`                                                                                                               |
| (b) the osquery query reads missing memory and CPU as ok (the first-approved disk-only design) | `fleet-shape-under-is-inconclusive`, `tie-with-under-and-unread-is-inconclusive`, `both-osquery-disk-fine-without-datadog-is-inconclusive` |
| (c) p95 rank changed from ceil to floor                                                        | `TestP95_NearestRank` (unit)                                                                                                               |

## Not changed

- The osquery connector and `osquery.host_posture.v1`.
- ADR 0028's check table.
- a1_1's title, anchor, freshness class and bounds.
