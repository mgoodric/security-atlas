- **test(coverage):** slice 282 — `internal/eval` coverage lift from
  67.2% to 75.9% merged (unit + integration). Slice 279's partial lift
  landed `internal/eval` at 67.2% / floor 65, short of the 70%
  aspirational target — the remaining surface was `consumer.go` (the
  slice 015 JetStream subscriber + the scheduler) which the audit
  flagged as "needs embedded NATS pattern or refactor". This slice
  closes the gap with a package-internal seam (a one-method
  `controlEvaluator` interface + an unexported `evaluatorFactory`
  bridge) that lets unit tests stub the engine without standing up
  Postgres + scope.Store. Public API is unchanged: `NewIngestSubscriber`
  still accepts the public `engineFactory` (`func() *Engine`); the
  constructor wraps it in a closure that returns the narrower
  `controlEvaluator` for `handle()` to call. New
  `internal/eval/consumer_test.go` covers every branch of
  `IngestSubscriber.handle` — bad proto (Term), bad/missing tenant
  header (Term), non-UUID control_id (Ack; scheduled sweep picks up
  anchor-ref controls), happy path (Ack + engine called once with
  TriggerIngest + FarFuture), `ErrControlNotFound` wrapped sentinel
  (Ack, not Nak — not retryable), transient engine error
  (`NakWithDelay(2s)` — broker redelivers), and Ack-returns-error on
  the happy path (logged, does not panic) — plus
  `NewIngestSubscriber`'s nil-logger fallback and the
  engineFactory→evaluatorFactory bridge contract (one engine per
  invocation), and the trivial `discardWriter.Write`. The
  `internal/eval` floor in `cmd/scripts/coverage-thresholds.json`
  ratchets from 65 → 73 per slice 069's `max(0, floor(measured - 2pp))`
  policy. `Scheduler.Run` / `Scheduler.SweepOnce` /
  `IngestSubscriber.Start` remain integration-only (real Postgres +
  real JetStream). Spillover from slice 279's coverage audit
  (`docs/coverage-audit-2026-05.md`).
