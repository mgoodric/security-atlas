# Slice 742 — dbtest drain decisions log

JUDGMENT slice. Per-batch build-time calls recorded here; maintainer iterates
post-merge.

## Batch 10 — `internal/api/anchors` + `internal/api/ucfcoverage`

Files migrated (4 `_test.go`, 2 packages):

- `internal/api/anchors/integration_test.go` — call sites in `setupHTTPServer`
  migrated to `dbtest.NewMigratePool`/`NewAppPool` (dropped redundant
  `adminPool.Close()`/`appPool.Close()`).
- `internal/api/anchors/export_integration_test.go` — `appDSN174`/`adminDSN174`/
  `openPool174`/`getEnv` deleted; router/seed/count → `NewAppPool`/`NewMigratePool`.
- `internal/api/ucfcoverage/integration_test.go` — `appDSN`/`adminDSN`/`openPool`
  deleted; admin sites → `NewMigratePool`, app site → `NewAppPool`.
- `internal/api/ucfcoverage/benchmark_test.go` — **untouched** (see below).

Judgment calls:

1. **`benchmark_test.go` left as-is (hard constraint, not preference).** It is
   `*testing.B`; the slice-435 harness is `*testing.T`-only and cannot accept a
   `*testing.B`. Its `adminDSN_b`/`appDSN_b`/`openPool_b`/`bGetenv` helpers stay.
   A `testing.TB` overload of the harness would be a separate slice.
2. **`anchors/integration_test.go` helpers retained (deferral, batch-2 lesson).**
   `appDSN`/`adminDSN`/`openPoolDSN` are shared with the same-package sibling
   `state_integration_test.go`, which is OUT of batch-10 scope. Deleting them
   would break the package's compile, so the definitions stay and only the
   in-file call sites are migrated. The helpers retire when `state_*` is drained
   in a future batch.
3. **`freshAnchorsTenant` kept inline (sanctioned carve-out).** Returns a
   `uuid.UUID` and cleans only `action='anchors_export'` rows — neither shape
   `dbtest.SeedTenant` (string tenant, plain `WHERE tenant_id=$1`) expresses.
   Only its pool re-routed to `NewMigratePool`; the migrate pool is opened before
   the cleanup closure registers, so LIFO ordering keeps it open during the DELETE.
4. **`ucfcoverage` wipes (`wipeTenantControls`/`wipeTenantState`) stayed as
   `TRUNCATE … CASCADE`** through the migrate pool — global, not per-tenant, so
   not a `SeedTenant` candidate; pool plumbing only.

Verification: `gofmt -l` clean, `goimports -w` applied, `go vet -tags=integration`

- `go build -tags=integration` clean for both packages; suites SKIP cleanly with
  no DB env. Live integration run deferred to CI (no local Postgres this session —
  batch-9 precedent). Shard enrolment unchanged.

detection_tier_actual: none
detection_tier_target: none

## Batch 11

Files migrated (4 files, 4 distinct packages, each fully owns its helpers —
`appDSN`/`adminDSN`/`openPool` deleted in all four):

1. `internal/api/csfassessment/integration_test.go`
2. `internal/api/dashboardexport/integration_test.go`
3. `internal/api/metrics/integration_test.go`
4. `internal/api/policies/ack_rate_integration_test.go`

Pool routing: every RLS-bound assertion / handler-under-test pool →
`dbtest.NewAppPool`; every catalog-seed / cross-tenant cleanup pool →
`dbtest.NewMigratePool`. No assertion defaulted to the privileged pool.

freshTenant judgments:

1. **csfassessment — migrated to `dbtest.SeedTenant`.** Returns `string`,
   pure-DELETE cleanup on four tables scoped by `tenant_id`. The unused `fvID`
   parameter was dropped from the thin wrapper; call sites updated.
2. **dashboardexport `freshExportTenant` — carve-out (kept inline).** Returns
   `uuid.UUID` (not the string `SeedTenant` yields) AND one cleanup row is
   scoped to `action='dashboard_export'` on `me_audit_log` — neither shape
   `SeedTenant` expresses. Only its pool re-routed to `NewMigratePool`; the
   migrate pool is acquired before the closure so dbtest registers its own
   `t.Cleanup` ahead of teardown (anchors `freshAnchorsTenant` precedent).
3. **metrics — carve-out (kept inline).** Returns `uuid.UUID`; pool re-routed
   to `NewMigratePool` only.
4. **policies — migrated to `dbtest.SeedTenant`.** Returns `string`, pure
   tenant-scoped DELETE. The original two-phase `policies` delete
   (`predecessor_id IS NOT NULL` first, then unconstrained) was defensive only:
   the policies self-FK is `ON DELETE SET NULL` (migration 20260511000016), so a
   single tenant-scoped `policies` DELETE removes the whole version chain with no
   constraint violation. Collapsed to one `policies` entry — behavior identical.
   `setup()`'s manual `app.Close()`/`admin.Close()` dropped (dbtest self-closes);
   `ts.Close()` kept.

Verification: `goimports -w` applied; `gofmt -l` clean; `go vet -tags=integration`

- `go build -tags=integration` clean for all four packages; all four suites SKIP
  cleanly with no DB env (`go test -tags=integration -p 1` → 4× `ok`). Live
  integration run deferred to CI (no local Postgres this session — batch-9/10
  precedent). No production (`!_test.go`) code touched; shard enrolment unchanged.

detection_tier_actual: none
detection_tier_target: none

## Batch 12

Files migrated (3 files, 3 distinct packages — each fully owns its helpers):

1. `internal/api/adminvendors/export_integration_test.go`
2. `internal/api/me/profile_integration_test.go`
3. `internal/api/scfimport/import_test.go`

Pool routing: every RLS-bound assertion / handler-under-test pool →
`dbtest.NewAppPool`; every catalog-wipe / cross-tenant seed / cleanup pool →
`dbtest.NewMigratePool`. No assertion defaulted to the privileged pool.

freshTenant / harness judgments:

1. **adminvendors `freshTenant` — migrated to `dbtest.SeedTenant`.** Returns
   `string`, pure-DELETE cleanup on three tables (`vendor_scope_cells`,
   `vendors`, `me_audit_log`) scoped by `tenant_id`. `appDSN`/`adminDSN`/
   `openPool` deleted. `setupHTTPServer`'s `openPool(t, appDSN(t))` →
   `dbtest.NewAppPool(t)` and its in-closure `app.Close()` dropped (dbtest
   self-closes); `ts.Close()` kept. The four test-body `admin := openPool(...)`
   - `defer admin.Close()` pairs collapsed to `admin := dbtest.NewMigratePool(t)`.
2. **me — `seedTenantAndUser` kept inline (carve-out).** It is NOT a pure-DELETE
   freshTenant: it INSERTs a `users` row and returns a `(tenantID, userID)`
   pair — neither shape `SeedTenant` expresses. Only its pools re-routed:
   `appDSN`/`adminDSN`/`openPool` deleted, every `admin := openPool(t, adminDSN(t))`
   → `dbtest.NewMigratePool(t)` and `app := openPool(t, appDSN(t))` →
   `dbtest.NewAppPool(t)`. `openPool` here had registered its own `t.Cleanup`,
   so no manual `.Close()` existed in test bodies to drop.
3. **scfimport — no freshTenant (uses `truncateCatalog`).** Only `adminDSN`/
   `openPool` (privileged catalog-wipe pool) existed; both deleted. The four
   `openPool(t)` call sites → `dbtest.NewMigratePool(t)` (BYPASSRLS pool backs
   the `TRUNCATE controls CASCADE` + `DELETE FROM scf_anchors` global-catalog
   wipes). `os` kept (`TestLoad_RejectsBadSchemaVersion` uses `os.CreateTemp`);
   `pgxpool` kept (`truncateCatalog`/`loadFixture` signatures); `time` removed.

Verification: `goimports -w` applied; `gofmt -l` clean; `go vet -tags=integration`

- `go build -tags=integration` clean for all three packages; all three suites
  SKIP cleanly with no DB env (`go test -tags=integration -p 1` → 3× `ok`). Live
  integration run deferred to CI (no local Postgres this session — batch-9/10/11
  precedent). No production (`!_test.go`) code touched; shard enrolment unchanged.

detection_tier_actual: none
detection_tier_target: none

## Batch 13 — `internal/control` + `internal/exception` + `internal/decision` + `internal/drift`

First **non-api** batch: these are service-layer suites that test the stores
directly (no `httptest` server), so the migration is pure pool plumbing plus
the `freshTenant` conversions. Files migrated:

1. `internal/control/integration_test.go`
2. `internal/exception/integration_test.go`
3. `internal/decision/integration_test.go`
4. `internal/drift/integration_test.go`

Pool routing: every RLS-bound store assertion → `dbtest.NewAppPool`; every
fixture seed / tenant cleanup pool → `dbtest.NewMigratePool`. No assertion
defaulted to the privileged pool.

freshTenant / harness judgments:

1. **control — `freshTenant` migrated to `dbtest.SeedTenant`.** Pure tenant-scoped
   DELETE in FK order (`evidence_records`, then `controls`). `appDSN`/`adminDSN`/
   `openPool` deleted; `openPool` here registered its own `t.Cleanup(pool.Close)`,
   so no manual closes in test bodies. The four `admin := openPool(t, adminDSN(t))`
   / `app := openPool(t, appDSN(t))` pairs → `NewMigratePool`/`NewAppPool`.
   `seedSCFAnchor` kept inline (takes `*pgxpool.Pool`, INSERTs framework/version/
   anchor rows — not a tenant cleanup), so `pgxpool` import stays.
2. **exception — `freshTenant` migrated to `dbtest.SeedTenant`.** Pure-DELETE on
   `exception_audit_log`, `exceptions`, `controls`. All 22 `admin`/`app`
   `openPool` + `defer .Close()` blocks collapsed to `NewMigratePool`/`NewAppPool`
   (dbtest self-closes, redundant defers removed). `seedControl` kept inline
   (takes pool); `time` kept (`validCreate`).
3. **decision — `freshTenant` kept inline (CARVE-OUT).** Its cleanup interleaves
   `UPDATE decisions SET superseded_by = NULL` before `DELETE FROM decisions`
   (breaks the self-referential `superseded_by` FK pre-delete) — not an ordered
   set of pure DELETEs, so `dbtest.SeedTenant` cannot express it. Kept inline;
   only its pool sourced from `NewMigratePool` at call sites. `TestOverdue`'s
   extra `migrate := openPool(t, adminDSN(t))` → second `NewMigratePool(t)`.
   `seedRisk`/`seedControl`/`seedException` kept inline (take pool).
4. **drift — `freshTenant` migrated to `dbtest.SeedTenant`.** Pure-DELETE on
   `control_drift_snapshots`, `control_evaluations`, `scope_cells`, `controls`.
   `seedControl`/`seedEvaluation`/`seedSnapshot`/`seedEvaluationWithCell` kept
   inline (take pool); `time` kept.

Verification: `goimports -w` applied; `gofmt -l` clean; `go vet -tags=integration`

- `go build -tags=integration` clean for all four packages; all four suites SKIP
  cleanly with no DB env (`go test -tags=integration -p 1` → 4× `ok`). Live
  integration run deferred to CI (no local Postgres this session — batch-9..12
  precedent). No production (`!_test.go`) code touched; shard enrolment unchanged.

detection_tier_actual: none
detection_tier_target: none
