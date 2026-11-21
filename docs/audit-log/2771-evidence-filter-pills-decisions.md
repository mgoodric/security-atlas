# #2771 — evidence Control, Scope and Source pills: decisions log

detection_tier_actual: playwright
detection_tier_target: integration

Found while restoring `web/e2e/evidence-list.spec.ts` (#2755). Each pill narrowed a real ledger to zero rows.

## D1 — Scope: match the record's pushed scope, not the control's applicability

Nothing mapped a record to a scope cell. The filter compared `evidence_records.scope_id` (FK to `scopes`, NULL on every push) with a `scope_cells` id. The eval engine notes per-cell evidence partitioning as a follow-up (`internal/eval/engine.go`, `computeRow`).

Three readings were weighed, and the dispatcher chose A:

- **A (chosen):** record R is in cell C when, for every `(k, v)` in `C.dimensions`, R's `scope_canonical` has key `k` with `v` among its values. Extra keys on R don't matter. This is the cartesian-cell reading `scope.MatchRecordScope` applies to credential predicates, and it is true of the record itself: the record was observed in that cell.
- B: evidence of controls that have an evaluation recorded for C (the slice-224 anchors filter). Rejected because it over-includes: prod-only evidence would show under a dev cell, which an auditor would misread.
- C: leave the pill as a `fixme`. Rejected because the ticket's acceptance criterion is the pill working.

A cell with no dimensions covers every record that has a captured scope. An unknown or other-tenant cell id matches nothing, never the whole ledger (`TestEvidence_ScopeCellOutOfTenantMatchesNothing`).

## D2 — Records with no captured scope drop out, and the page says so

Rows pushed before slice 474 have `scope_canonical` NULL (or JSON `null` when the push carried no scope). They match no cell. While the Scope pill is set, a hint under the filter row (`evidence-scope-hint`, text `SCOPE_FILTER_HINT` in `filters.ts`) says those records are not matched. The e2e spec asserts the text. Backfilling the scope isn't possible: the ledger is append-only (invariant #2) and the pre-474 pushes never stored it.

## D3 — Cost of the jsonb match

The cell's dimensions become one `[{key, values:[v]}, …]` pattern in an uncorrelated scalar subquery. Postgres runs it once as an InitPlan, and the row predicate is `er.scope_canonical @> $1`. `EXPLAIN` on a scratch cluster:

```
InitPlan 2 (returns $1)
  ->  Index Scan using scope_cells_tenant_id_unique on scope_cells sc
...
Filter: ((scope_canonical @> $1) AND (tenant_id = ...) AND (observed_at <= now()) AND (observed_at >= ...))
```

That form can use a GIN index. No index on `scope_canonical` exists, and this change does not add one. Today the `@>` is a per-row filter over the rows that the `tenant_id` + `[since, until]` window already selects (30 days by default), stopping at `LIMIT pageRows+1` in `observed_at` order. The cost grows with the tenant's evidence in the window, not with the whole table. Adding `CREATE INDEX … USING gin (scope_canonical jsonb_path_ops)` on a large append-only table is a migration. Atlas migrations run in a transaction, so it can't be built `CONCURRENTLY` and would lock writes to the ledger while it builds. If Scope-filtered reads get slow, that index is the follow-up, built out of band. Confidence: MEDIUM. No production-volume measurement was made.

## D4 — Source: a new wire field, `source` unchanged

The list rows filled `source` from `provenance`, which has no actor fields. The evidence detail wire already carries both `source` (provenance) and `source_attribution`, so the list wire gains `source_attribution` and the Source column and pill read it. Replacing `source` would have changed a field other readers may depend on. Both contract goldens were regenerated.

## D5 — Control: the tenant control behind the anchor

The pill now uses `AnchorWithState.control_id` and leaves out anchors without one. It fetches `has_control=true` at the 500-row page cap. Before, it took the first 100 catalog anchors, so a tenant's anchored controls could be missing from the list. When several controls share an anchor, the platform returns the one with the lowest `bundle_id` (`ListSCFAnchorsLatestWithState`), and the pill offers only that one. A Control pill listing tenant controls directly would need a `GET /v1/controls` list, which doesn't exist.

## Not changed

The Since pill's "All time" still sends no `since`, so the handler's 30-day default window (`pagination.go` `defaultWindow`) applies. The ticket notes this from source only. It is outside the acceptance criterion.
