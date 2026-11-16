- **test(coverage):** slice 297 — `internal/policy/seed` coverage lift
  to 89.5% merged (33.3% → 89.5%). Two complementary moves: (1) a new
  pure-Go `internal/policy/seed/seed_unit_test.go` covers the previously
  untested `NewSQLAnchorResolver` constructor and the parser edge
  branches that the existing `seed_test.go` left uncovered (missing
  trailing `---` delimiter, malformed YAML frontmatter, CRLF
  delimiter, leading-whitespace tolerance, non-markdown entries
  filtered, deterministic sort order); (2) a new integration test file
  `internal/policy/seed/seed_integration_test.go` covers the
  DB-touching `Seed` (happy path with both `NoopAnchorResolver` and
  `SQLAnchorResolver`, nil-resolver fallback, wrong-count guard,
  default source-attribution branch, resolved-links materialize on
  the row) and `SQLAnchorResolver.Resolve` (empty-input fast path,
  all-missing, mixed hit-and-miss with input-order preservation,
  duplicate-input handling). The new tests are enrolled in the CI
  integration job by adding `./internal/policy/...` to the
  `tests-integration` package list — matching the slice 287
  precedent for `internal/vendor`. The `internal/policy/seed` floor
  in `cmd/scripts/coverage-thresholds.json` ratchets from 31 → 87 per
  slice 069's `max(0, floor(measured - 2pp))` policy. Spillover from
  slice 279's coverage audit (`docs/coverage-audit-2026-05.md`).
