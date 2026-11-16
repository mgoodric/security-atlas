- **test(coverage):** slice 290 — `internal/api/controldetail` coverage
  lift from 29.3% to 92.7% merged. The control-detail HTTP handler
  package shipped a 932-line `integration_test.go` in slice 064 that
  was never enrolled in the CI integration job, so the slice 279 audit
  measured 29.3% (unit-only). Enrolling
  `./internal/api/controldetail/...` in the `Go · integration (Postgres
RLS)` job (same playbook as slice 279 for `internal/frameworkscope` +
  `internal/risk`, slice 287 for `internal/vendor`, slice 284 for
  `internal/scope`, and slice 297 for `internal/policy/...`) lifts the
  store-touching surface to its real measured ceiling. The companion
  new `internal/api/controldetail/helpers_test.go` pins the pure-Go
  helpers the integration suite does not exercise: cursor
  encode/decode round-trip + all four malformed paths (bad base64,
  missing separator, bad RFC3339 timestamp, bad UUID), `parseLimit` /
  `parseRFC3339` boundary cases (default, at-cap, below-min, above-
  max, non-int, RFC3339 fallback, malformed), the three page-splitting
  functions' both no-next-page and has-next-page branches with cursor
  round-trip assertions, the seven wire-conversion helpers (`pgUUID` /
  `pgTimestamptz` / `uuidString` / `uuidPtr` / `tsString` /
  `jsonOrNull` / `numericToFloat`) on both their valid and null/empty
  branches, the two row-to-wire converters (`evidenceWireFrom` +
  `evidenceWireFromListRow`) including the null-scope JSONB-null
  branch, `hasControlRead` role-derivation on all three accept signals
  (admin, approver, owner-role) and the no-signal reject case, and the
  remaining 400/401/403 branches on the Policies / Risks / History
  handlers via `guardAndResolvePathControl` (malformed UUID path
  param, missing tenant context, no-control-read-role credential,
  malformed cursor, out-of-range limit). Unit-only coverage moves
  28.6% → 59.7%; merged moves 29.3% → 92.7%. Floor in
  `cmd/scripts/coverage-thresholds.json` ratchets from `25` to `90`
  per slice 069's `floor(measured - 2pp)` methodology. Spillover from
  slice 279's coverage audit (`docs/coverage-audit-2026-05.md`).
