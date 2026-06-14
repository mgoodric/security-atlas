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

## Batch 14 — `internal/eval` + `internal/freshness` + `internal/staleness` + `internal/artifact`

Four more **non-api** service-layer suites. Files migrated:

1. `internal/eval/integration_test.go`
2. `internal/freshness/integration_test.go`
3. `internal/staleness/integration_test.go`
4. `internal/artifact/integration_test.go`

Pool routing: every RLS-bound store assertion → `dbtest.NewAppPool`; every
fixture seed / tenant cleanup pool → `dbtest.NewMigratePool`. No assertion
defaulted to the privileged pool.

freshTenant / harness judgments:

1. **eval — `freshTenant` migrated to `dbtest.SeedTenant`.** Pure tenant-scoped
   DELETE in FK order (`control_evaluations`, `evidence_records`, `scope_cells`,
   `scope_dimensions`, `controls`). `appDSN`/`adminDSN`/`openPool` deleted; all
   `admin := openPool(t, adminDSN(t))` + `defer .Close()` / `app := openPool(t,
appDSN(t))` + `defer .Close()` blocks collapsed to `NewMigratePool`/
   `NewAppPool` (dbtest self-closes, redundant defers removed). `os` import
   dropped. `seedControl`/`seedEvidence`/`seedEvidenceWithPayload`/`seedScopeCell`/
   `countEvaluations` kept inline (take pool), so `pgxpool` import stays.
2. **freshness — `freshTenant` migrated to `dbtest.SeedTenant`.** Pure-DELETE on
   `evidence_freshness`, `evidence_records`, `controls`. Same `openPool`+`defer`
   → `NewMigratePool`/`NewAppPool` collapse; `os` dropped, `pgxpool` kept for the
   inline `seedControl`/`seedEvidence`/`countEvidenceRecords` helpers.
3. **staleness — `freshTenant` migrated to `dbtest.SeedTenant`.** Pure-DELETE on
   `staleness_rollup_log`, `notifications`, `user_notification_preferences`,
   `evidence_freshness`, `evidence_records`, `controls`, `users`. Same collapse;
   `os` dropped. `TestScheduler_SweepOnce_DrivesRollupPerTenant` builds
   `staleness.New(admin, app, nil)` — `admin` (migrator/BYPASSRLS) →
   `NewMigratePool`, `app` (RLS) → `NewAppPool`, role model preserved.
   `seedUser`/`seedControl`/`seedEvidence`/etc. kept inline (take pool).
4. **artifact — `freshTenant` kept inline (CARVE-OUT).** It returns `uuid.UUID`
   (not `string`): the tests pass the typed id to `tenantCtx` and assert on
   `tenant.String()`, a shape `dbtest.SeedTenant` (returns `string`) cannot
   express. Kept inline; only its pool re-routed via `buildStore`, where
   `admin`/`app` `openPool` → `NewMigratePool`/`NewAppPool` and the per-test
   `defer admin.Close()` lines were removed (dbtest self-closes). **The MinIO/S3
   setup stayed intact** (`minioEndpoint`/`minioBucket`/`newS3Client`/
   `ensureBucket`/`buildStore`'s S3 wiring untouched), so `os` (MinIO env vars)
   and `pgxpool` (freshTenant + buildStore signatures) both stay imported.
   `appDSN`/`adminDSN`/`openPool` deleted.

Verification: `goimports -w` applied; `gofmt -l` clean; `go vet -tags=integration`

- `go build -tags=integration` clean for all four packages; all four suites SKIP
  cleanly with no DB env (`go test -tags=integration -p 1` → 4× `ok`). Live
  integration run deferred to CI (no local Postgres this session — batch-9..13
  precedent). No production (`!_test.go`) code touched; shard enrolment unchanged.

detection_tier_actual: none
detection_tier_target: none

## Batch 15

Files migrated (4 files, 4 distinct packages, each fully owning its DB helpers
in the named file):

1. **board — `freshTenant` migrated to `dbtest.SeedTenant`.** Pure tenant-scoped
   DELETE in FK order (`board_packs`, `board_briefs`, `control_evaluations`,
   `risks`, `controls`, `frameworks`). `appDSN`/`adminDSN`/`openPool` deleted;
   every `admin := openPool(t, adminDSN(t))` + `defer .Close()` / `app := openPool(t,
appDSN(t))` + `defer .Close()` block collapsed to `NewMigratePool`/`NewAppPool`
   (dbtest self-closes, redundant defers removed), including the single app-only
   block in `TestCreate_ValidationErrors`'s board analogue. `os` import dropped;
   `pgxpool` stays (inline `seedFramework`/`seedRisk`/`seedControl`/
   `seedFailingEvaluation` take a pool).
2. **boardnarrative — `freshTenant` kept inline (CARVE-OUT).** It does MORE than
   a tenant-scoped DELETE: it `INSERT`s a `tenants` row first
   (`INSERT INTO tenants (id, name) …`), a shape `dbtest.SeedTenant` does not
   express (SeedTenant inserts no tenant row). Kept inline; only its pool is
   re-routed — every per-test `app := openPool(t, appDSN(t))` / `admin :=
openPool(t, adminDSN(t))` pair → `NewAppPool`/`NewMigratePool`. The old
   `openPool` registered `t.Cleanup(pool.Close)`; dbtest self-closes, so no manual
   close survives. `admin` is created in the test body before `freshTenant(t,
admin)`, so LIFO keeps the migrate pool open during freshTenant's cleanup
   DELETE. `os` dropped, `time` dropped (no longer referenced), `pgxpool` kept
   (helper signatures).
3. **policy — `freshTenant` kept inline (CARVE-OUT).** Its cleanup is NOT a flat
   tenant-scoped DELETE: the first statement is column-scoped
   (`DELETE FROM policies WHERE tenant_id = $1 AND predecessor_id IS NOT NULL`) to
   drop self-FK successors before predecessors — an ordering `dbtest.SeedTenant`
   (one plain `WHERE tenant_id = $1` per table) cannot express (batch-13 self-FK
   precedent). Kept inline; only its pool re-routed. `appDSN`/`adminDSN`/`openPool`
   deleted; the `admin`/`app` `defer .Close()` blocks → `NewMigratePool`/
   `NewAppPool`, and the app-only block in `TestCreate_ValidationErrors` →
   `NewAppPool`. `os` dropped; `pgxpool` kept (freshTenant + `seedControl`
   signatures).
4. **policy/seed — `freshTenant` kept inline (DOUBLE CARVE-OUT).** Two reasons
   `dbtest.SeedTenant` cannot express: (a) it returns `uuid.UUID` (callers pass
   the typed id to `seed.Seed` / `seedControlWithSCFID` / `admin.QueryRow`), not
   the `string` SeedTenant yields (batch-14 `uuid.UUID` precedent); (b) the same
   `predecessor_id IS NOT NULL`-first self-FK cleanup as policy (batch-13
   precedent). Kept inline; only its pool re-routed — the `admin`/`app` `openPool`
   pairs → `NewMigratePool`/`NewAppPool`, and the three standalone
   `admin := openPool(t, adminDSN(t))` resolver tests → `NewMigratePool`. The old
   `openPool` had `t.Cleanup(pool.Close)`; dbtest self-closes, so no manual close
   survives. `os`/`time` dropped; `pgxpool` kept (helper signatures).

Verification: `goimports -w` applied; `gofmt -l` clean; `go vet -tags=integration`

- `go build -tags=integration` clean for all four packages; all four suites SKIP
  cleanly with no DB env (`go test -tags=integration -p 1` → `ok` for board,
  boardnarrative, policy, policy/seed, and the untouched sibling policy/pdf). The
  `internal/board` golden-file (`pdf_html_golden_test.go`) + unit tests were NOT
  touched and run as unit tests. Live integration run deferred to CI (no local
  Postgres this session — batch-9..14 precedent). No production (`!_test.go`) code
  touched; shard enrolment unchanged.

detection_tier_actual: none
detection_tier_target: none

## Batch 16

Files migrated (4 files, 4 distinct packages, each fully owning its DB helpers
in the named file; siblings — `helpers_test.go`, `ollama_test.go`,
`service_test.go`, `qaisuggest_test.go`, `gapexplain_test.go`, the excel/pdf/
library/fuzz unit tests, `store_helpers_test.go` — are unit tests that do NOT
reference `appDSN`/`adminDSN`/`openPool`/`freshTenant` and were left untouched):

1. **llm — clean full removal.** `getenv`/`appDSN`/`adminDSN`/`openPool` deleted;
   `freshTenant` (pure single-table tenant-scoped DELETE on `ai_generations`,
   returns string) delegates to `dbtest.SeedTenant`. Every `app := openPool(t,
appDSN(t))` / `admin := openPool(t, adminDSN(t))` (incl. the admin-only
   `TestReusableCheckTemplate_RejectsAtDBLayer`) → `NewAppPool`/`NewMigratePool`
   (dbtest self-closes; the old `openPool` registered `t.Cleanup(pool.Close)`, now
   redundant). `os` dropped (only `getenv` used it); `time` kept (`validReq`);
   `pgxpool` kept (`mutateUnderTenant`/`rawInsertUnknownSurface`/`freshTenant`
   signatures). NO fake-Ollama HTTP server in this file — it uses `llm.StubClient`
   (the slice-498 CI seam), so there was no HTTP server setup/teardown to preserve.
2. **qaisuggest — clean full removal.** `appDSN`/`adminDSN`/`openPool` deleted;
   `freshTenant` (FK-ordered tenant-scoped DELETE over 6 tables, returns string)
   delegates to `dbtest.SeedTenant`. All `app`/`admin` pairs + the admin-only
   `TestDBGuard_RejectsApprovedWithoutApprover` re-routed. `os` + `time` dropped
   (no remaining references); `pgxpool` kept (`freshTenant`/`seedQuestion`/
   `seedPolicy`/`seedEvidence` signatures). Uses `llm.StubClient` — no live Ollama.
3. **questionnaire — clean full removal.** `package questionnaire` (in-package, not
   `_test`). `appDSN`/`adminDSN`/`openPool` deleted; `freshTenant` (FK-ordered
   tenant-scoped DELETE over 4 tables, returns string) delegates to
   `dbtest.SeedTenant`. App-only `TestStore_CreateQuestionnaire_RejectsMissingTenant`
   → `NewAppPool`. `os`/`time`/`uuid` dropped (no remaining references); `pgxpool`
   KEPT for the `freshTenant(admin *pgxpool.Pool)` parameter only.
4. **gapexplain — clean full removal.** `appDSN`/`adminDSN`/`openPool` deleted;
   `freshTenant` (FK-ordered tenant-scoped DELETE over 3 tables, returns string)
   delegates to `dbtest.SeedTenant`. All `app`/`admin` pairs re-routed. `os`
   dropped; `time`/`fmt` kept (seeders + tests); `pgxpool` kept (seeder signatures).
   Uses `llm.StubClient` — no live Ollama.

No `freshTenant` carve-out was needed: all four are pure FK-ordered tenant-scoped
DELETEs returning a `string`, exactly the `dbtest.SeedTenant` shape (no
tenant-row INSERT, no `uuid.UUID` return, no self-FK column-scoped ordering).

Verification: `goimports -w` applied; `gofmt -l` clean; `go vet -tags=integration`

- `go build -tags=integration` clean for all four packages; all four suites
  return `ok` under `go test -tags=integration -p 1` with no DB env (clean SKIP +
  untouched unit tests pass). Live integration run deferred to CI (no local
  Postgres this session — batch-9..15 precedent). No production (`!_test.go`) code
  touched; shard enrolment unchanged.

detection_tier_actual: none
detection_tier_target: none

## Batch 17

Files migrated (4 files, 4 distinct packages, each fully owning its DB helpers
in the named `integration_test.go`; the sibling unit tests — `registry_test.go`,
`worker_test.go`, `helpers_test.go`, `scheduler_test.go`, `checklist_test.go`,
`roles_test.go`, `export_test.go` — do NOT reference
`appDSN`/`adminDSN`/`openPool`/`freshTenant` and were left untouched):

1. **internal/metrics/eval — carve-out (uuid.UUID freshTenant).** `adminDSN` +
   `openPool` deleted; every `admin := openPool(t, adminDSN(t))` → `NewMigratePool`
   (8 sites; this suite has no app pool — every evaluator runs through the BYPASSRLS
   pool per the slice-294 header comment, role model preserved). `freshTenant` returns
   `uuid.UUID` (the seeders key off it), NOT the string `dbtest.SeedTenant` returns,
   so it stays INLINE; only its pool is re-routed. `os`/`time` dropped (only the
   deleted DSN/openPool helpers used them); `uuid`/`pgxpool`/`strings` kept
   (freshTenant + seeder signatures + error-substring asserts).
2. **internal/metrics/scheduler — carve-out (uuid.UUID freshTenant).** `appDSN` +
   `adminDSN` + `openPool` deleted; `admin`/`app` pairs (4 sites) →
   `NewMigratePool`/`NewAppPool` (role model preserved: RLS-enforced sweepTenant
   transaction runs through the app pool, BYPASSRLS seeding through migrate).
   `freshTenant` returns `uuid.UUID` → stays inline, pool re-routed only. `os`
   dropped; `time` KEPT (`1*time.Hour`, `time.After` in the Run-loop test);
   `uuid`/`pgxpool` kept.
3. **internal/backup — carve-out (migratorDSN raw-DSN-string).** `appDSN` +
   `openPool` deleted; `pool := openPool(t, migDSN)` (4 sites) → `NewMigratePool`,
   `appPool := openPool(t, appDSN(t))` → `NewAppPool` (AC-7 tenant-role-denied
   assertion preserved through the app pool). `migratorDSN` stays INLINE: the raw
   `DATABASE_URL` string is load-bearing beyond pool creation — it is passed to
   `backup.NewVerifier(...)` (spins an ephemeral restore DB) and `assertNoEphemeralDBs`
   (`pgx.Connect`); `dbtest.NewMigratePool` returns a pool, not the DSN. The
   MinIO/object-store scaffolding (`newMinioClient`, `s3.*`, bucket-create) and the
   pg_dump/restore/ephemeral-DB scaffolding stayed fully intact — only the Postgres
   pool plumbing was migrated. `os`/`time`/`pgxpool` all KEPT (migratorDSN +
   newMinioClient + os.WriteFile; time.Date; helper signatures).
4. **internal/checklist — clean full removal.** `appDSN` + `adminDSN` + `openPool`
   deleted; `freshTenant` (pure FK-ordered tenant-scoped DELETE over 6 tables,
   returns string) delegates to `dbtest.SeedTenant`. All `app`/`admin` pairs +
   the two admin-only DB-guard tests re-routed to `NewAppPool`/`NewMigratePool`.
   `tenantCtx` (a thin `tenancy.WithTenant` wrapper) kept inline — it is outside the
   pool/DSN/freshTenant helper set this drain targets and is not a copy of a shared
   dbtest symbol; `tenancy` import retained. `os`/`time` dropped; `uuid`/`pgxpool`
   kept (freshTenant signature + seeders). Uses `llm.StubClient` (slice-498 CI seam)
   — no live Ollama, so no HTTP server setup/teardown to preserve.

Three carve-outs this batch (eval + scheduler uuid.UUID freshTenant; backup
raw-DSN-string migratorDSN), all consistent with the batch-13/14/15 precedent
of leaving a helper inline when it does more than dbtest.SeedTenant can express
(non-string return) or when its raw output is used beyond pool creation.

Verification: `goimports -w` applied; `gofmt -l` clean; `go vet -tags=integration`

- `go build -tags=integration` clean for all four packages; all four suites return
  `ok` under `go test -tags=integration -p 1` with no DB env (clean SKIP + untouched
  unit tests pass). Live integration run deferred to CI (no local Postgres this
  session — batch-9..16 precedent). No production (`!_test.go`) code touched; shard
  enrolment unchanged.

detection_tier_actual: none
detection_tier_target: none

## Batch 18

Files migrated (4 files, 4 distinct packages, each fully owning its DB helpers
in the named file; the sibling unit tests in these packages do NOT reference
`appDSN`/`adminDSN`/`openPool`/`freshTenant` and were left untouched):

1. **internal/evidence/streambuf — carve-out (NATS JetStream harness kept).**
   `openPool` deleted; the `boot` fixture's `openPool(t, envOrSkip(t, "DATABASE_URL"))`
   → `dbtest.NewMigratePool(t)` and `openPool(t, envOrSkip(t, "DATABASE_URL_APP"))`
   → `dbtest.NewAppPool(t)` (role model preserved: the BYPASSRLS migrate pool drives
   the advisory-lock + TRUNCATE + platform-schema seed; the app pool backs
   `ingest.New` and every RLS-scoped count/audit/payload assertion). The dbtest pools
   self-close, so the manual `adminPool.Close()`/`appPool.Close()` on the
   `streambuf.Open` error path AND in the `t.Cleanup` closure were removed; the NATS
   stream teardown (`sc.JS().DeleteStream(...)` + `sc.Close()`) is KEPT fully intact.
   `envOrSkip` stays inline — it is still the only skip path for `NATS_URL` (not a
   DSN/pool helper, outside the grep AC). `os`/`time`/`pgxpool`/`uuid` etc. all KEPT
   (envOrSkip, AckWait, fixture struct `pool *pgxpool.Pool`, per-test stream uuid).
2. **internal/frameworkscope — clean removal + setupHTTPServer hoist.** `appDSN` +
   `adminDSN` + `openPool` deleted; `freshTenant` (pure FK-ordered tenant-scoped
   DELETE over 8 tables, returns string) delegates to `dbtest.SeedTenant`. All
   `admin`/`app` pairs + the two admin-only HTTP tests re-routed to
   `NewMigratePool`/`NewAppPool`. `setupHTTPServer`'s `app` pool → `dbtest.NewAppPool`,
   created BEFORE the server-teardown `t.Cleanup` so LIFO closes the httptest server
   first while the pool is still open; the redundant `app.Close()` was dropped, the
   `ts.Close()` KEPT. `seedFrameworkVersion`/`seedScopeAndControl`/`withAdminTenant`
   kept inline (outside the pool/DSN/freshTenant helper set; they perform extra row
   inserts + GUC-scoped admin transactions `SeedTenant` cannot express). `os` dropped;
   `uuid`/`pgxpool`/`pgx` kept.
3. **internal/mcp/writeproposals — clean full removal.** `appDSN` + `adminDSN` +
   `openPool` deleted; `freshTenant` (pure single-table tenant-scoped DELETE of
   `mcp_write_proposals`, returns string) delegates to `dbtest.SeedTenant`. Although
   this package adopts the `ai_assist_human_approver_guard` column set, its seed
   helper only DELETEs (it does NOT insert proposal rows — the tests create those via
   `store.Create`), so it is a CLEAN migration, not a carve-out. All `admin`/`app`
   pairs + the admin-only schema-invariant test (`TestSchemaInvariant_...`) re-routed
   to `NewMigratePool`/`NewAppPool`; the `mcp_wp_ai_assist_invariant` CHECK assertion
   and the BYPASSRLS-admin direct-INSERT path are preserved. `os`/`time` dropped;
   `uuid`/`pgxpool`/`pgx`/`pgconn` kept.
4. **internal/platform — clean full removal (status_integration_test.go).** `adminDSN`
   - `appDSN` + `openPool` deleted; every `openPool(t, adminDSN(t))` → `NewMigratePool`
     and `openPool(t, appDSN(t))` → `NewAppPool`. No `freshTenant` here — the singleton
     `platform_status` + bootstrap fixtures are managed by `resetPlatformStatus` and
     `resetBootstrapFixtures` (UPDATE / TRUNCATE-CASCADE helpers), which are NOT the
     pool/DSN helper set this drain targets and stay inline. Role model preserved: the
     public-read RLS + app-cannot-write P0 assertions run through the app pool; the
     write/reset/seed paths through the BYPASSRLS migrate pool. The original `openPool`
     self-closed via `t.Cleanup`, so there were no manual `.Close()` calls to drop.
     `os` dropped; `time`/`uuid`/`pgxpool` kept.

One carve-out this batch (streambuf's NATS JetStream harness — Postgres pool
plumbing migrated, broker scaffolding left intact); the other three are clean
full removals. writeproposals was verified to be a clean removal (its seed helper
DELETEs only) despite carrying the AI-assist guard columns.

Verification: `goimports -w` applied; `gofmt -l` clean; `go vet -tags=integration`

- `go build -tags=integration` clean for all four packages; all four suites return
  `ok` under `go test -tags=integration -p 1` with no DB env (clean SKIP + untouched
  unit tests pass). Live integration run deferred to CI (no local Postgres this
  session — batch-9..17 precedent). No production (`!_test.go`) code touched; shard
  enrolment unchanged.

detection_tier_actual: none
detection_tier_target: none

## Batch 19

Files migrated (4 files, 4 distinct packages — the notify cluster; the sibling
unit tests in these packages (config_test.go, kindfilter_test.go,
message_test.go, scheduler_test.go, slack_test.go, webhook_test.go) do NOT
reference the pool helpers and were left untouched):

1. internal/notify/email/integration_test.go
2. internal/notify/scheduler/integration_test.go
3. internal/notify/slack/integration_test.go
4. internal/notify/webhook/integration_test.go

These four suites were already partially on the harness (each imported
`internal/dbtest` and used `dbtest.WithTenantCtx`); each defined a `openPools`
TUPLE helper `func openPools(t *testing.T) (app, admin *pgxpool.Pool)` that was
itself just a thin wrapper returning `dbtest.NewAppPool(t), dbtest.NewMigratePool(t)`.
The drain split that tuple helper at every call site: `app, admin := openPools(t)`
→ `app := dbtest.NewAppPool(t)` / `admin := dbtest.NewMigratePool(t)`, and the
`openPools` func was deleted from each file. Every call site uses BOTH returned
pools, so both are created at each site. No `appDSN`/`adminDSN`/`openPool`/`freshTenant`
existed in any of the four files — nothing else to remove.

`freshTenant` carve-out: none of these files has one. Each owns a `seedUser`
helper that `uuid.New()`s a fresh tenant, INSERTs `users` (+ optional
`notifications`/opt-in rows), and registers a column-FK-ordered tenant-scoped
cleanup — this is a row-seeding helper `dbtest.SeedTenant` cannot express
(returns a `(uuid.UUID, uuid.UUID)` tuple, seeds extra rows), so `seedUser`
(and the sibling `seedNotification`/`seedEmailPref`/`setPref`) stay inline,
re-routed only to the migrate pool, exactly as before.

Fake-delivery-server teardown: these notify suites stand up in-memory fake
delivery sinks (`fakeProvider` Send for email/scheduler, `fakeTransport` Post
for slack/webhook) — pure in-process structs, not httptest servers, so there is
no `ts.Close()` to keep; they were left fully intact. No manual pool `.Close()`
calls existed (the dbtest pools self-close via `t.Cleanup`). `pgxpool` stays
referenced (every `seed*`/`setPref` signature takes `*pgxpool.Pool`); `uuid`,
`context`, `sync`, `strings` all still referenced.

Role model preserved throughout: RLS-bound delivery + assertions run through
`dbtest.NewAppPool`; the BYPASSRLS `dbtest.NewMigratePool` is used only for
cross-tenant seeding and the append-only delivery-log cleanup the app role
cannot DELETE (scheduler additionally runs its BYPASSRLS enumeration SELECT
through the migrate pool, unchanged).

Verification: `goimports -w` applied; `gofmt -l` clean; `go vet -tags=integration`

- `go build -tags=integration` clean for all four packages; all four suites
  return `ok` under `go test -tags=integration -p 1` with no DB env (clean SKIP +
  untouched unit tests pass). Live integration run deferred to CI (no local
  Postgres this session — batch-9..18 precedent). No production (`!_test.go`) code
  touched; shard enrolment unchanged.

detection_tier_actual: none
detection_tier_target: none
