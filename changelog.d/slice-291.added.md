- **test(coverage):** slice 291 — `internal/api/controls` coverage lift
  from 26.3% to 75.7% merged. Spillover from slice 279's coverage audit
  (docs/coverage-audit-2026-05.md). The audit flagged `internal/api/controls`
  at 26.3% merged (559 stmts) as a `unit-add` target — investigation
  surfaced the same root cause slices 287 / 284 / 290 / 297 each found
  in their packages: the package shipped substantial
  `*_integration_test.go` files (slice 011 attest 750 lines, slice 137
  export 610 lines, slice 175 history_export 552 lines, slice 151 list
  289 lines = ~2,200 lines total) but was never enrolled in the CI
  integration job, so the audit measured unit-only coverage. Lift
  strategy mirrors that playbook plus a companion pure-Go test file:
  (1) enroll `./internal/api/controls/...` in the `Go · integration
(Postgres RLS)` job's `-coverpkg` list (ci.yml); (2) add
  `internal/api/controls/helpers_test.go` covering the pure-Go surface
  the integration suite does not exercise — `isManualImplementation`
  (every implementation_type enum + case sensitivity + whitespace),
  `decodeJSONBObject` (nil / empty / valid / malformed branches),
  `deriveIdempotencyKey` (determinism + attest- prefix + 32-char hex
  suffix + per-input-change collision invariant + zero-byte separator
  invariant), `ingestErrorToStatus` (all seven sentinel→status mappings
  - catch-all 500 + errors.Join wrapped sentinel), `writeAttestJSON` /
    `writeAttestError` / `writeJSON` / `writeError` (Content-Type +
    status + envelope contract), `writeControlLookupError` (pgx.ErrNoRows
    → 404 vs generic-error → 500 with load-control prefix),
    `controlsHasProgramRead` (admin / approver / owner-roles / empty-
    slice rejection / plain-cred rejection), `controlsCountingWriter`
    (single-write + multi-write byte tallies + nil-write pass-through),
    and `exportLimiter` accessor for both ExportHandler and
    HistoryExportHandler (defaults to process-wide singleton). Also
    fixes a bit-rot in `list_integration_test.go`: the seed fixture
    used an obsolete enum value `'preventive'` for
    `control_implementation_type` (valid values are
    automated / semi_automated / manual_attested / manual_periodic) —
    rotted while the file was unenrolled from CI; corrected to
    `'automated'`. Coverage-threshold floor for `internal/api/controls`
    ratchets from 26 to 73 (`floor(75.7 − 2)`) per the slice 069 ratchet
    contract and slice 279 measurement methodology
    (`floor(measured − 2pp)`). Net new test surface: 1 new file (414
    lines unit tests for helpers), no production code changes, no new
    dependencies. Honors invariant 6 (RLS continues to be the
    integration suite's tenancy gate). [#291]
