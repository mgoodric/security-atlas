# #2789 — activity-actors module filter matches route keys, not raw paths: decisions log

detection_tier_actual: manual_review
detection_tier_target: integration

Follow-up from #2718 (PR #2788). `ListUnifiedAuditMachineActors` applies the #2173 module filter in SQL. Its `matches` CTE paired every distinct decision path in the window with every `featureflag.Modules` route pattern (77 today), and ran a `generate_subscripts` subquery for each pair. Paths carry IDs, so the distinct-path count grows with traffic.

## D1 — Measured first, and it was slow

Harness: a throwaway `//go:build explain2789` test in `internal/api/adminauditlog` (not committed). It seeds one tenant through the owner role with `COPY` and runs `EXPLAIN (ANALYZE, BUFFERS)` on the generated query text as `atlas_app` under the tenant GUC. It builds the arguments with the handler's own `moduleRouteFilter`, using a zero `ModuleSet` (every module seen).

Dataset: 100,000 `decision_audit_log` rows spread uniformly over 90 days. 40% are by 20 machine actors (`system`, three `system:<process>`, 16 `oauth_client:<uuid>`) and 60% by person UUIDs. 60% are `read`. Each path is a random registry pattern with every `*` replaced by a fresh UUID. A quarter of them gain a trailing `/<uuid>` and a quarter a `:bulk` suffix. Postgres 16.15 on a local scratch cluster (`scripts/scratch-pg.sh`), warm cache.

In the 90-day window with reads included, the `resolved` CTE finished at 3,415 ms of a 3,570 ms run. Its nested loop is 12,424 paths × 77 patterns with a subquery per pair. That is far over the ticket's 100 ms bar.

## D2 — Normalise each path to a route key, then match only distinct keys

The ticket offered two options. Option (a) normalises paths before matching. Option (b) precomputes a per-row route key. I chose (a).

- (b) needs a migration on an append-only ledger, a backfill, and a write path that knows the registry. Adding a route to the registry would then make every stored key stale, so the key could not be stored.
- (a) is query-local and has nothing to keep in sync.

The new `keyed` CTE replaces a segment with `*` when it equals no registry literal and does not start with `<literal>:`. The key resolves exactly as the path does:

- For a pattern's `*`, the test is `s[i] <> ''`. A replaced segment is non-empty (`''` is a literal, since every pattern starts with `/`), and so is `*`.
- For a pattern literal `p`, the test is `s[i] = p OR starts_with(s[i], p || ':')`. A replaced segment fails it by construction. `*` also fails it: it is never a literal, and it contains no `:`.
- Segment count is unchanged, so `cardinality(s) >= cardinality(segs)` still holds.

The literal set is computed from the `route_patterns` argument, so it follows the registry with no second list. The `:` check runs the `EXISTS` only on segments that contain `:`, so a UUID segment costs one `= ANY` scan. Confidence: HIGH.

## D3 — Measurements

Same dataset and harness. Execution time is from `EXPLAIN (ANALYZE, BUFFERS)`, planning time (1 to 6 ms) is excluded. "Paths" counts distinct machine-actor decision paths in the window.

| Window | Reads    | Paths  | Before   | After  |
| ------ | -------- | ------ | -------- | ------ |
| 7 d    | excluded | 548    | 161 ms   | 29 ms  |
| 7 d    | included | 1,104  | 319 ms   | 43 ms  |
| 90 d   | excluded | 5,069  | 1,470 ms | 113 ms |
| 90 d   | included | 12,408 | 3,721 ms | 243 ms |

After the change, the 12,424 paths collapse to 247 keys, and `matches` plus `resolved_keys` take about 18 ms. Most of the remaining 243 ms is outside the module filter:

- about 73 ms is the existing `tuples` `DISTINCT` over 40k machine-actor rows;
- about 47 ms is building `keyed`;
- the rest is the final joins and the `DISTINCT` on actor.

Reads are excluded by default (`exclude_read_telemetry`). The 90-day cases are the widest window, and this seed is pessimistic: 40% machine actors, and every ID unique.

## D4 — Not done: the rest of the 90-day cost

The 90-day, reads-included case is still over 100 ms (243 ms), and the module filter is no longer the main cost. Going lower would need narrowing the `tuples` scan, for example a partial index on machine-actor decisions or a per-row route key. Both are schema changes with the costs listed in D2. They are not justified by this ticket. I'd file one if a real tenant's numbers show it.

## D5 — Equivalence

`TestActivityActors_ModuleFilterMatchesGo` passes. Its cases replace `*` with `x2718` and append `/r-2718` and `:bulk`, so they go through the collapse and the verb branch. I checked that it fails when the normalisation is wrong:

- Collapsing every segment to `*` failed the test: 365 actors listed, 285 expected.
- Dropping the `<literal>:<verb>` branch also failed it.

Both edits were reverted by regenerating with sqlc v1.31.1.
