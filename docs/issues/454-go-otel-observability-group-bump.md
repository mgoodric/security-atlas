# 454 — go-otel observability group bump (13 modules)

**Cluster:** Infra
**Estimate:** S/M (0.5-1d)
**Type:** AFK

**Status:** `ready`

## Narrative

Dependabot PR **#951** bumps the 13 OpenTelemetry Go modules as a group —
`go.opentelemetry.io/otel` and its siblings from `1.43.0` (plus
`contrib/...otelhttp` `0.68.0`, `exporters/prometheus` `0.65.0`, etc.) to their
latest tags. OTEL is the project's native observability substrate (CLAUDE.md:
"OTEL native (traces + metrics + logs); default docker-compose bundles
Prometheus + Grafana + Tempo + Loki"), so the otel modules are on the hot path
of every instrumented request: `otelhttp` (HTTP server middleware), `otelpgx`
(Postgres query tracing), the OTLP exporters, and the runtime/metric SDKs.

The CI failure currently on #951 is **not** the bump — it is the
`audit_sink_failures` migration flake, which is being fixed separately. So #951
is "red for an unrelated reason." But a 13-module otel group bump still needs a
_validated_ upgrade, not a rubber-stamp: the modules are version-locked as a
group, and an otelhttp/otelpgx/exporter API change can break compilation or
silently change trace/metric emission. This slice performs the validated bump
and **supersedes dependabot PR #951**.

Note: `otelpgx` was already bumped separately (`0.10.0 → 0.11.1`, dependabot
**#656**, merged at `3b7d4a42`). This slice handles the remaining core otel
group and confirms `otelpgx@0.11.1` still composes with the bumped
`go.opentelemetry.io/otel` core.

**Scope discipline.** Group dependency bump + emission verification only. No new
spans/metrics, no instrumentation refactor, no exporter-endpoint config changes.

## Threat model

STRIDE pass. Runtime-security surface is minimal (observability instrumentation),
but the **telemetry-egress + credential-in-telemetry** axis is the real risk:
traces/metrics leave the deployment to an OTLP endpoint, so a change in what
gets attached to a span could leak.

**S — Spoofing / R — Repudiation / E — Elevation of privilege**

- _Threat:_ Not directly applicable — otel modules carry no auth/identity
  decision.
- _Mitigation:_ Confirm the bump touches no auth/RLS/identity code; it is
  confined to the instrumentation + exporter layer.

**T — Tampering**

- _Threat:_ An exporter behavior change (OTLP wire/version) could silently drop
  or corrupt telemetry, degrading the observability the operator relies on for
  incident detection (cross-ref slice 372 IR plan).
- _Mitigation:_ Verify traces + metrics still emit end-to-end against the
  docker-compose OTEL bundle (Tempo/Prometheus) — a span actually arrives.
- _Anti-criterion:_ P0-454-2.

**I — Information disclosure (the load-bearing risk)**

- _Threat:_ An otel/otelpgx/otelhttp default change could newly attach
  sensitive data to a span/metric — e.g. otelpgx capturing full SQL with
  embedded literals (tenant IDs, secrets in a `WHERE`), otelhttp capturing full
  request URLs/headers (a bearer token in a query string or `Authorization`
  header), or an exporter logging its endpoint **credentials** at startup.
- _Mitigation:_ Assert no telemetry endpoint credential is logged; confirm
  otelpgx's SQL-capture and otelhttp's header/URL-capture defaults are
  unchanged (or, if changed, re-pin the previous redaction behavior). The
  `Authorization` header and DB connection string must not appear in any
  emitted span attribute.
- _Anti-criterion:_ P0-454-1, P0-454-3.

**D — Denial of service**

- _Threat:_ A span/metric cardinality or sampling default change could blow up
  telemetry volume or memory.
- _Mitigation:_ Confirm sampling/exporter batching config is unchanged
  post-bump; no unbounded-cardinality attribute newly added.

## Acceptance criteria

- [ ] **AC-1.** `go.mod` bumps the 13 otel group modules
      (`go.opentelemetry.io/otel` + `sdk` + `metric` + `trace` + the
      `exporters/otlp/...` set + `exporters/prometheus` +
      `contrib/instrumentation/.../otelhttp` + `.../runtime`) to their target
      tags as a coherent group; `go mod tidy`; `go.sum` updated.
- [ ] **AC-2.** `go build ./...` clean; `go test ./...` (unit) green across the
      instrumented packages.
- [ ] **AC-3.** `otelpgx@0.11.1` (already merged via #656) is confirmed
      compatible with the bumped otel core (compiles + traces a query).
- [ ] **AC-4.** Trace + metric emission verified end-to-end: a request through
      `otelhttp` produces a span that reaches the docker-compose OTEL backend
      (Tempo) and a metric reaches Prometheus (manual or integration check;
      evidence in PR body).
- [ ] **AC-5.** No telemetry endpoint credential, `Authorization` header value,
      DB connection string, or raw SQL literal is present in any emitted span
      attribute (redaction defaults unchanged or re-pinned).
- [ ] **AC-6.** Sampling / exporter-batching config is unchanged (no
      cardinality or volume regression).
- [ ] **AC-7.** `pre-commit run --all-files` passes; CI green (the
      `audit_sink_failures` migration flake is fixed separately and is not this
      slice's concern — if it still flakes, note it, do not paper over it).
- [ ] **AC-8.** PR body notes "Supersedes #951; #656 (otelpgx) already merged".

## Constitutional invariants honored

- **Observability — OTEL native (canvas §9 / CLAUDE.md tech stack).** The bump
  keeps the OTEL-native substrate current without changing the
  instrumentation contract.
- **Tenant isolation (invariant #6).** Telemetry must not become a side channel
  that leaks tenant-scoped data across the deployment boundary.

## Canvas references

- `Plans/canvas/09-tech-stack.md` — "Observability — OTEL native".
- CLAUDE.md tech-stack "Observability" row.
- Slice 372 (IR plan) — observability is the detection substrate it depends on.

## Dependencies

- Supersedes **dependabot #951** (otel group bump).
- **#656** (otelpgx 0.10 → 0.11.1) — `merged` (`3b7d4a42`); this slice confirms
  it composes with the bumped otel core.

## Anti-criteria (P0 — block merge)

- **P0-454-1.** Does NOT log or emit any telemetry-endpoint credential, bearer
  token, `Authorization` header value, or DB connection string in a span /
  metric / startup log.
- **P0-454-2.** Does NOT silently break telemetry emission — a span/metric must
  be shown to still reach the backend (AC-4).
- **P0-454-3.** Does NOT change otelpgx SQL-capture or otelhttp header/URL
  redaction defaults toward more-verbose without re-pinning the prior behavior.
- **P0-454-4.** Does NOT add new spans/metrics or refactor instrumentation —
  group bump + emission verification only.

## Skill mix (3-5)

- `dependency-auditor` — the otel group changelogs (breaking API + default
  changes across the version span).
- `observability-designer` — verify trace/metric emission + redaction defaults.
- `tdd` — re-run instrumented-package unit tests.
- `simplify` — pre-PR pass.

## Notes for the implementing agent

- The 13 modules to bump as a group are visible in `go.mod` lines ~29-41:
  the `go.opentelemetry.io/contrib/instrumentation/.../otelhttp` +
  `.../runtime`, `go.opentelemetry.io/otel` + `/sdk` + `/sdk/metric` +
  `/metric` + `/trace`, and the `exporters/otlp/...` (otlptrace grpc+http,
  otlpmetric grpc+http) + `exporters/prometheus` set. Keep them version-coherent
  (otel core, contrib, and exporters move on aligned-but-distinct version lines
  — match the dependabot #951 target set).
- `otelpgx@0.11.1` is already in `go.mod` (line 17, merged via #656). The main
  validation here is that the _rest_ of the otel group bumps cleanly and still
  composes with it.
- The `audit_sink_failures` migration flake on #951's CI run is a red herring
  for this slice — it is being fixed elsewhere. Do not attempt to fix it here;
  if it surfaces, note it and rerun.
- The redaction check (AC-5) is the load-bearing security AC: otelhttp/otelpgx
  defaults around what request/SQL detail gets attached to spans are exactly the
  kind of thing a minor default change can flip. Inspect the actual span
  attributes emitted, not just that emission works.
