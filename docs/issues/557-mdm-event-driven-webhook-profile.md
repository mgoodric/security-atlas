# 557 — MDM connectors: event-driven (webhook) profile (Jamf + Intune)

**Cluster:** Connectors
**Estimate:** M-L (2-3d)
**Type:** code (profile addition + receiver wiring)
**Status:** `blocked` (depends on #490 — base MDM connectors — merged first)

## Narrative

Slice 490 shipped the Jamf + Intune MDM connectors with a **pull-only** profile
(`profiles_supported = [pull]`), naming the interval honestly (operator-scheduled,
recommended 24h — explicitly NOT "continuous monitoring", P0-490-6). It deferred
an event-driven profile as a follow-on (P0-490-7).

Both MDMs emit compliance-state-change events (Jamf webhooks;
Intune/Graph change notifications). An event-driven profile would let the
connector push a fresh `endpoint.device_posture.v1` record when a device's
compliance state changes, narrowing evidence freshness from "up to 24h stale" to
"near-real-time" — without polling more often (honest interval discipline
preserved).

This slice adds the `subscribe` profile to the connectors and the platform-side
webhook-receiver wiring. The reused evidence kind is unchanged
(`endpoint.device_posture.v1`); only the retrieval profile changes. The
platform-side wire stays push (invariant #3) — the connector receives the MDM
webhook source-side and emits via `Push` as today.

## Dependencies

- **#490** (base MDM connectors) — must merge first; the evidence kind, record
  builder, and over-collection guard are reused unchanged.

## Anti-criteria (P0)

- Does NOT widen the platform-side wire — push only (invariant #3). The webhook
  is received SOURCE-side by the connector, not by the platform.
- Does NOT collect anything beyond the slice-490 posture-summary field set.
- Does NOT require a write/management MDM scope (read-only + webhook-receive).

Parent: #490.
