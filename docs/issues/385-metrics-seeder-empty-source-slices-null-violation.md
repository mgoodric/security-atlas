# 385 — metrics seeder: empty `source_slices` becomes SQL NULL, violates NOT NULL

**Cluster:** infra / catalog
**Estimate:** 0.5d
**Type:** AFK (mechanically verifiable — the fix is a nil-vs-empty-slice
normalization with a regression test)
**Status:** `ready`

## Narrative

Surfaced during slice 380, captured as follow-up per continuous-batch
policy. (Slice 380 is a frontend-only dashboard change and touches no
metrics/catalog/seed code; this bug is pre-existing on `main`.)

The metrics-catalog seeder
(`internal/catalog/metrics/seed.go`) builds the upsert params with:

```go
SourceSlices: append([]string(nil), m.SourceSlices...),
```

When a catalog metric declares `source_slices: []` (an empty list) —
e.g. `backup_restore_validation` in
`catalogs/metrics/exception-runway.yaml:27` (line 38 `source_slices:
[]`) — `append([]string(nil), <empty>...)` evaluates to a `nil` slice,
not an empty-but-non-nil slice. pgx/sqlc serializes a `nil` `[]string`
as SQL `NULL`, which violates the column constraint declared in
`migrations/sql/20260516000001_metrics_catalog.sql:122`:

```sql
source_slices TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
```

The insert fails with:

```
null value in column "source_slices" of relation "metrics_catalog"
... on backup_restore_validation
```

The column's `DEFAULT ARRAY[]` does NOT save it, because an explicit
`NULL` value in the INSERT/UPSERT overrides the column default (defaults
apply only when the column is omitted from the statement, not when it is
explicitly set to NULL).

### Why now

The metrics seeder runs during the e2e/docker-compose bring-up; the
NULL-violation logs as a seeder error during bootstrap. It was observed
during slice 380's CI Playwright bring-up. The dashboard e2e tests pass
regardless (the dashboard does not depend on the metrics catalog), but
the seeder error is noise that masks real bring-up failures and will
hard-fail any path that depends on a fully-seeded metrics catalog.

### Trigger

slice 380 CI Playwright bring-up log:
`metrics seeder ... null value in column "source_slices" of relation
"metrics_catalog"` on `backup_restore_validation`.

### Disposition

Code change to `internal/catalog/metrics/seed.go` (normalize a `nil`
`SourceSlices` to a non-nil empty slice before the upsert), or set
`source_slices: ["..."]` on the affected catalog entries. The seeder fix
is preferred — it is the robust, general fix (any future metric with an
empty `source_slices` is covered) rather than data-patching one YAML.

## Threat model

Internal seeder data path; no untrusted input; no auth/RLS surface
change. STRIDE: no new surface. The fix is a defensive normalization.

**Constitutional invariants honored**: no change to the evidence
ledger, UCF graph, scope, or tenancy. Pure catalog-seed correctness.

## Acceptance criteria

- [ ] **AC-1.** `internal/catalog/metrics/seed.go` normalizes a `nil`
      `SourceSlices` to a non-nil empty `[]string` before the upsert
      (or equivalently passes an empty `TEXT[]`, never SQL NULL).
- [ ] **AC-2.** A metric declaring `source_slices: []` seeds without a
      NOT-NULL violation (integration test against a real Postgres,
      never mocked — `internal/catalog/metrics/integration_test.go`).
- [ ] **AC-3.** The seeded row's `source_slices` reads back as an empty
      array (`{}`), not NULL.
- [ ] **AC-4.** Existing metrics with non-empty `source_slices` are
      unchanged (regression guard).
- [ ] **AC-5.** `go test ./internal/catalog/metrics/...` passes
      (unit + integration).

## Anti-criteria (P0)

- **P0-1.** Does NOT relax the migration's `NOT NULL` on
  `source_slices` — empty arrays are correct; NULL is not.
- **P0-2.** Does NOT data-patch only the one YAML and leave the seeder
  bug latent for the next empty-`source_slices` metric (fix the seeder).
- **P0-3.** Does NOT auto-merge.

## Dependencies

- **#380** (this is the slice it surfaced under) — does not block; the
  two are independent. Status `ready` (the metrics-catalog migration +
  seeder are long merged on `main`).
