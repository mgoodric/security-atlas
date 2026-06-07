# 535 — Monitoring connectors: alert-firing-history (event-driven) profile

**Cluster:** Connectors
**Estimate:** L (3-5d)
**Type:** JUDGMENT (profile shape + evidence-kind shape + dedup/idempotency choices)
**Status:** `blocked` (depends on #488 — base monitoring connectors — merged first)

## Narrative

Slice 488 shipped the Datadog + Grafana connectors with a **pull-only** profile
emitting alert/monitor _configuration_ inventory (`monitoring.alert_config.v1`).
It deliberately deferred **alert-firing history** — the record of when an
alert/monitor actually fired and was acknowledged/resolved — as a follow-on
(P0-488-7). Firing history is the evidence that monitoring is not just configured
but _operational and acted upon_ (SOC 2 CC7.3 "the entity evaluates security
events" / CC7.4 "responds to identified security incidents").

This slice adds a firing-history surface for both vendors:

- **Datadog:** monitor state-transition / event history (e.g. Events API or the
  monitor `overall_state` transitions).
- **Grafana:** alert-instance state history (the Grafana alerting state-history
  API).

**The hard JUDGMENT this slice owns: the profile.** Slice 488's connectors are
pull-only on a named honest interval. Firing history is a stream of timestamped
events, which tempts an event-driven / `subscribe` profile. The slice must decide
honestly between (a) a **bounded pull** of the last-N-hours of firing events on
the same scheduled cadence (simplest; still honest — names the window), or (b) a
genuine **event-driven `subscribe`** profile if (and only if) the vendor offers a
real push/webhook the connector receives. It must NOT label a 24-hour poll
"continuous monitoring" or "event-driven" (P0). The `profiles_supported`
registration value must accurately reflect how the connector retrieves the data
from the source (anti-pattern: honest intervals).

A new evidence kind (`monitoring.alert_firing.v1` — one record per firing event:
rule_id, vendor, fired_at, resolved_at, state, and the routing-target HANDLE the
notification went to) with `x-default-scf-anchors` (candidate: `MON-01` +
`IRO-02` Incident Handling), registered in `DefaultSeed`. No platform-side wire
change (invariant #3 — push only).

**Scope discipline.** Firing _events_ only — fired_at / resolved_at / state /
which configured target was notified. NEVER the alert's full message body
(can contain incident detail / PII), the triggering metric values, the secret
webhook URL, or recipient PII.

## Threat model (STRIDE)

Source-credential-heavy, plus an event-stream-volume risk and an
incident-detail-leakage risk that 488's config-only surface did not have.

- **S — Spoofing.** Platform push via existing credential; source via the
  vendor key/token. **Mitigation:** read-only least-privilege source scope
  (Datadog `events_read` / `monitors_read`; Grafana Viewer read of state-history);
  source creds stay source-side.
- **T — Tampering.** sha256 content-hash per record; ingest validates.
- **R — Repudiation.** Register-per-run + stable `actor_id`
  (`connector:<vendor>:firing@<version>`) + `observed_at`/`fired_at`
  granularity. Idempotency on (vendor, rule_id, fired_at) so re-reads of an
  overlapping window do not double-write the ledger — a key dedup JUDGMENT.
- **I — Information disclosure (DOMINANT).** Firing events can embed incident
  detail / PII in the alert message and route to secret-bearing targets.
  **Mitigation:** collect fired_at / resolved_at / state / rule_id + target
  HANDLE only — never the message body, triggering metric values, secret webhook
  URL, or recipient PII. A test asserts no message body / metric value / secret /
  recipient PII enters a record.
- **D — Denial of service.** A busy alert can fire thousands of times; bound the
  per-run window + a per-run event cap + run timeout. This is the load-bearing DoS
  surface for this slice (more acute than 488's config inventory).
- **E — Elevation of privilege.** Read-only least-privilege; name the exact
  minimum per vendor.

## Acceptance criteria (sketch — refine at pickup)

- [ ] Firing-history collectors land for both vendors following the slice-488
      pattern (register-per-run, stable `actor_id`, scope minimum).
- [ ] The profile decision is made HONESTLY and documented (bounded-pull window
      vs genuine event-driven `subscribe`); `profiles_supported` reflects it
      accurately; no "continuous monitoring" mislabel.
- [ ] Bounded per-run window + event cap + run timeout (threat-model D).
- [ ] Idempotency on (vendor, rule_id, fired_at) so overlapping-window re-reads
      don't double-write (record the dedup JUDGMENT).
- [ ] Pushes to the single `IngestEvidence` (`Push`) API — no wire change.
- [ ] `monitoring.alert_firing.v1` evidence kind + schema with
      `x-default-scf-anchors`, registered in `DefaultSeed`.
- [ ] Tests cover collect → push against mocked source APIs (no live
      Datadog/Grafana).
- [ ] A test asserts no message body / metric value / secret URL / recipient PII
      enters a record; a test asserts the source key/token is never logged.
- [ ] READMEs + decisions log + changelog.

## Anti-criteria (P0)

- **P0.** No platform-side wire change (push only — invariant #3).
- **P0.** No write/admin source scope.
- **P0.** No alert message bodies / triggering metric values / secret webhook
  URLs / recipient PII in a record.
- **P0.** No source key/token logged or transmitted into the platform.
- **P0.** No mislabeling a poll as "continuous monitoring"; the profile name is
  honest.

## Dependencies

- **#488** (base monitoring connectors) — provides the connector scaffolds, auth,
  and the monitoring evidence family.
