# Slice 539 — PagerDuty responder-performance metrics — decisions log

JUDGMENT slice. This records the subjective build-time calls — the aggregation
altitude (the load-bearing one), the window / percentile choices, the SCF anchor
choice, and the structural over-collection guard shape. It does NOT block merge;
the maintainer iterates post-deployment from the "Revisit once in use" notes.

- detection_tier_actual: unit
- detection_tier_target: unit

(One bug surfaced during the slice — my own `TestClient_TokenNeverLogged`
asserted that the shared `pdhttp.Transport` struct's unexported `token` field is
absent from its `%+v` reflection. That is a false leak signal: the transport
value is internal and never logged, and `%+v` reflects unexported fields within
the package. Caught at the `unit` tier where it should be; the fix narrowed the
assertion to the surfaces that are actually logged / transmitted — the error
path and the emitted records. No shared type was changed.)

## Decisions made

### D1 — Aggregation altitude: SERVICE grain, never per-named-responder (the load-bearing call)

- **Chosen:** metrics are collected and emitted as **service-level aggregates**
  (one `pagerduty.response_metrics.v1` record per service), carrying MTTA / MTTR
  (mean + p50/p90/p95) and incident / acknowledged / resolved counts over the
  window. The connector **never decodes which individual** acknowledged or
  resolved an incident.
- **Options considered:**
  1. Per-named-responder records (a per-engineer scorecard) — **rejected.** This
     is the DOMINANT Information-Disclosure threat in the slice threat model: it
     turns the evidence ledger into an individual-performance surveillance store,
     a privacy + works-council concern in some jurisdictions, and is the exact
     over-collection the base slice (489) deferred this surface to avoid.
  2. Service-level aggregates — **chosen.** The auditor demand (SOC 2 CC7.4) is
     "incidents are acknowledged and resolved within target windows," a
     **program-level** posture. The service grain answers that without profiling
     a single human.
  3. Per-team aggregates — deferred (revisit). Plausibly a better board-reporting
     altitude, but PagerDuty's team model is a second lookup and the service
     grain is the one already used by every other PagerDuty kind's scope, so it
     ships first.
- **Enforcement is structural, not just behavioral** (mirrors slice 538 /
  slice 595): the connector-side types (`RawAck` / `RawTiming` /
  `ServiceMetrics`) have **no field that can hold a responder identity BY
  CONSTRUCTION** — the only string field is the opaque `ServiceID` (the grain).
  A reflection guard (`metrics_guard_test.go:TestAggregateOnly_StructuralGuard`)
  fails the build if a responder-identity / free-text field is added. A drop test
  (`TestCollect_DropsResponderIdentity`) feeds source data with named
  acknowledgers, assignees, responder emails, and an incident title naming an
  individual, and asserts no per-named-responder identity becomes the grain of
  any emitted record (the `TestNormalize_DropsSecretBearingSettings` shape the
  slice prompt called for).
- **Confidence:** high. This is the spec's explicit mandate and matches the
  established slice-489-family structural-guard pattern.

### D2 — Metric set + window + percentiles

- **Chosen:** MTTA (time from incident creation to FIRST acknowledgment) and
  MTTR (creation to resolution), each as **mean + p50 + p90 + p95** in whole
  seconds, plus `incident_count` / `acknowledged_count` / `resolved_count` over
  the same bounded window as the incident + postmortem reads (`--lookback-days`,
  default 90).
- **Rationale:**
  - **First-ack for MTTA:** time-to-acknowledge measures responsiveness; later
    acks (re-assignments) do not change the responsiveness fact. Only the
    earliest `at` is used.
  - **Mean + p50/p90/p95:** the mean alone hides tail latency (the slow
    incident that matters most to an auditor); p90/p95 surface it. p99 omitted —
    at the solo-leader persona's incident volume a p99 is a single data point and
    reads as noise; revisit if volume grows.
  - **Nearest-rank percentile (not linear interpolation):** the result is always
    an actually-observed sample value, which an auditor can reconcile against a
    single real incident. Explainability beats statistical smoothness here.
  - **Whole seconds:** sub-second precision is meaningless for incident response;
    integer seconds keep the payload clean and the schema simple.
  - **Same window as the sibling reads:** one `--lookback-days` flag governs all
    bounded reads; a separate metrics window would be a config wart.
  - **Open incidents still contribute to MTTA** (they can be acknowledged but
    unresolved); only resolved incidents contribute to MTTR. The counts make the
    two sample sizes explicit.
  - **Negative / clock-skew durations dropped:** an ack or resolve timestamp
    before the creation timestamp is skew, not a real sample; it is excluded from
    the aggregate (the incident still counts toward `incident_count`).
- **Confidence:** medium. The metric set is conventional and defensible, but the
  exact percentile set and the first-ack-vs-any-ack call are the kind of thing a
  real auditor's preference should confirm.

### D3 — SCF anchors: `IRO-02` (Incident Handling) + `MON-02` (Continuous Monitoring)

- **Chosen:** `x-default-scf-anchors = [IRO-02, MON-02]`; `--metrics-control`
  defaults to `scf:IRO-02`, operator-overridable.
- **Rationale:** the metrics surface's auditor value is proving incidents are
  **handled within target windows** — `IRO-02` (Incident Handling) is the
  canonical anchor for the ack/resolve-timing facet (it is also the
  incident-summary kind's primary anchor, which is correct: response metrics are
  a quantitative view of the same handling activity). `MON-02` (Continuous
  Monitoring) covers the metrics/measurement facet — this is the operational
  monitoring of the IR program's effectiveness. Both anchors are **real and
  already in use in this repo** (IRO-02 on `pagerduty.incident_summary.v1`;
  MON-02 on `github.audit_event.v1` and in slice 488's monitoring connectors), so
  the choice introduces no new anchor. The slice doc floated `IRO-02` / `MON-02`
  as the candidate; this confirms it.
- **Caveat:** like every connector's anchors these are DEFAULT mapping hints
  flagged for maintainer accuracy recheck (OQ #9).
- **Confidence:** high (both anchors verified present in the catalog fixtures /
  existing schemas).

### D4 — No new credential scope; reuse the slice-489 read-only token + `pdhttp`

- **Chosen:** the metrics client reuses `pagerdutyauth` (read-only token) and
  `pdhttp` (thin read-only GET transport), reading `GET /incidents` — the same
  endpoint family as the incident-summary kind. No new scope, no new package
  surface for auth.
- **Rationale:** incident timings (created / acknowledgment `at` / resolved) are
  readable with the same read-only REST API key; there is no least-privilege
  reason to widen the token (P0-539). Matches the slice-538 "no new credential
  scope" decision.
- **Confidence:** high.

### D5 — Honest interval; no wire change; profiles stay `[pull]`

- **Chosen:** the metrics collector is one bounded read-and-push pass per `run`
  invocation, operator-scheduled (recommended 24h), pushed through the single
  `IngestEvidence` (`Push`) API. `profiles_supported` stays `[pull]`. The
  `PullInterval` string and the `run` long-help name the cadence honestly — NOT
  "continuous monitoring."
- **Rationale:** invariant #3 (single push wire surface) + the anti-pattern ban
  on continuous-monitoring mislabels. No platform-side change was made.
- **Confidence:** high.

## Revisit once in use

- **Per-team vs per-service grain (D1):** if a board pack reads better at the
  team altitude than the service altitude, add a team-grained aggregate (a second
  PagerDuty lookup). The structural guard already forbids the per-responder
  grain regardless.
- **Percentile set + first-ack call (D2):** confirm p90/p95 (and the absence of
  p99) match a real auditor's expectation at real incident volume; confirm
  first-ack is the right MTTA definition for the program (vs. SLA-clock ack).
- **`IRO-02` / `MON-02` anchor accuracy recheck (D3 / OQ #9).**
- **Negative-duration handling (D2):** if clock-skew samples turn out to be
  common (rather than rare), surface a count of dropped samples so the aggregate
  is not silently lossy.
- **Event-driven profile (slice 540):** a PagerDuty incident-lifecycle webhook
  profile would let metrics update on resolution rather than on the next poll —
  the same follow-on already noted for the incident + postmortem surfaces.
