- **test(coverage):** slice 286 — `internal/observability/otel`
  coverage lifted from 15.8% (slice 279 audit baseline) to 94.2%
  unit-only via a new `coverage_test.go` that exercises every
  load-bearing branch the slice 121 init wiring exposes:
  Init's enabled-path (HTTP protocol; with + without Prometheus
  fallback), the unsupported-protocol error branch,
  `buildResource` (default + Options-attribute paths),
  `buildTraceExporter` + `buildMetricExporter` (grpc / http /
  unsupported), `samplerFromEnv` (env-unset + env-set),
  `newShutdown` (first-call + idempotent second-call),
  all nine NATS-helper functions in `nats.go`
  (`natsHeaderCarrier` Get/Set/Keys, Inject/Extract context,
  Start{Publish,Consume}Span, natsAttrs +/- messageID,
  EndNATSSpanWithError nil-span / no-error / real-error),
  `NewTracedPool` (parse-error + lazy-dial success branch via
  pgxpool's background-goroutine connection model), and
  `StartRuntimeMetrics`. All tests are pure Go — no Postgres,
  no NATS broker, no OTel collector — and use `tracetest.InMemoryExporter`
  to capture spans for assertions. Global OTel state (tracer /
  meter / propagator) is saved + restored per-test via a
  `withSavedGlobals(t)` helper so each case is isolated. The
  `internal/observability/otel` floor in
  `cmd/scripts/coverage-thresholds.json` ratchets from 13 → 92
  per slice 069's `max(0, floor(measured - 2pp))` policy.
