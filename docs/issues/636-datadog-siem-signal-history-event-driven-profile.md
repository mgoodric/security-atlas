# 636 — Datadog connector: Cloud-SIEM signal-history (event-driven) profile

**Cluster:** Connectors
**Estimate:** M (1-2d)
**Type:** JUDGMENT (evidence-kind shape + over-collection boundary)
**Status:** `ready` (follow-on of #533 — Datadog Cloud-SIEM rule evidence — merged first)

## Narrative

Slice 533 added `datadog.siem_rule.v1` — detection-rule **configuration**
inventory (which rules exist, their class, severity, enabled state, routing). It
deliberately does NOT read firing **signals**, the detection query's matched raw
log samples, or matched-event payloads (P0-533 / the structural over-collection
guard: the collector structs physically cannot hold a signal).

There is a separate, recurring audit demand — "show that detection rules actually
**fired** and were **triaged** over the audit period" (SOC 2 CC7.3 incident
_response_, not just CC7.2 detection _configuration_). That is a signal-history /
triage-outcome surface, structurally distinct from rule configuration, and would
arrive via Datadog's security-signals API on an **event-driven** (subscribe /
webhook-receipt) profile rather than the slice-533 `pull` config snapshot.

This slice scopes that surface IF user demand surfaces. The load-bearing JUDGMENT
is the over-collection boundary: a signal-triage evidence record must carry
triage METADATA (signal id, rule id, severity, status open/triaged/closed,
triaged-at, triager handle) **only** — NEVER the matched log/event payloads, the
raw query, or PII in the signal body. The slice-533 structural-guard pattern
(struct cannot hold the excluded data) is the template.

## Anti-criteria (P0)

- **P0.** No platform-side wire change (push only — invariant #3).
- **P0.** No write/admin source scope.
- **P0.** No matched log/event payloads, raw detection queries, or signal-body
  PII in any record — triage METADATA only.
- **P0.** Honest profile naming: event-driven (subscribe/webhook) where the API
  allows, NOT 24h polling dressed as "continuous".

## Dependencies

- **#533** (Datadog SIEM-rule configuration evidence) — established the
  connector's SIEM read path, auth scope family, and the structural
  over-collection guard this surface mirrors.
