# security-atlas observability backplane

OTel Collector + Prometheus + Tempo + Grafana data-source provisioning + a starter dashboard. Sits alongside the existing Loki + Promtail + Grafana stack (which handles logs end-to-end today). Brings up metrics + traces once the atlas-OTel SDK slice ships.

## Three signals, three statuses

| Signal      | Status                                                                                                                                                                                       | Receives via                                                           |
| ----------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------- |
| **Logs**    | Working today (Promtail → Loki → Grafana). Atlas's `slog` TextHandler → stderr → docker logs is auto-discovered by Promtail's `docker_sd_configs` and shipped to Loki at `GrafanaLoki:3100`. | Existing Loki                                                          |
| **Metrics** | Working — slice 121 wires the atlas OTel SDK (HTTP / DB / NATS / Go runtime). Set `OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4317` in the atlas `.env` and restart to enable.        | OTel Collector `:4317` → Prometheus exporter `:8889` → Prometheus pull |
| **Traces**  | Working — same slice 121 wires producer + consumer spans for the HTTP → NATS → DB hot path with W3C trace-context propagation end-to-end.                                                    | OTel Collector `:4317` → Tempo OTLP `:4317`                            |

## Bundle contents

| File                                      | Purpose                                                                                                                                                                                                                               |
| ----------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `docker-compose.yml`                      | Three services — `otel-collector`, `prometheus`, `tempo`. Joins existing `personal-ai` network.                                                                                                                                       |
| `otel-collector-config.yaml`              | OTLP receiver → batch+memory-limiter+resource processors → `otlp/tempo` (traces) + `prometheus` (metrics). Logs pipeline intentionally absent.                                                                                        |
| `prometheus.yml`                          | Single-node config. Scrapes the collector's metrics exporter at `otel-collector:8889`, its own `:9090`, and the collector's internal telemetry at `:8888`. 30-day retention.                                                          |
| `tempo.yaml`                              | Single-node Tempo with local filesystem backend, 7-day retention, OTLP receiver. metrics-generator enabled for service-graph + span-metrics (Tempo can synthesize RED metrics from traces, useful before the atlas-OTel slice lands). |
| `grafana-datasources.yaml`                | Provisioning file for Grafana — three data sources (Prometheus, Tempo, Loki) with trace-to-logs + trace-to-metrics + exemplar correlation wired.                                                                                      |
| `dashboards/security-atlas-overview.json` | Starter dashboard. Three sections — Logs (works today via Loki), Metrics (lights up post atlas-OTel slice), Traces (same). Importable via Grafana UI → Dashboards → Import.                                                           |

## Deploy on Unraid

```bash
# 1. Stage files
SSH_AUTH_SOCK="" ssh -i /tmp/unraid_key2 -o IdentitiesOnly=yes -o "IdentityAgent none" \
    root@192.168.1.246 "mkdir -p /mnt/user/appdata/observability/dashboards"

scp -i /tmp/unraid_key2 -o IdentitiesOnly=yes -o "IdentityAgent none" \
    deploy/observability/docker-compose.yml \
    deploy/observability/otel-collector-config.yaml \
    deploy/observability/prometheus.yml \
    deploy/observability/tempo.yaml \
    root@192.168.1.246:/mnt/user/appdata/observability/

scp -i /tmp/unraid_key2 -o IdentitiesOnly=yes -o "IdentityAgent none" \
    deploy/observability/dashboards/security-atlas-overview.json \
    root@192.168.1.246:/mnt/user/appdata/observability/dashboards/

# 2. Bring up the stack
SSH_AUTH_SOCK="" ssh -i /tmp/unraid_key2 -o IdentitiesOnly=yes -o "IdentityAgent none" \
    root@192.168.1.246 \
    "cd /mnt/user/appdata/observability && docker compose up -d"

# 3. Wire Grafana data sources (one-time setup)
scp -i /tmp/unraid_key2 -o IdentitiesOnly=yes -o "IdentityAgent none" \
    deploy/observability/grafana-datasources.yaml \
    root@192.168.1.246:/mnt/user/appdata/Grafana/provisioning/datasources/security-atlas.yaml

SSH_AUTH_SOCK="" ssh -i /tmp/unraid_key2 -o IdentitiesOnly=yes -o "IdentityAgent none" \
    root@192.168.1.246 "docker restart Grafana"

# 4. Verify
curl -s http://192.168.1.246:13133/ | head -1     # OTel Collector health
curl -s http://192.168.1.246:9090/-/ready         # Prometheus
curl -s http://192.168.1.246:3200/ready           # Tempo

# 5. Import the dashboard (Grafana UI):
#    Dashboards → New → Import → paste contents of
#    dashboards/security-atlas-overview.json
```

## Verify logs end-to-end (Phase A — works today)

Once the bundle is up (or even right now, without it), the logs side is fully functional via the existing Loki + Promtail stack. In Grafana → Explore → Loki:

```logql
# All atlas-related log volume by container
sum by (container) (rate({container=~"security-atlas-.*"}[5m]))

# Errors across the stack
{container=~"security-atlas-.*"} |~ "level=ERROR|error:|panic:"

# Atlas startup sequence (NATS, pgx pool, listeners)
{container="security-atlas-atlas"} |~ "atlas: "

# Bootstrap success markers (one-shot completions)
{container="security-atlas-bootstrap"} |~ "bootstrap complete"

# Authz audit denials (slice 035 + 065 audit-writer)
{container="security-atlas-atlas"} |~ "audit log write failed|forbidden|decision_audit"

# Per-tenant slicing (when atlas's structured logs include tenant=)
{container="security-atlas-atlas"} | logfmt | tenant!=""
```

These work against the current atlas binary (which emits `slog.TextHandler` formatted output) and the existing Promtail+Loki path. No atlas changes needed.

## Verify metrics + traces (Phase B — needs atlas-OTel slice)

After the atlas-OTel slice merges and atlas restarts with `OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4317` set in `/mnt/user/appdata/security-atlas/.env`:

```bash
# 1. Generate some traffic
TOKEN=$(grep ATLAS_BOOTSTRAP_TOKEN /mnt/user/appdata/security-atlas/.env | cut -d= -f2)
for i in $(seq 1 20); do
    curl -s -H "Authorization: Bearer $TOKEN" http://192.168.1.246:8087/v1/anchors > /dev/null
    sleep 0.5
done

# 2. Verify metrics in Prometheus
curl -s 'http://192.168.1.246:9090/api/v1/query?query=atlas_http_server_duration_count' | jq

# 3. Verify traces in Tempo
curl -s 'http://192.168.1.246:3200/api/search?tags=service.name%3Dsecurity-atlas&limit=5' | jq

# 4. Verify trace-to-logs correlation in Grafana UI:
#    Explore → Tempo → search for service.name=security-atlas →
#    click any trace → expand a span → "Logs for this span" should
#    surface the corresponding atlas log line from Loki, joined by trace_id.
```

## Operational notes

### Why no Watchtower labels?

All three services are stateful — Prometheus has TSDB files, Tempo has WAL + blocks, OTel Collector buffers spans in memory. A blind Watchtower upgrade can hit a config-format breaking change (OTel Collector minor versions have done this) and silently stop receiving telemetry. **Upgrade manually after reading release notes.**

### Why is Tempo running as user `0:0`?

Tempo's image's default user often hits `EACCES` writing to `/var/tempo` on first boot when the volume is fresh. Running as root fixes this for the homelab. For a production deployment, pre-create the volume with correct ownership and drop the `user:` override.

### Memory expectations

Bound at roughly:

- OTel Collector: 512 MB hard limit (`memory_limiter.limit_mib`)
- Prometheus: ~500 MB-1 GB depending on cardinality (single-tenant homelab → low end)
- Tempo: ~300-600 MB depending on trace volume

Total ~1.5-2 GB of additional RAM committed for the backplane. Unraid hosts with 16 GB+ should have no issue.

### High-cardinality watchout

Atlas's OTel SDK should NOT attach `tenant_id` as an unbounded label on traces or metrics. Per-tenant labels work for low-tenant-count homelabs but explode for multi-tenant SaaS. Use `tenant_id` as a span attribute (high cardinality is fine in trace storage) but NOT as a Prometheus metric label.

### Trace sampling

In production, never sample at 100%. Sane defaults: parent-based + 5-10% root sampling. The atlas-OTel slice should expose `OTEL_TRACES_SAMPLER` and `OTEL_TRACES_SAMPLER_ARG` env vars so this is tunable without code changes.

## Phase B + C — done (slice 121)

Slice 121 wired the atlas OTel SDK end-to-end. With this bundle deployed AND `OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4317` set in `/mnt/user/appdata/security-atlas/.env`, the trace-to-log correlation in Grafana lights up end-to-end and the Metrics / Traces sections of `dashboards/security-atlas-overview.json` populate.

What's wired on the atlas side:

- `cmd/atlas/main.go` initializes the OTel SDK BEFORE the pgx pool comes up. When `OTEL_EXPORTER_OTLP_ENDPOINT` is unset, init is a NO-OP — atlas serves traffic with zero telemetry overhead.
- HTTP server: `otelhttp.NewHandler(root, "atlas-http")` at the outermost middleware layer; `/health`, `/metrics`, `/v1/version`, `/v1/install-state` excluded from spans (high-frequency probes).
- DB pool: every `pgxpool.New` call goes through `atlasotel.NewTracedPool`, which attaches the `otelpgx` tracer. Every SQL query becomes a child span under the request span; `db.statement` carries the parameterized form (`$N` placeholders, never inline values).
- NATS: producer-side span on each `Publish`, W3C `traceparent` header injected into the message; consumer extracts it and starts a linked child span. Evidence ingest becomes one trace from HTTP push → publisher span → consumer span → DB write.
- Go runtime metrics (`runtime.go.goroutines`, `runtime.go.gc.*`, `runtime.go.mem.*`, etc.) auto-emitted at 15s cadence.
- Opt-in `/metrics` Prometheus scrape endpoint via `ATLAS_METRICS_FALLBACK_ENABLE=true`. OFF by default — when on, operators MUST gate at the network layer (it's an unauthenticated read surface).

### Post-deploy smoke procedure

After atlas restarts with `OTEL_EXPORTER_OTLP_ENDPOINT` set, run:

```bash
# 1. Look for the startup line confirming OTel is enabled.
docker logs security-atlas-atlas 2>&1 | grep "opentelemetry:"
# Expected: atlas: opentelemetry: enabled endpoint=http://otel-collector:4317 sampler=parentbased_traceidratio sampler_arg=0.1 ...

# 2. Drive traffic.
TOKEN=$(grep ATLAS_BOOTSTRAP_TOKEN /mnt/user/appdata/security-atlas/.env | cut -d= -f2)
for i in $(seq 1 20); do
    curl -s -H "Authorization: Bearer $TOKEN" http://192.168.1.246:8087/v1/anchors > /dev/null
    sleep 0.5
done

# 3. Verify traces in Tempo (via the OTel Collector).
curl -s 'http://192.168.1.246:3200/api/search?tags=service.name%3Dsecurity-atlas&limit=5' | jq

# 4. Verify metrics in Prometheus (via the OTel Collector → prometheus exporter).
curl -s 'http://192.168.1.246:9090/api/v1/query?query=atlas_http_server_request_duration_seconds_count' | jq

# 5. Verify trace-to-logs correlation in Grafana UI:
#    Explore → Tempo → search for service.name=security-atlas →
#    click any trace → expand a span → "Logs for this span" should
#    surface the corresponding atlas log line from Loki, joined by trace_id.
```
