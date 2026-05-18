# Slice 121 — Decisions log

Slice type: JUDGMENT. Per the slice-development workflow (CLAUDE.md "JUDGMENT slices"), this file records the design calls Claude made at build time. The maintainer iterates post-deployment.

## D1 — OTel Go SDK baseline: v1.34.0 + otelpgx v0.10.0

**Decision.** Pin to OpenTelemetry Go SDK v1.34.0 (with `sdk/metric` + `sdk` + `trace` + the four OTLP exporters and the Prometheus exporter v0.65.0 derived from it), `otelpgx` v0.10.0, `contrib/instrumentation/net/http/otelhttp` v0.68.0, `contrib/instrumentation/runtime` v0.68.0.

**Why.** `otelpgx@v0.10.0` requires `go.opentelemetry.io/otel@>=v1.34.0`; pinning OTel to v1.34.0 is the smallest version that satisfies that constraint while keeping the contrib packages aligned (otelhttp v0.59.0 → v0.68.0 are all v1.34-compatible because OTel-Go API is stable across the v1.x line). Using older OTel (v1.32) was tried first to minimize churn but failed dependency resolution. Going to bleeding edge (v1.43) churned grpc to a `-dev` tag, which we explicitly pinned back to v1.81.0 (the version atlas was already on).

**Spillover risk.** A future OTel release may bump the contrib semver to v0.69+; we revisit then.

## D2 — `service.name` vs `otelhttp.NewHandler` operation name

**Decision.** Use two distinct strings:

- The OTel resource attribute `service.name` defaults to `security-atlas` (overridable via `OTEL_SERVICE_NAME`).
- The `otelhttp.NewHandler` operation-name argument is the literal `"atlas-http"`.

**Why.** They are different concepts:

- `service.name` identifies the EMITTING SERVICE (the binary). Dashboards, the trace-to-logs correlation in Grafana, and the receive-side `deploy/observability/dashboards/security-atlas-overview.json` all filter by it. `security-atlas` matches what the docs and the existing dashboard queries assume.
- `otelhttp.NewHandler`'s second arg is a generic span name FALLBACK for requests whose route the chi router can't surface (404s mostly). `"atlas-http"` is a readable default that surfaces on the spans-without-a-route. Most requests will get their span name overridden to the actual route pattern by chi → otelhttp's route-pattern derivation.

The slice doc's AC-3 says `OTEL_SERVICE_NAME defaults to security-atlas` — followed. The AC also mentions `otelhttp.NewHandler(root, "atlas-http")` in the canvas + slice-doc hints — followed too. The distinction matters; do NOT collapse them.

## D3 — Default OTLP protocol: gRPC

**Decision.** When `OTEL_EXPORTER_OTLP_PROTOCOL` is unset, use gRPC (`otlptracegrpc.NewClient` + `otlpmetricgrpc.New`). Setting `OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf` switches to HTTP.

**Why.** The receive-side OTel Collector in `deploy/observability/otel-collector-config.yaml` listens on both gRPC (`:4317`) and HTTP (`:4318`). The OTel spec lists no default; SDK implementations are free to pick. gRPC is lower-overhead per call (single long-lived connection vs HTTP POSTs) and matches what the receive-side dashboard's `endpoint: tempo:4317` exchange does already. Operators who specifically need HTTP for proxy/firewall reasons set the env-var; everyone else gets gRPC.

## D4 — NATS trace propagation: hand-rolled over `nats.Header`

**Decision.** OTel-Go ships no first-party NATS contrib package as of OTel-Go v1.34. The slice doc anticipated this. We hand-rolled:

- `InjectNATSTraceContext(ctx, *nats.Msg)` — writes `traceparent` + `baggage` headers via a thin `natsHeaderCarrier` adapter to the `propagation.TextMapCarrier` interface.
- `ExtractNATSTraceContext(ctx, *nats.Msg) context.Context` — reverse direction.
- `StartNATSPublishSpan` / `StartNATSConsumeSpan` — open spans with the right `SpanKind` + the AC-13 attribute set (`messaging.system=nats`, `messaging.destination=<subject>`, `messaging.message.id=<idempotency-key>`).

~50 lines total; 5 integration tests at `internal/observability/otel/integration_test.go` lock the contract.

**Why not a community package.** Surveying community packages: none has clear maintenance signal at the OTel-Go v1.34 era. The hand-roll is small, correct, and free of supply-chain risk. If/when the OTel community standardizes a NATS contrib, swap in one file.

## D5 — Outermost otelhttp wrap, not per-route

**Decision.** Wrap the assembled chi router with `otelhttp.NewHandler(root, ...)` ONCE at the OUTERMOST layer of `Server.httpHandler()`. Every request — including 401s from the bearer-auth middleware, 403s from the OPA authz middleware, and exempt paths like `/auth/local/login` — gets a span.

**Why.** The alternative is per-route registration via `otelhttp.NewMiddleware` inside chi. That would:

- Skip 401s entirely (the middleware never reaches the route).
- Require touching every `root.Get("/v1/...")` call (there are ~120).
- Make the "every request gets a span" property fragile to add-route mistakes.

The outermost wrap is the standard `otelhttp` pattern for this exact reason. The AC-7 filter then prunes the four high-frequency probe paths so we don't drown out useful spans.

## D6 — Opt-in `/metrics` fallback (default OFF)

**Decision.** The `/metrics` Prometheus-format endpoint is mounted ONLY when `ATLAS_METRICS_FALLBACK_ENABLE=true`. When unset (the default), the route is absent — GET returns 404.

**Why.** P0-A3 makes this load-bearing: the endpoint creates an unauthenticated read surface that exposes aggregate route-level traffic patterns. Operators MUST gate it at the network layer (firewall, reverse-proxy ACL, private subnet) before turning it on. Making it opt-in forces the operator decision; default-on would silently expose the surface on every fresh self-host bundle.

The OTLP push path (via `OTEL_EXPORTER_OTLP_ENDPOINT`) is the primary, safer default. The Prometheus fallback exists for environments that can't reach an OTel Collector — direct Prometheus scrape works.

## D7 — Deterministic sampler default, even when env-vars unset

**Decision.** Even when `OTEL_TRACES_SAMPLER` is unset, we install an explicit `ParentBased(TraceIDRatioBased(0.1))` sampler on the TracerProvider via `WithSampler`. We do NOT fall through to the SDK's `WithFromEnv` default (which is `AlwaysSample` when the env-var is absent).

**Why.** P0-A5 forbids 100% sampling as a default — a traffic spike would DOS the OTel Collector OR the local export queue. Setting the explicit 10% default keeps the binary deterministic without env-var presence. Operators can still crank to 100% for debugging by setting `OTEL_TRACES_SAMPLER=always_on` — though they then need to construct their own TracerProvider via the SDK env-parser route, which atlas does not surface as a knob in this slice (revisit if the need actually surfaces in production).

The choice here is: deterministic-safe default over compose-with-env-parser. The slice prefers the safe default; future contributors who need the env-parser path should add it as a new option in `Options` rather than removing the explicit `WithSampler` call.

## Out of scope (filed if value materializes)

- OTel logs as a third pipeline (P0-A11 — slog→Promtail→Loki keeps working).
- Tail sampling at the OTel Collector (not in scope for this slice; can be added without atlas changes).
- Custom business metrics beyond the auto-emitted HTTP/DB/runtime (those land in the slice that introduces the business need).
- Tracing the bootstrap container (one-shot script; doesn't justify the wiring overhead).
- Tracing inside the web container (frontend OTel is a separate slice).
- Multi-backend exporters (multi-fan-out happens at the OTel Collector, not in atlas).

## Spillovers filed

None. Every phase landed in this PR.
