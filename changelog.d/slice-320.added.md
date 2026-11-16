- **test(coverage):** slice 320 — `internal/demoseed` coverage lift from
  4.4% to 83.1% merged. Round-3 coverage spillover surfaced during slice
  312's audit (`docs/coverage-audit-2026-05-round-3.md`) — the slice-205
  demo seed dataset (522 stmts) measured 4.4% merged (unit-only) because
  the comprehensive 341-line `integration_test.go` shipped in slice 205
  was never enrolled in CI's `tests-integration` job. The slice doc
  predicted "data-heavy, likely move to excludes"; local measurement
  disproved that — the package has substantive behavioral surface (one
  Apply orchestrator with 5 branches: idempotent / refusal / scale /
  happy / invalid-slug; one symmetric Teardown; 17 writer functions; 4
  pure-Go sentinel detectors; the scale knob). Enrolment alone lifts
  the package from 4.4% to 80.7% merged; the new unit suites pay off
  the pure-Go branches integration cannot reach efficiently. New
  `internal/demoseed/helpers_test.go` covers `capitalize` (empty +
  lower + non-lower), `kindToConnector` (vendor.kind split + no-dot
  fallback), `riskScoreJSON` (likelihood × impact = rating contract),
  `fictionalUserEmail` (lower-case + idx-mod wrapping), the
  `buildEvidencePayload` default branch + known-kind non-nil sanity,
  the `withTenant` / `currentTenantOf` context round-trip, and the
  writer helpers `nullableUUID` / `nonZeroOrSelf` / `nonZeroOrTenant`
  / `periodStatus` / `frozenHashOrNil` / `frozenByOrNil` / `sha256Of`.
  New `internal/demoseed/seeder_test.go` covers the `NewSeeder` nil-pool
  sentinel + guard-order, `WithClock` chainable mutator, the
  `validateSlug` enumerated bad-input branches (empty / too-long /
  non-alnum-first / mid-string space + underscore + dot) + happy
  paths, the `pgIsUndefinedTable` sentinel detection (SQLSTATE 42P01
  substring + "does not exist" substring + non-matching), the
  `applyScale` scale-clamp edges (minimum-1 at 0.1, pass-through at
  1.0, doubled at 2.0), `hashCanonicalJSON` determinism + 32-byte
  digest, `hexString` shape, and the `DemoSeedVersion` / `DefaultScale`
  / `PopulatedRowCap` constant invariants (forensic-mark + clamp range
  - AC-3 threshold). `internal/demoseed/...` enrolled in CI's
    `tests-integration` job via `-coverpkg=./...`. One new floor added to
    `cmd/scripts/coverage-thresholds.json` at 81 (`max(0, floor(83.1 -
2)) = 81` per slice 069 methodology). Constitutional invariants
    honored — append-only ledger (P0-320-4): every test seeds via INSERT
    only; cleanup via the seeder's own `Teardown` against the BYPASSRLS
    admin pool, never the app role; no vacuous struct-literal tests
    (P0-279-7); cross-tenant RLS isolation directly pinned by
    `TestApply_CrossTenantIsolation`. Same playbook as slices 290 / 297
    / 310 / 315 — enrolment is the load-bearing move.
