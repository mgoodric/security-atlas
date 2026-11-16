- **test(coverage):** slice 287 — `internal/vendor` coverage lift to
  87.2% merged (10.1% → 87.2%). The slice 279 audit flagged this
  package as a small-surface (179 statements) `unit-add` target. The
  package shipped an `integration_test.go` in slice 024 but was never
  enrolled in the CI integration job, so its measured coverage was
  unit-only despite covering most of `store.go`. This slice (a) adds
  `./internal/vendor/...` to the `tests-integration` job's package list
  in `.github/workflows/ci.yml` (the same lever slice 279 pulled for
  `internal/frameworkscope` and `internal/risk`), (b) ships a new
  `internal/vendor/helpers_test.go` suite covering every pure-Go branch
  the integration tests do not exercise (`validateInput` happy path +
  all five reject branches, `ReviewCadence.Duration` default-case
  fail-closed guard, `normalizeDomain` / `normalizeOpt` trim-and-lower
  behaviour, `onTime` zero-total / partial / full branches, `pgDate` /
  `fromPgDate` round-trip and nil handling, `pgUUID`, `pgCriticality`),
  and (c) adds `internal/vendor/integration_get_delete_test.go` with
  six new integration tests closing the per-function gap surfaced by
  the audit: `Get` round-trip + `ErrVendorNotFound` + cross-tenant RLS
  isolation, `Delete` happy path + idempotent re-delete on a missing
  row, plus a `no-tenant-context` guard test on `inTx`. Floor in
  `cmd/scripts/coverage-thresholds.json` ratchets from `7` to `85` per
  the slice 069 methodology (`floor(measured - 2pp)`). Spillover from
  slice 279's coverage audit (`docs/coverage-audit-2026-05.md`).
