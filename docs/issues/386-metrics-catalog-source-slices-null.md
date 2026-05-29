# 386 — metrics_catalog seed aborts on empty source_slices (NULL violation)

**Cluster:** Backend / Metrics
**Estimate:** 0.5d
**Type:** JUDGMENT (hotfix)
**Status:** `ready`

## Narrative

Surfaced while diagnosing the slice 385 demo-seed issue on `atlas-edge`
2026-05-29. The atlas startup metrics-catalog seed logs:

```
atlas: metrics seeder: catalog/metrics: upsert backup_restore_validation:
  ERROR: null value in column "source_slices" of relation "metrics_catalog"
  violates not-null constraint (SQLSTATE 23502)
```

`metrics_catalog.source_slices` is `TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[]`
(migration `20260516000001`). `internal/catalog/metrics/seed.go` built the
upsert param with `append([]string(nil), m.SourceSlices...)`, which returns a
**nil** slice for empty input. pgx encodes a nil `[]string` as SQL `NULL` (the
column DEFAULT only applies when the column is omitted from the INSERT, not when
NULL is passed explicitly), so any metric declared with `source_slices: []`
violates the NOT NULL constraint.

Three catalog metrics declare `source_slices: []` —
`backup_restore_validation`, `incident_response_drill_outcome`,
`tabletop_exercise_outcome` (`catalogs/metrics/exception-runway.yaml`). The seed
runs in a single transaction, so the first such metric aborts the **whole**
catalog seed → `metrics_catalog` ends up **empty**. That cascades: the metrics
scheduler then hits `metric_observations_metric_id_fkey` violations (observations
reference catalog ids that were never inserted) and aborts its sweep.

### Why CI never caught it

The startup seed failure is logged, not fatal (boot continues), so the self-host
e2e doesn't fail on it. The `metrics` integration test that exercises
`SeedFromEmbedded` `t.Skip`s when the migrator-role `DATABASE_URL` is unset, so
the seed path wasn't gated.

## What ships

1. `nonNilStrings(s)` helper guarantees a non-nil copy (empty/nil → non-nil
   empty `[]string{}`; populated → independent copy).
2. `Apply` uses it for `SourceSlices`.
3. Unit regression test `TestNonNilStrings` (runs in the no-DB unit job).

## Acceptance criteria

- AC-1: a metric with empty `source_slices` seeds as `{}` (not NULL), no error.
- AC-2: the full embedded catalog seeds without the NOT NULL abort.
- AC-3: populated `source_slices` are preserved and copied (no aliasing).

## Out of scope (follow-up)

- Metrics evaluator query drift: evaluators query `framework_id` and
  `policy_versions`, but the schema has `framework_version_id` and `policies`.
  Separate slice.
- Making the startup metrics-seed failure fatal (or surfacing it as a healthcheck)
  so a future seed regression is caught — separate hardening decision.
