- **test(coverage):** slice 295 — `internal/metrics/scheduler` coverage
  lift from 0.0% to 78.4% merged. The package shipped in slice 076 as the
  15-minute metrics-evaluator cron but was never unit-tested. This slice
  adds two test files: a pure-Go `worker_test.go` covering `New` (nil-
  logger fallback + retain-custom-logger + field assignment),
  `encodeDimensions` (sorted-JSON shape, empty map, single entry,
  determinism), `discardWriter.Write` (every byte-slice variant
  returns len and nil err), `Run` (cancelled context, zero interval,
  negative interval — all routed through the
  `interval <= 0` fallback and the `ctx.Done()` arm), `SweepReport`
  zero-value + field assignment, and `SweepOnce`'s list-tenants-error
  wrapper (via a defunct pool pointed at `127.0.0.1:1`). And a
  `//go:build integration` `integration_test.go` covering
  `SweepOnce`'s full success path (per-tenant transaction lifecycle,
  ApplyTenant, the evaluator try/log/continue loop, and the RLS-bound
  `InsertMetricObservation`), the append-only ledger contract (two
  sweeps double the observation count for our tenant — `slice 076`
  schema has SELECT + INSERT policies only), and `Run`'s inline-sweep
  - ctx.Done path against a real DB. The scheduler package is
    enrolled in CI's integration test list (slice 287/284/297/283/290
    pattern). Per-function coverage moves `New` 0% → 100%, `Run`
    0% → 92.9%, `SweepOnce` 0% → 72.2%, `sweepTenant` 0% → 73.5%,
    `encodeDimensions` 0% → 75%, `discardWriter.Write` 0% → 100%.
    Floor in `cmd/scripts/coverage-thresholds.json` ratchets from `0`
    to `76` per slice 069's `floor(measured - 2pp)` methodology.
    Spillover from slice 279's coverage audit
    (`docs/coverage-audit-2026-05.md`).
