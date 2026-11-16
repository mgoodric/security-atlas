- **test(coverage):** slice 284 — `internal/scope` coverage lift to
  87.6% merged (35.3% → 87.6%). Two complementary moves: (1) the
  package's existing slice-017 integration suite — never enrolled in
  the CI integration job since slice 017 shipped — is now in the
  `tests-integration` `-coverpkg=./internal/scope/...` target list,
  matching the slice 279 precedent for `internal/frameworkscope` +
  `internal/risk`; (2) a new pure-Go `internal/scope/expr_helpers_test.go`
  covers the previously-untested `DefaultEngine.Applicability` /
  `NewEngine` Engine wrapper that slice 012's evaluation engine
  imports, the `Canonicalize` empty-key rejection, the
  `writeJSONString` escape branches (tab / newline / CR / quote /
  backslash / `\u0007` sub-0x20 fallback) reached via the public
  `Canonicalize` API, the lexicographic-key-order guarantee that backs
  the DB UNIQUE(tenant_id, dimensions_hash), and additional
  rejection-loud paths through `validate()`'s recursive descent
  (`in` with missing `dim`, deeply-nested malformed `eq` propagating
  out of an `or` inside an `and`, `in` against a cell that lacks the
  queried dimension). Three pre-existing integration-test control
  inserts gained a `bundle_id` column to satisfy a NOT-NULL constraint
  added by a later slice — the suite had silently bit-rotted while it
  was out of the CI integration list. A local `strings` shim that
  collided with the stdlib package was removed in favour of
  `strings.NewReader` once the unit tests imported `strings` for
  real. The `internal/scope` floor in
  `cmd/scripts/coverage-thresholds.json` ratchets from 33 → 85 per
  slice 069's `floor(measured - 2pp)` policy. Spillover from slice
  279's coverage audit (`docs/coverage-audit-2026-05.md`).
