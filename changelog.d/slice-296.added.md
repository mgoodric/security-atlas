- **test(coverage):** slice 296 — `internal/catalog/metrics` coverage
  lift from 64.7% to 78.4% unit-only. A new white-box
  `internal/catalog/metrics/helpers_test.go` exercises the pure-Go
  branches the existing `loader_test.go` (slice 076) leaves untouched:
  the `Load` nil-evaluator default-to-`EmptyRegistry` branch, the
  empty-FS / non-yaml-skip / malformed-yaml / empty-metrics-file
  rejection paths, every `validateMetric` per-field rejection (missing
  id, non-snake-case id, missing name/description/unit/category,
  invalid level / cadence / compute_strategy, empty parent_id, weight
  out of (0,1] for both negative and >1 extremes), the
  `EmptyRegistry.Has` / `MapRegistry.Has` lookup semantics, the
  `NewSeeder` constructor, the `Apply` nil-pool early-return, and
  `formatWeight`'s fixed-four-fractional-digit pgtype-Numeric encoding.
  Per-function coverage moves `validateMetric` 65.6% → 100%, `Load`
  86.8% → 96.2%, `NewSeeder` 0% → 100%, `formatWeight` 0% → 100%,
  `EmptyRegistry.Has` 0% → 100%. The remaining uncovered surface
  (`SeedFromEmbedded` + most of `Apply`) requires a live pgx pool and
  is covered by the package's existing slice 076 `//go:build
integration` suite. Floor in `cmd/scripts/coverage-thresholds.json`
  ratchets from `0` to `76` per slice 069's `floor(measured - 2pp)`
  methodology. Spillover from slice 279's coverage audit
  (`docs/coverage-audit-2026-05.md`).
