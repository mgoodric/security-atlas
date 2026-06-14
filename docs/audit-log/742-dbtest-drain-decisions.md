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
