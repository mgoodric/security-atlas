# Observability tuning

Operator runbook for keeping OpenTelemetry overhead bounded under load.
For the baseline enable/verify steps see [`docs/observability.md`](../observability.md);
this document covers the **performance-tuning knobs** an operator reaches
for when telemetry cost starts showing up under sustained load.

> Surfaced during slice 332's performance audit (F-OTEL-2) and shipped in
> slice 381. The audit flagged that `otelpgx` wraps every pgx
> `Exec`/`Query` with a child span, so trace-emission cost tracks the
> database query rate one-to-one.

---

## How tracing cost scales

The atlas pgx pool is wired with `otelpgx.NewTracer()`
(`internal/observability/otel/pgx.go`). Every database `Exec` and `Query`
starts a child span. That is exactly what you want for diagnosing a slow
request — but it means trace-emission work is proportional to the
**database query rate**, not the HTTP request rate.

The eval engine amplifies this: a scheduled `EvaluateAll` tick walks
`controls × scope cells`, so a single tick can issue hundreds of queries
in a burst. With a high-fidelity sampler that is hundreds of fully
recorded-and-exported spans per tick.

When OTel is **unconfigured** (`OTEL_EXPORTER_OTLP_ENDPOINT` unset) the
SDK is a genuine no-op and this section does not apply — you pay nothing.
The tuning below matters only once you point atlas at a collector.

---

## High DB query rate

Use this recipe when atlas is exporting traces to a collector **and** the
database query rate is high enough that span emission shows up in CPU
profiles or collector ingest cost — in practice, sustained DB rates above
roughly 100 queries/second (e.g. a busy eval-engine schedule, or an
atlas-edge node fanning out connector work).

The lever is the head sampler. Lower the sampled fraction so the bulk of
spans are dropped at creation (cheap) rather than recorded and exported
(expensive):

```bash
# deploy/docker/.env  (or the Helm values.yaml env block)
OTEL_TRACES_SAMPLER=parentbased_traceidratio
OTEL_TRACES_SAMPLER_ARG=0.1     # keep ~10% of root traces; drop the rest
```

- `parentbased_traceidratio` keeps a child span's sampling decision
  consistent with its parent (so a sampled request keeps its whole trace,
  and an unsampled one drops cleanly) while ratio-sampling the roots.
- `OTEL_TRACES_SAMPLER_ARG` is the kept fraction. `0.1` keeps 10%; drop it
  further (`0.05`, `0.01`) if collector ingest is still the bottleneck at
  very high DB rates. Raise it toward `1.0` only while actively debugging.

Verify the change took effect on the next start — atlas logs the active
sampler:

```
atlas: opentelemetry: enabled endpoint=http://otel-collector:4317 sampler=parentbased_traceidratio sampler_arg=0.1 ...
```

### Note on the default

atlas already **defaults** to `parentbased_traceidratio` at `0.1`
(`internal/observability/otel/otel.go`), so a fresh deployment is sampled
at 10% out of the box — you only reach for this recipe to push the ratio
lower under exceptional DB load, or to confirm/pin the value explicitly
in your env file.

(The slice 332 audit's F-OTEL-2 observation described the default as
`parentbased_always_on`; that was a stale reading of the slice 121 design
trail. The shipped default is the ratio sampler. Recorded in the slice
381 decisions log, D3.)

---

## Metrics and the `/metrics` fallback

Metrics export is OTLP-push by default. If you scrape with Prometheus
instead, opt into the pull endpoint:

```bash
ATLAS_METRICS_FALLBACK_ENABLE=true   # serve Prometheus /metrics for scrape
```

Metric instruments are pre-aggregated, so unlike traces their cost does
not scale with query rate — leave this off unless your collector topology
needs a scrape target.

---

## Related references

- [`docs/observability.md`](../observability.md) — enable/verify baseline,
  external audit-log sink, OTel ↔ audit-sink composition.
- `internal/observability/otel/otel.go` — SDK init + sampler resolution.
- `internal/observability/otel/pgx.go` — the per-query tracer wiring.
- `docs/audits/332-performance-audit-report.md` — F-OTEL-2, the finding
  this runbook answers.
