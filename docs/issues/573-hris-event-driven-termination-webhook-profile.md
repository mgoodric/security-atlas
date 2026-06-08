# 573 — HRIS connectors: event-driven termination-webhook profile (Rippling + BambooHR)

**Cluster:** Connectors
**Estimate:** M-L (2-3d)
**Type:** code (profile addition + receiver wiring)
**Status:** `blocked` (depends on #491 — base HRIS connectors — merged first)

## Narrative

Slice 491 shipped the Rippling + BambooHR HRIS connectors with a **pull-only**
profile (`profiles_supported = [pull]`), naming the interval honestly
(operator-scheduled, recommended 24h — explicitly NOT "continuous monitoring",
P0-491-6). It deferred an event-driven profile as the flagged
**highest-value follow-on** (P0-491-7): a real-time leaver signal for
deprovisioning.

The leaver case is the asymmetric one — a terminated worker whose access is not
revoked is the exact failure SOC 2 CC6.3 guards against, and "up to 24h stale" is
the window in which that failure lives. Both HRIS systems emit termination /
status-change events (Rippling webhooks; BambooHR webhooks). An event-driven
profile would let the connector push a fresh `hris.worker_lifecycle.v1` record
the moment a worker is marked terminated, narrowing the deprovisioning-evidence
freshness from "up to 24h" to "near-real-time" — without polling more often
(honest-interval discipline preserved).

This slice adds the `subscribe` profile to the connectors and the platform-side
webhook-receiver wiring. The reused evidence kind is unchanged
(`hris.worker_lifecycle.v1`); only the retrieval profile changes. The
platform-side wire stays push (invariant #3) — the connector receives the HRIS
webhook source-side and emits via `Push` as today.

## Dependencies

- **#491** (base HRIS connectors) — must merge first; the evidence kind, record
  builder, auth packages, and over-collection guard are reused unchanged.

## Anti-criteria (P0)

- Does NOT widen the platform-side wire — push only (invariant #3). The webhook
  is received SOURCE-side by the connector, not by the platform.
- Does NOT collect anything beyond the slice-491 worker-lifecycle field set — the
  sensitive-PII exclusion (SSN / compensation / address / bank / benefits /
  performance / DOB / personal contact) holds unchanged.
- Does NOT require a full-PII / write HRIS scope — read-only minimal-field +
  webhook-receive only.

Parent: #491.
