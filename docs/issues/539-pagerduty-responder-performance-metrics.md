# 539 — PagerDuty connector: responder-performance metrics

**Cluster:** Connectors
**Estimate:** M (1-2d)
**Type:** JUDGMENT (aggregation altitude + individual-vs-aggregate PII boundary)
**Status:** `blocked` (depends on #489 — base PagerDuty connector — merged first)

## Narrative

Slice 489 shipped the PagerDuty connector with on-call coverage + incident
summaries. It deliberately deferred **responder-performance metrics** (P0-489-7)
— time-to-acknowledge, time-to-resolve, escalation rates — as a follow-on,
because per-responder performance data is both a PII-adjacent surface (it
profiles named individuals) and an aggregation-altitude JUDGMENT the base slice
should not pre-empt.

Responder-performance metrics support SOC 2 CC7.4 ("responds to identified
security incidents") evidence at the program level: an auditor wants proof that
incidents are acknowledged and resolved within target windows, not a per-engineer
scorecard. The hard JUDGMENT this slice owns is the **aggregation altitude**:
metrics should almost certainly be collected as **service- / team-level
aggregates** (MTTA / MTTR percentiles over a window), NOT per-named-responder
records, to avoid turning the evidence ledger into an individual-performance
surveillance store.

## Threat model

Source-credential-heavy, plus a DOMINANT individual-profiling risk.

- **S — Spoofing.** Platform push via the existing connector credential; source
  via the read-only PagerDuty REST token. **Mitigation:** read-only
  least-privilege token; source creds stay source-side (invariant #3).
- **T — Tampering.** sha256 content-hash per record; ingest validates.
- **R — Repudiation.** Register-per-run + stable `actor_id`
  (`connector:pagerduty:metrics@<version>`) + `observed_at` granularity.
- **I — Information disclosure (DOMINANT).** Per-responder timing data profiles
  named individuals (a privacy + works-council concern in some jurisdictions).
  **Mitigation:** collect SERVICE- / TEAM-level aggregates (MTTA / MTTR / count
  / percentiles over a bounded window) — NOT per-named-responder records. The
  aggregation altitude is the JUDGMENT; a test asserts no individual responder
  identity is the grain of a metrics record.
- **D — Denial of service.** Bound the window + per-run cap + run timeout.
- **E — Elevation of privilege.** Read-only token only; no write/admin.

## Acceptance criteria (sketch — refine at pickup)

- [ ] A metrics collector lands following the slice-489 pattern.
- [ ] The aggregation-altitude JUDGMENT is made and documented (service/team
      aggregates, not per-named-responder).
- [ ] Pushes to the single `IngestEvidence` (`Push`) API — no wire change.
- [ ] `pagerduty.response_metrics.v1` evidence kind + schema with
      `x-default-scf-anchors` (candidate: `IRO-02` / `MON-02`), registered in
      `DefaultSeed`.
- [ ] Tests cover collect → push against a mocked PagerDuty API (no live source).
- [ ] A test asserts metrics are aggregated (no per-named-responder grain); a
      test asserts the token is never logged.
- [ ] README + decisions log + changelog.

## Anti-criteria (P0)

- **P0.** No platform-side wire change (push only — invariant #3).
- **P0.** No write/admin PagerDuty token.
- **P0.** No per-named-responder performance records (aggregate grain only).
- **P0.** No token logged or transmitted into the platform.
- **P0.** No "continuous monitoring" mislabel; the profile name is honest.

## Dependencies

- **#489** (base PagerDuty connector) — provides the connector scaffold, the
  `pagerdutyauth` + `pdhttp` packages, and the IR-evidence family.
