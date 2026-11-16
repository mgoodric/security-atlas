- **test(coverage):** slice 279 — coverage measurement now reads a
  MERGED unit + integration profile. The unit-only profile that slice
  069 enforced against was UNDER-counting coverage for packages whose
  tests are exclusively `//go:build integration`-tagged. Slice 279
  extends `cmd/scripts/coverage-gate` with an `-extra-profile=` flag
  (in-process merge with `gocovmerge` semantics) and re-points the CI
  coverage gate to run in the `tests-integration` job against the
  merged profile. Five packages get lifted floors with new unit tests:
  `internal/decision` (2 → 70), `internal/frameworkscope` (19 → 75),
  `internal/risk` (10 → 71), `internal/eval` (14 → 65),
  `internal/board` (20 → 31). Seven packages get floor-only ratchets
  to reflect the integration coverage they already had:
  `internal/api` (11 → 69), `internal/api/credstore` (24 → 69),
  `internal/api/schemaregistry` (26 → 71), `internal/authz` (29 → 70),
  `internal/control` (47 → 72), `internal/featureflag` (23 → 82),
  `internal/tenancy` (51 → 90). Plus a bonus catch from the integration
  list extension: `internal/risk/aggrule` (17 → 72). Audit doc at
  `docs/coverage-audit-2026-05.md` enumerates every below-70% package
  with a disposition (`unit-add` / `count-integration` / `exempt`) and
  reserves spillover slice slots 282-303 for the long tail of
  `unit-add` packages not lifted in this slice. The
  `$tier_recommendations` block in `coverage-thresholds.json` is
  DOCUMENTATION only — the per-package `thresholds` map is the only
  enforced surface (P0-279-6). Fix-forward: re-enrolls
  `internal/frameworkscope/integration_test.go` with the missing
  `bundle_id` INSERT (migration 20260511000009 backfill pattern); the
  tests had been broken since `bundle_id` landed but the package was
  never in CI's integration list, so the break never surfaced. See
  [`docs/audit-log/279-coverage-audit-decisions.md`](docs/audit-log/279-coverage-audit-decisions.md)
  for the six D-decisions (D1-D6 + D1a) and the revisit list.
