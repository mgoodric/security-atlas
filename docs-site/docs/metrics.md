# Measuring your program

Slice 076 lands the metrics-catalog backbone: a curated set of
~40 measurements that compose into board-, program-, and team-level
KPIs. This page explains what the catalog is, how the cascade is
structured, and how to interpret what you see.

> The full catalog is auto-generated at
> [metrics-reference.md](metrics-reference.md). Read this page first to
> learn how the catalog is shaped; jump to the reference for
> per-metric definitions.

## What the catalog is

The platform ships an opinionated catalog of metrics — the numbers a
solo security leader at a 50-150-person security-product startup
actually reaches for when:

- Their board asks "is the security program working?"
- Their auditor asks "are you ready for fieldwork next month?"
- Their team asks "what should we work on this sprint?"
- They ask themselves "where is risk concentrating without me
  noticing?"

Every metric in the catalog answers one of those questions. Vanity
metrics — interesting-but-unused numbers — were rejected during the
slice-076 selection process; the
[decisions log](https://github.com/mgoodric/security-atlas/blob/main/docs/audit-log/076-metrics-catalog-cascade-decisions.md)
records what was rejected and why.

## The three cascade levels

| Level     | Audience                     | Cadence (typical) | Example                          |
| --------- | ---------------------------- | ----------------- | -------------------------------- |
| `board`   | Board members, exec sponsors | Quarterly         | _Audit readiness_                |
| `program` | The security-program owner   | Weekly / monthly  | _Per-framework coverage_         |
| `team`    | Individual contributors      | Daily / weekly    | _Open findings by control owner_ |

A board metric is a **rollup** of one or more program metrics, which
are themselves rollups of team metrics. The cascade edges encode the
dependency: when _Audit readiness_ dips, you click down to see
_Per-framework coverage_ and _AuditPeriod currency_; when
_Per-framework coverage_ dips, you click further down to
_Walkthrough completion rate_.

You're never reading a single number without a path to its inputs.

## Compute strategies

Each catalog metric has a `compute_strategy` that says how the platform
produces its value:

| Strategy               | What it means                                                                                       |
| ---------------------- | --------------------------------------------------------------------------------------------------- |
| `computed`             | A Go evaluator runs every 15 minutes against the platform's primitives. No work for you.            |
| `manual_input`         | You submit values via `POST /v1/metrics/{id}/inputs`. Each submission is auditable.                 |
| `external_integration` | A future connector will compute it. For v1 the platform treats it like `manual_input` (you submit). |

Eight metrics are `computed` in v1: _Program effectiveness_,
_Evidence freshness %_, _Audit readiness_, _Open risk financial
exposure_, _Policy attestation rate_, _Vendor risk concentration_,
_Exception expiration runway_, and _Critical-findings SLA_.

The remaining ~32 are `manual_input` or `external_integration`. Some
have integration paths already filed (HRIS, ticketing, training); some
are intentionally manual (board attendance, regulatory exposure) because
the source data lives outside the platform's purview.

## Reading a metric value

Every metric value lives in `metric_observations` — a tenant-scoped,
append-only series. A manual input also writes a permanent audit row
in `metric_inputs` (who, when, what notes), and an `AFTER INSERT`
trigger replicates that input into `metric_observations` so reads of
the series include both manual and computed values uniformly.

The HTTP API exposes:

| Endpoint                                  | What it returns                               |
| ----------------------------------------- | --------------------------------------------- |
| `GET /v1/metrics`                         | The catalog, optionally filtered by level     |
| `GET /v1/metrics/{id}`                    | One metric + its immediate parents + children |
| `GET /v1/metrics/cascade?level=board`     | The full cascade tree (recursive walk)        |
| `GET /v1/metrics/{id}/observations?since` | The observation series (computed + manual)    |
| `POST /v1/metrics/{id}/inputs`            | Submit a manual value (admin role; auditable) |
| `GET /v1/metrics/{id}/target`             | The target + warning + critical thresholds    |
| `PUT /v1/metrics/{id}/target`             | Set or update the target (admin role)         |

## Worked example: when _Audit readiness_ dips

You see the board pack show _Audit readiness_ at 62%, down from 84% a
quarter ago. What now?

1. `GET /v1/metrics/audit_readiness_score` — read the metric's
   children. v1's cascade hangs three program metrics under it:
   _Per-framework coverage_, _AuditPeriod currency_, and
   _SSP currency by framework_.
2. `GET /v1/metrics/framework_coverage_pct/observations` — see the
   series. If SOC 2 was 92% and ISO 27001 was 71% a quarter ago, but
   now SOC 2 is 88% and ISO 27001 is 41%, you know which framework
   dropped.
3. `GET /v1/metrics/cascade?level=program` — see what hangs under
   _Per-framework coverage_. For ISO 27001 the cascade child
   _Walkthrough completion rate_ is at 25%, vs SOC 2's 91%.
4. Open the audit hub, filter to ISO 27001, and you find a cluster of
   in-scope controls without walkthroughs — that's your work.

The cascade made the rollup interrogable instead of just descriptive.

## Setting targets

For every metric you care about, set a target via
`PUT /v1/metrics/{id}/target`:

```json
{
  "target_value": 0.95,
  "warning_threshold": 0.85,
  "critical_threshold": 0.7,
  "direction": "higher_is_better",
  "notes": "Quarterly board pack expects ≥95%"
}
```

The `direction` field is one of `higher_is_better`,
`lower_is_better`, or `target_is_better`. Targets are tenant-scoped
(your tenant's _Audit readiness_ target is not visible to another
tenant) and are upserted on each PUT.

## Submitting a manual input

For `manual_input` and `external_integration` metrics, submit values
via `POST /v1/metrics/{id}/inputs`:

```json
{
  "numeric_value": 0.87,
  "observed_at": "2026-05-15T12:00:00Z",
  "dimensions": { "framework": "soc2" },
  "notes": "Quarterly count from CC6.3 access review"
}
```

The trigger replicates the input to `metric_observations` so a
follow-up `GET /v1/metrics/{id}/observations` returns the value
alongside any computed entries. Manual entries are **append-only** —
there is no edit or delete API for `metric_inputs` (it's the audit
trail). To "correct" a value, submit a new input with a fresh
`observed_at`; readers consume the latest by `observed_at`.

## What slice 076 deliberately does NOT do

The slice ships the backbone. These pieces are explicitly downstream:

- **A dashboard view**. The follow-on slice (issue #097) lands the
  metrics dashboard + cascade-tree visualization.
- **Alerting / threshold tripping**. Setting a `critical_threshold` on
  a target does not send a notification today. The notification path
  is a separate slice that consumes the threshold definition.
- **Anomaly detection** on observation series. v1 reads observations
  in a time window; statistical anomaly is a v2 concern.
- **Per-tenant catalog extension**. The catalog is platform-shared in
  v1. A tenant who wants to add a private metric (the `tenant_id IS
NULL` RLS pattern is set up for it) waits for a separate slice.
- **Integration adapters for the ~32 manual metrics**. Each
  integration (HRIS, ticketing, SIEM, training platform, etc.) lives
  in its own connector-shape slice.

## Constitutional invariants honored

The metrics-catalog implementation respects three constitutional
invariants:

- **#1 (One control, N framework satisfactions)** — per-framework
  metric breakdowns read the SCF anchor + framework_scope graph;
  there is no duplicated per-framework metric store.
- **#6 (Tenant isolation at the DB layer)** — every observation,
  target, and input table is RLS-bound to the tenant. The catalog +
  cascade-edges tables are intentionally singleton-tenant-agnostic
  (the slice 068 `tenant_id IS NULL OR …` pattern).
- **#9 (Manual evidence is first-class)** — the `metric_inputs` →
  `metric_observations` insert trigger means manual and computed
  values share one read shape. Consumers don't special-case.

The AI-assist boundary is also honored: the catalog defines metrics
and the cron computes their values, but the platform never
auto-generates narrative interpretation of a dip. Templated rollups in
the board pack consume the values; the prose interpretation is
authored by the program owner.
