- **test(coverage):** slice 293 — `internal/api/metrics` coverage lift
  from 0.4% to 87.3% merged. The package shipped in slice 076 as the
  HTTP surface for the metrics catalog, cascade, observation series,
  manual inputs, and tenant targets but had zero tests of its own. This
  slice adds two test files: a pure-Go `handlers_test.go` covering
  `New`, `tenantContext` (no-credential + empty-tenant + missing-GUC +
  happy path), every pre-DB rejection branch on the auth-gated handlers
  (`ListObservations`, `CreateInput`, `GetTarget`, `UpsertTarget` —
  401 / 403 / 400 on URL params, JSON body, direction switch, owner
  UUID, every accepted direction case), and every pure helper
  (`numericPtr` nil/non-nil, `numericString` invalid/valid,
  `numericStringMaybe` invalid/valid, `uuidString` invalid/valid, the
  four wire mappers `metricWireFromRow` / `observationWireFromRow` /
  `inputWireFromRow` / `targetWireFromRow` with present + absent
  optional fields, and `writeJSON` / `writeError`). And a
  `//go:build integration` `integration_test.go` covering the
  post-auth DB-touching branches: `ListCatalog` (level + category
  filters return seeded rows), `GetCatalog` (happy path with
  parents/children populated + 404 not-found), `GetCascade` (default
  level + explicit depth + truncation header on depth > 6 + garbage
  depth fallback), `CreateInput` (manual_input happy path through the
  `runInTx` transaction lifecycle + the metric_inputs replicate
  trigger, default-time + default-dimensions defaults branch, wrong-
  strategy 409 conflict on a `computed` row, not-found 404 on an
  unknown id, 403 admin-gate via the live `jwtmw` middleware path),
  `ListObservations` (reflects the manual-input replicate trigger,
  since/until/limit bounds parsing, non-covering window returns zero
  count), `Target` (pre-upsert 404, full upsert with all three
  thresholds + owner, update path exercising the upsert UPDATE
  branch, `target_is_better` third-direction case, 403 admin-gate).
  The package is enrolled in CI's integration test list (slice
  287/284/297/283/290/295 pattern). Per-function coverage moves
  `New` 0% → 100%, `ListCatalog` 0% → 81.8%, `GetCatalog` 0% →
  65.4%, `GetCascade` 0% → 91.7%, `ListObservations` 0% → 94.9%,
  `CreateInput` 0% → 80.8%, `GetTarget` 0% → 90.0%, `UpsertTarget`
  0% → 90.2%, `tenantContext` 0% → 100%, `runInTx` 0% → 80.0%,
  every wire mapper + pure helper to 100%. Floor in
  `cmd/scripts/coverage-thresholds.json` ratchets from `0` to `85`
  per slice 069's `floor(measured - 2pp)` methodology. Spillover
  from slice 279's coverage audit
  (`docs/coverage-audit-2026-05.md`).
