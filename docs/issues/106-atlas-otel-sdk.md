# 106 — Add OTel SDK to atlas (traces + metrics + Go runtime telemetry)

**Cluster:** Infra / observability
**Estimate:** 2.5d
**Type:** AFK

## Narrative

Atlas emits structured logs via stdlib `slog` (TextHandler → stderr → docker logs → Promtail → Loki). That path works end-to-end today. What atlas does NOT emit: OpenTelemetry traces, runtime metrics, or any Prometheus-format `/metrics` endpoint. `grep -rE "go.opentelemetry|prometheus/client_golang" --include='*.go'` returns zero hits in atlas code (the lone match is in `connectors/osquery/internal/idem/idem.go`, which is a false positive on the substring "tempo" — not actual OTel).

The companion deploy bundle (`feat(observability)`, PR #234 / `deploy/observability/`) ships the **receive** side — OTel Collector at `otel-collector:4317`, Prometheus pulling at `:9090`, Tempo storing traces at `:3200`, all on the `personal-ai` Docker network and verified working via curl smoke tests + Prometheus target health. The Grafana starter dashboard (`deploy/observability/dashboards/security-atlas-overview.json`) has Metrics + Traces sections that stay empty until atlas restarts with `OTEL_EXPORTER_OTLP_ENDPOINT` set.

This slice closes that loop. It wires the OTel SDK into `cmd/atlas/main.go` startup, instruments the HTTP server (request spans, latency histograms, error counts) via `otelhttp`, instruments the pgx pool via `otelpgx` (every SQL query becomes a span + the standard `db_client_*` metrics), instruments the NATS publisher/subscriber so evidence-ingest gets traced end-to-end (atlas-side span → NATS stream context → atlas-side consumer span), exposes Go runtime metrics (GC, goroutines, heap, threads) via the OTel runtime instrumentation package, and adds a fallback `/metrics` Prometheus scrape endpoint (for when the OTel pipeline is down — direct-scrape from Prometheus stays viable).

### Scope discipline

In scope:

- OTel SDK initialization (provider + exporters + propagators)
- HTTP server instrumentation (every route gets a span; request/response metadata on attributes)
- DB instrumentation (pgx pool tracer)
- NATS instrumentation (evidence-ingest path; both publisher and consumer)
- Go runtime metrics (the standard `runtime/metrics` exposed via OTel meter)
- Fallback `/metrics` Prometheus endpoint, auth-exempted (same pattern as slice 092 for `/api/version`)
- All env-var configurable via OTel-standard names (`OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME`, `OTEL_RESOURCE_ATTRIBUTES`, `OTEL_TRACES_SAMPLER`, etc.); ZERO Go code changes needed to point at a different backend
- The fallback `/metrics` endpoint is opt-in via `ATLAS_METRICS_FALLBACK_ENABLE=true` (default off — turning it on adds an unauthenticated read surface and operators should opt in deliberately)

Out of scope (file separately if value materializes):

- Re-architecting the existing `slog` path (logs already work; OTel logs as a third pipeline is duplicative without a clear win)
- Custom business metrics beyond auto-emitted HTTP/DB/runtime (those land in the slice that introduces the business need)
- Tracing the bootstrap container (one-shot script; doesn't justify the wiring overhead)
- Tracing inside the web container (frontend OTel is a separate slice with its own design questions)
- Multi-backend exporters (the OTel-standard env-var path covers any single endpoint; multi-fan-out happens at the OTel Collector, not in atlas)

## Threat model

Per the IdeaToSlice convention, STRIDE analysis at design time:

### S — Spoofing

- **New unauthenticated endpoints**: `/metrics` (when enabled via `ATLAS_METRICS_FALLBACK_ENABLE=true`). Mitigation: explicit env opt-in (default off); add to the auth-middleware exemption list (same surface as `/api/version` from slice 092). Document in `README.md` that the operator MUST gate this endpoint at the network layer (firewall, reverse-proxy ACL, private subnet) if exposed beyond localhost. Anti-criterion: `/metrics` is NOT enabled by default.
- **OTLP endpoint env-var spoofing**: a malicious operator could point `OTEL_EXPORTER_OTLP_ENDPOINT` at an attacker-controlled URL to capture span data. Mitigation: operator-controlled env var is in scope by the threat model (operator is trusted); document that the endpoint should be private-network only and that TLS (`OTEL_EXPORTER_OTLP_INSECURE=false`) is required for cross-host destinations.

### T — Tampering

- **Span attribute injection from user input**: HTTP request paths, query parameters, body content become span attributes via `otelhttp`. Mitigation: `otelhttp` default is to record only structured metadata (method, route pattern, status code) — NOT request body or query strings. The implementing agent MUST confirm this default and explicitly NOT enable `WithPublicEndpoint` or any setting that records raw URL.
- **OTLP receive endpoint trust**: atlas only emits, never receives OTLP. Out of scope.

### R — Repudiation

- Traces ARE an audit-trail-like signal. Operators may want to use them for incident reconstruction. But they're not a SECURITY audit trail (which is what `decision_audit_log` is for, slice 035). Mitigation: in the implementing-agent notes, explicitly distinguish "operational traces for performance debugging" from "decision audit log for tenant security investigations" — they live in different storage with different retention, RLS, and access patterns. Anti-criterion: traces do NOT replace or supplement `decision_audit_log`.

### I — Information disclosure

- **Bearer tokens / session cookies in span attributes**: HTTP request headers are NOT recorded by `otelhttp` default, but a careless future contributor could enable a header-recording option. Mitigation: explicit anti-criterion below; integration test asserts that a span captured from a `Authorization: Bearer ...` request does NOT contain the token value in any attribute.
- **SQL query parameters in DB spans**: `otelpgx` exposes the SQL query text as a span attribute. Parameters are by default rendered as `$1, $2, $3` placeholders (NOT inline values), but the implementing agent MUST verify the default and confirm parameterized queries don't leak literals. If they do, configure `otelpgx.WithIncludeQueryParameters(false)` (or equivalent) explicitly.
- **PII in business-data span attributes**: any custom span attribute the implementing agent adds (`tenant_id`, `user_email`, `evidence_id`, etc.) must be reviewed. Mitigation: stick to opaque IDs (UUIDs, hash digests), NEVER email addresses or human-readable names, in span attributes. `tenant_id` UUID is fine; tenant display name is not.
- **NATS message bodies in span attributes**: evidence-ingest NATS messages can carry tenant data. Mitigation: the NATS instrumentation should record message metadata (subject, stream, sequence) NEVER message body.

### D — Denial of service

- **Unbounded trace volume**: at 100% sampling, every HTTP request generates a trace. A traffic spike could DOS the OTel Collector OR the local export queue (memory growth → atlas OOM). Mitigation: default to a parent-based + 10% root sampler (`OTEL_TRACES_SAMPLER=parentbased_traceidratio,OTEL_TRACES_SAMPLER_ARG=0.1`). Operators can crank to 100% for debugging; the default protects against accidental DOS. Anti-criterion: tail sampling at OTel Collector is OUT of scope for this slice (separate decision; can be added without atlas changes).
- **Slow exporter blocking request path**: if the OTel Collector is down, atlas's exporter could block waiting for an ACK and stall the request handler. Mitigation: the OTel SDK's batch span processor defaults to async + bounded queue; the implementing agent MUST verify it's NOT using the synchronous simple span processor.
- **Cardinality explosion in Prometheus**: every unique combination of label values becomes a metric series; `tenant_id` as a label would create one series per tenant per route per status code (multiplicative blowup). Mitigation: `tenant_id` MAY be a trace attribute (Tempo handles high cardinality fine) but MUST NOT be a Prometheus metric label. Anti-criterion below.

### E — Elevation of privilege

- **`/metrics` endpoint bypassing auth middleware**: required for Prometheus to scrape unauthenticated, but creates a read surface for atlas-internal data (route names, request counts, latencies — potentially business-sensitive information about tenant activity patterns). Mitigation: the endpoint is opt-in only (`ATLAS_METRICS_FALLBACK_ENABLE=true`); when enabled, doc requires operator to gate at network layer. The metric labels are scrubbed of tenant identity (no `tenant_id` label per the D mitigation above), so the disclosure surface is limited to aggregate route-level traffic patterns.
- **OTel SDK doesn't open new network listeners**: it only originates outbound to the OTLP endpoint. No new auth-boundary surface from the SDK itself (separate from the `/metrics` opt-in above).

### Threat-model anti-criteria added to slice's P0 list

See P0-A7 through P0-A12 below.

## Acceptance criteria

### OTel SDK initialization

- [ ] AC-1: `cmd/atlas/main.go` initializes the OTel SDK (TracerProvider + MeterProvider + propagators: tracecontext + baggage) after the pgx pool is ready, BEFORE the HTTP/gRPC listeners start
- [ ] AC-2: SDK init is a no-op when `OTEL_EXPORTER_OTLP_ENDPOINT` is unset (the env var is the on/off switch). atlas continues to serve requests; traces + metrics are simply not exported. This is the "single-binary, runs without OTel infra" property — must be preserved.
- [ ] AC-3: SDK reads OTel-standard env vars exclusively (`OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_PROTOCOL`, `OTEL_SERVICE_NAME` defaults to `security-atlas`, `OTEL_RESOURCE_ATTRIBUTES`, `OTEL_TRACES_SAMPLER` defaults to `parentbased_traceidratio`, `OTEL_TRACES_SAMPLER_ARG` defaults to `0.1`). No custom `ATLAS_OTEL_*` env vars unless documented in CHANGELOG with a migration path.
- [ ] AC-4: Atlas startup log includes a single one-liner confirming whether OTel is enabled: `atlas: opentelemetry: enabled (endpoint=otel-collector:4317, sampler=parentbased_traceidratio@0.1)` OR `atlas: opentelemetry: disabled (OTEL_EXPORTER_OTLP_ENDPOINT not set)`. Future operators grepping for "opentelemetry" in atlas logs can confirm state in one query.

### HTTP server instrumentation

- [ ] AC-5: The root chi handler is wrapped with `otelhttp.NewHandler(root, "atlas-http")` so every HTTP request generates a span with route + method + status code as attributes
- [ ] AC-6: Span attributes for HTTP requests include ONLY: `http.method`, `http.route`, `http.status_code`, `http.target` (with query string stripped), `net.peer.ip`. They do NOT include: request body, response body, `Authorization` header, `Cookie` header, full URL with query string.
- [ ] AC-7: The `otelhttp` filter excludes `/health` and `/metrics` and `/api/version` from span generation (these are high-frequency probes; tracing them is noise without signal)

### DB instrumentation

- [ ] AC-8: The pgx pool is configured with `otelpgx.NewTracer()` (or equivalent) so every SQL query generates a child span under the request span
- [ ] AC-9: SQL query attributes include `db.system=postgresql`, `db.operation` (SELECT / INSERT / UPDATE / DELETE), `db.statement` (parameterized form with `$N` placeholders, NEVER literals), and `db.connection_string` is explicitly REDACTED (must NOT contain the password)
- [ ] AC-10: `otelpgx` query parameter recording is explicitly disabled (`WithIncludeQueryParameters(false)` or default-off). Integration test asserts a span captured from an `INSERT INTO foo VALUES ($1)` does NOT contain the literal value of `$1` in any attribute.

### NATS instrumentation

- [ ] AC-11: The NATS publisher (slice 015's evidence-ingest path) wraps `Publish` calls with an OTel span; trace context is injected into the NATS message header (`traceparent` per W3C spec) so the downstream consumer can continue the trace
- [ ] AC-12: The NATS consumer extracts the trace context from message headers, creates a child span linking the consumer-side handler to the producer-side span. Evidence-ingest becomes a single multi-span trace spanning HTTP → publish → consumer → DB-write.
- [ ] AC-13: NATS span attributes include `messaging.system=nats`, `messaging.destination` (stream name), `messaging.message.id` (sequence). They do NOT include the message body.

### Go runtime metrics

- [ ] AC-14: `go.opentelemetry.io/contrib/instrumentation/runtime` is wired so atlas auto-emits the standard Go runtime metrics: `runtime.go.goroutines`, `runtime.go.gc.pause_total_ns`, `runtime.go.mem.heap_alloc`, `runtime.go.mem.heap_inuse`, `runtime.go.mem.lookups`, `runtime.go.cgo.calls`, `runtime.uptime`. Default collection interval 15s.

### Fallback /metrics endpoint

- [ ] AC-15: When `ATLAS_METRICS_FALLBACK_ENABLE=true`, atlas exposes a Prometheus-format scrape endpoint at `GET /metrics`. The endpoint returns all metrics the OTel SDK would otherwise push via OTLP. When the env is unset/false (the default), `GET /metrics` returns 404.
- [ ] AC-16: `/metrics` is added to the auth-middleware exemption list (same pattern as `/api/version` from slice 092 AC-5). Verified by `curl http://atlas:8080/metrics` returning 200 without a bearer token when enabled, 404 when disabled.
- [ ] AC-17: README + the `.env.example` template document the security implication of `ATLAS_METRICS_FALLBACK_ENABLE=true` (unauthenticated read surface; must be gated at the network layer).

### Tests

- [ ] AC-18: Integration test for the auth-exemption: `curl /metrics` returns 404 when env unset, 200 when env=true, AND `curl /v1/anchors` continues to return 401 without auth (the exemption is scoped to exactly `/metrics`, not broadened).
- [ ] AC-19: Integration test for the security anti-criteria: capture a span from an authenticated request, assert no span attribute contains the bearer-token value, no SQL span attribute contains a parameterized-query literal, no NATS span attribute contains a message body.
- [ ] AC-20: Integration test for the env-var on/off switch: with `OTEL_EXPORTER_OTLP_ENDPOINT` unset, atlas starts, serves traffic, and emits NO outbound OTLP. With it set to a test collector, atlas emits spans + metrics that the test collector receives.
- [ ] AC-21: Integration test for the trace context propagation: produce a NATS evidence message from atlas, capture the spans, assert the consumer-side span is linked to the producer-side span (same trace ID, parent-child relationship).

### Documentation

- [ ] AC-22: `deploy/docker/.env.example` adds `OTEL_EXPORTER_OTLP_ENDPOINT=` (commented placeholder) and `ATLAS_METRICS_FALLBACK_ENABLE=false` (commented documentation)
- [ ] AC-23: `deploy/docker/docker-compose.yml` adds `OTEL_EXPORTER_OTLP_ENDPOINT=${OTEL_EXPORTER_OTLP_ENDPOINT:-}` to the atlas service environment (no-op when env unset)
- [ ] AC-24: `deploy/observability/README.md` is updated to confirm Phase B + C are both done and the trace-to-log correlation now works end-to-end. Includes a `curl + sleep + Grafana-query` smoke procedure operators can run post-deploy.

## Constitutional invariants honored

- **CLAUDE.md "no PII in logs/metrics":** AC-6, AC-9, AC-10, AC-13, and the cardinality anti-criterion below explicitly enforce this for the new telemetry surfaces.
- **Invariant 6 (RLS at DB layer):** Not directly relevant — OTel doesn't bypass RLS. But AC-9's `db.connection_string` redaction prevents accidental DSN leakage that could include a role's password.
- **Slice 092 (auth-middleware exemption pattern):** AC-16 follows the exact pattern slice 092 established for `/api/version` — exact-path match, not prefix glob.

## Canvas references

- `Plans/canvas/09-tech-stack.md` (verify observability is in the v1 tech-stack scope — if explicitly deferred, this slice should explain why it's reaching forward)
- `cmd/atlas/main.go` (the file this slice modifies most heavily — SDK init lands near the existing pgx pool setup)
- `internal/api/httpserver.go` (the chi router this slice wraps with otelhttp)
- `internal/evidence/streambuf/` (NATS publish/subscribe — this slice instruments)
- PR #234 / `deploy/observability/` (the receive-side backplane this slice's emit-side completes)
- Slice 092 (`/api/version` middleware exemption — exact pattern for AC-16)

## Dependencies

- #015 (NATS JetStream ingest stage; merged) — provides the NATS path that AC-11/12/13 instrument
- #034 (auth + sessions; merged) — provides the auth middleware that AC-16 adds `/metrics` to the exemption list of
- #037 (self-host bundle; merged) — provides the deploy-compose this slice's env vars plug into
- #065 (self-host bundle P0 fixes; merged) — established the auth-writer-tenant-context pattern that AC-9 mirrors for db spans
- #092 (version display fix; pending merge) — provides the auth-exemption pattern AC-16 follows
- PR #234 / `feat(observability)` (pending merge) — provides the OTLP receiver this slice exports to

## Anti-criteria (P0 — block merge)

- **P0-A1:** Does NOT replace or supplement the existing `slog` logging path. Logs stay on stdlib slog → stderr → Promtail → Loki. The OTel logs pipeline is explicitly out of scope; revisit only if there's a documented win.
- **P0-A2:** Does NOT introduce a custom `ATLAS_OTEL_*` env-var namespace. Reuses the OTel-standard `OTEL_*` env vars exclusively. Operators familiar with OTel anywhere can configure atlas without reading atlas-specific docs.
- **P0-A3:** Does NOT enable `/metrics` by default. The env var `ATLAS_METRICS_FALLBACK_ENABLE` defaults to false. Turning it on adds an unauthenticated read surface that requires operator network-layer gating.
- **P0-A4:** Does NOT use the synchronous simple span processor. Bounded async batch only. A slow exporter must never block the request path.
- **P0-A5:** Does NOT default to 100% trace sampling. Default is `parentbased_traceidratio` at 10%. Operators can crank for debugging via env vars.
- **P0-A6:** Does NOT change any existing audit-log semantics. The `decision_audit_log` table is the source of truth for security audit; OTel traces are operational telemetry, distinct surface.
- **P0-A7 (security):** Does NOT record `Authorization` headers, `Cookie` headers, or any header containing the substrings `token`, `secret`, `password`, `key` as span attributes. Integration test AC-19 enforces.
- **P0-A8 (security):** Does NOT record raw request body, response body, NATS message body, or full URL with query string as span attributes. Same test enforces.
- **P0-A9 (security):** Does NOT inline-render SQL query parameter values into `db.statement` span attribute. Placeholders only.
- **P0-A10 (security):** Does NOT add `tenant_id`, `user_id`, `user_email`, or any other identity-bearing label to Prometheus metrics. These belong on traces (where Tempo's cardinality model handles them) or in logs (where Loki indexes them as labels separately), NOT on Prometheus metrics where they create a cardinality explosion.
- **P0-A11 (security):** Does NOT emit OTel logs (the third pipeline) in v1. The existing slog path is the only logging surface. Adding a duplicate path widens the disclosure surface for the same data.
- **P0-A12 (security):** Does NOT change the `.env.example` semantics that the env var on/off pattern uses. `OTEL_EXPORTER_OTLP_ENDPOINT` unset = OTel off; set = OTel on. No third state.
- **P0-A13:** Does NOT use vendor-prefixed test fixture tokens in any new test — neutral `test-*` only.

## Skill mix (3–5)

- OpenTelemetry Go SDK (`go.opentelemetry.io/otel`, `otelhttp`, `otelpgx`, `runtime` contrib package, OTLP gRPC exporter)
- pgx tracer composition (the pool needs a `tracer` option; verify it doesn't conflict with the existing query-tracing in the pool config)
- chi middleware ordering (the otelhttp wrap goes at the OUTERMOST layer — before auth, before tenancy — so every request including 401s gets a span)
- Integration testing against a real OTel Collector test fixture (use `testcontainers-go` to spin up `otel/opentelemetry-collector-contrib:0.113.0` in-test)
- Security-conscious attribute filtering (every place a span attribute could leak data — and there are many — needs an explicit anti-criterion or test guard)

## Notes for the implementing agent

- The OTel SDK initialization is the single trickiest part — there are ~6 separate `Provider` constructors (tracer, meter, propagator, sampler, resource, exporter) that need composition. Recommend modeling this on a known-good reference like `github.com/open-telemetry/opentelemetry-go/example/otel-collector/main.go` and adapting to atlas's startup flow. Resist the temptation to invent novel init patterns.
- The fallback `/metrics` endpoint (AC-15) uses `otelprom.New(...)` to wire the OTel meter to a Prometheus `Registry`, then exposes that registry via `promhttp.HandlerFor`. The Prometheus exporter and the OTLP push exporter run SIMULTANEOUSLY (both pulling from the same meter); this is supported and is the standard pattern for "OTLP-primary, Prometheus-scrape-fallback" setups.
- For the NATS instrumentation, OTel doesn't ship a first-party NATS contrib package as of OTel-Go 1.32. You'll need to either (a) use a community package like `github.com/nats-io/nats.go/middleware/otel` (verify it exists at slice-write time; this may have changed), or (b) hand-roll the trace-context propagation via `nats.Header` for `traceparent` injection/extraction. The hand-roll is ~30 lines + 2 integration tests.
- AC-7's filter (exclude `/health`, `/metrics`, `/api/version` from spans) is implemented via `otelhttp.WithFilter()` — pass a func that returns false for those paths.
- AC-19's "no bearer token in spans" test is the most important guard. Implement it as a real test that boots atlas with an OTel test collector, fires a request with `Authorization: Bearer secret-token-test-12345`, dumps all received spans, asserts the literal string "secret-token-test-12345" appears NOWHERE.
- Slice surfaced 2026-05-16 from the observability rollout session. Companion to PR #234 (the receive-side deploy bundle). Once both merge and atlas restarts with `OTEL_EXPORTER_OTLP_ENDPOINT` in `.env`, the trace-to-log correlation in Grafana lights up end-to-end and the Metrics/Traces sections of the starter dashboard at `deploy/observability/dashboards/security-atlas-overview.json` populate.
