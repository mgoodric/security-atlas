- **test(coverage):** slice 318 — audit-log family coverage lift to 70%+
  merged. Round-3 coverage spillover surfaced during slice 312's audit
  (`docs/coverage-audit-2026-05-round-3.md`) — three audit-log family
  packages measured below 70% merged: `internal/audit` umbrella (231
  stmts, 0.4% merged), `internal/audit/sink` (150 stmts, 67.3% merged —
  2.7pp short), and `internal/audit/unifiedlog` (32 stmts, 18.8% merged).
  The umbrella shipped a 660-line `integration_test.go` in slice 026
  covering CreatePopulation / GetPopulation / DrawSample / GetSample /
  AnnotateSample / ListAnnotations / ListAuditLog under real Postgres
  - RLS, and the sink shipped a 330-line `integration_test.go` in
    slice 126 covering AC-7 (100-record happy path) + AC-8 (10001-record
    backpressure fallback to `audit_sink_failures`); neither was enrolled
    in CI's `tests-integration` job, so the merged audit measured the
    unit-only floor. The unifiedlog package had no in-package integration
    test at all (the slice-124 happy path was covered transitively by
    `internal/api/adminauditlog` which is `excludes`-listed and does not
    roll up). A new `internal/audit/unifiedlog/integration_test.go` drives
    `Query` against real Postgres + RLS for the canonical 8-column Entry
    shape (slice-180 subject_module preserved), tenant-isolation invariant
    (constitutional invariant #6 — running under tenant A's GUC never
    returns tenant B's rows), KindFilter narrowing the UNION-ALL branches,
    ActorFilter exact-match, and empty-range zero-rows. New unit suites
    added: `internal/audit/helpers_test.go` covers the pure-Go predicate
    canonicalization (`isTrivialPredicate` — every match-all shape +
    non-trivial reject; `canonicalPredicate` — nil/empty -> canonical
    `{"op":"true"}` + passthrough), the `pgUUID` / `pgTimestamptz` /
    `nullableInt32` wrappers, and the pre-tx validation guards on
    `CreatePopulation` (empty CreatedBy + inverted time window),
    `DrawSample` (non-positive N + empty Seed + empty CreatedBy), and
    `AnnotateSample` (unknown Result + empty AnnotatedBy + canonical
    result keys pinned). `internal/audit/sink/helpers_test.go` covers
    the `Default()` reader (nil-when-unset + non-nil-after-SetDefault),
    the `Shutdown` ctx-cancel branch (slice 126 graceful-shutdown bound),
    the disabled-sink Emit instant-discard path (1000-Emit timeout
    invariant), and the `New` env-var BufferSize override branches
    (integer + opts-overrides-env). `internal/audit/unifiedlog/
joinkinds_test.go` covers the `joinKinds` CSV serializer (empty +
    single + multiple branches) and pins the `AllKinds` declaration-order
    contract (slice-124 / 180 nine-kind UNION order). All three packages
    enrolled in CI's `tests-integration` job via `-coverpkg=./...`. Three
    new floors added to `cmd/scripts/coverage-thresholds.json` at 70 each
    (`max(0, floor(measured - 2pp))` per slice 069 methodology).
    Constitutional invariants honored — append-only ledger (P0-318-4):
    every test seeds via INSERT only; cleanup uses the admin BYPASSRLS
    migrate role, never the app role; ingestion / evaluation separation
    (canvas invariant #2): no test asserts that evaluation can corrupt
    ledger rows; tenant isolation via RLS (canvas invariant #6): the
    unifiedlog integration test directly pins this. Same playbook as
    slices 290 / 297 / 310 / 315.
