- **test(coverage):** slice 310 — `internal/api/soc2import` coverage lift
  to 77.4% merged. Spillover from slice 279's coverage audit
  (`docs/coverage-audit-2026-05.md`). The audit flagged
  `internal/api/soc2import` at floor `23` in
  `cmd/scripts/coverage-thresholds.json`. Investigation surfaced the same
  root cause slices 290 / 291 / 293 / 297 each found in their packages:
  the package shipped a 287-line `import_test.go` in slice 007 (build
  tag `integration`) but was never enrolled in the CI integration job's
  `-coverpkg` list, so the merged audit measured the same 25.2% as the
  unit-only run — every Import / importIntoTx / requirement / edge /
  idempotency / source_attribution / STRM-distribution branch exercised
  by the suite contributed zero to the gate. Lifting strategy mirrors
  that playbook plus a companion pure-Go test file:
  1. Enroll `./internal/api/soc2import/...` in the
     `Go · integration (Postgres RLS)` job's `-coverpkg` list
     (`.github/workflows/ci.yml`).
  2. Add `internal/api/soc2import/helpers_test.go` covering the pure-Go
     surface the integration suite does not exercise:
     - `uuidFromString` (determinism + distinct-seeds-differ +
       empty-seed)
     - `uuidString` (36-char canonical + 4-hyphen format on valid +
       `""` on `pgtype.UUID{}` / `Valid: false`)
     - `parseDate` (empty + valid `2017-04-01` + five malformed shapes:
       not-a-date, slash separators, prose, month 13, missing day)
     - `edgeContentEqual` (all-equal true + each of the four content
       fields rejected when it diverges)
     - `Import` BeginTx-error branch via a `stubBeginner` that returns
       a sentinel error — locks the "soc2import: begin tx" wrap +
       zero-Report contract.
       Coverage lift:
  - Unit-only: 25.2% → 39.6%
  - Merged (unit + integration): 25.2% → 77.4%
    Floor in `cmd/scripts/coverage-thresholds.json` ratchets from `23` to
    `75` per slice 069's `floor(measured - 2pp)` methodology
    (`floor(77.4 - 2) = 75`). Honors slice 069's monotonic-ratchet
    contract: tests written + floor lifted in the same PR; no other
    floors touched; `_STATUS.md` untouched per `P0-310-3` (orchestrator's
    surface). Constitutional invariants honored: invariant 1 (one control,
    N framework satisfactions — the integration suite asserts no
    `fw_to_fw` table exists and `framework_requirements` is referenced by
    exactly one FK, locking the SCF-anchor traversal); invariant 6 (RLS
    isolation — the suite seeds the SOC2 framework as a global
    `tenant_id IS NULL` row and uses the established `atlas_app` role
    with the `app.current_tenant` GUC pattern via `DATABASE_URL_APP`).
    No vendor-prefixed tokens in fixtures per `CLAUDE.md`'s hard rule —
    the new test strings (`test-sentinel-begin-failure`, `round-trip-seed`)
    are neutral. Closes
    [#310](docs/issues/310-coverage-soc2import.md).
