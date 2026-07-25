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

## Acceptance criteria (refined at pickup — implied by the Narrative + Anti-criteria)

- AC-1 — BOTH connectors advertise `profiles_supported = [pull, subscribe]` at
  register, named honestly (never "continuous monitoring").
- AC-2 — Each connector gains a `webhook` subcommand running a SOURCE-side receiver
  built as a thin adapter onto `connectors/shared/webhookrecv` (the first external
  consumer of slice 656; no second `http.Server` pattern; shared package
  unmodified).
- AC-3 — Each vendor adapter verifies its credential BEFORE building any record; a
  forged / missing-credential delivery is rejected 401 and produces no record
  (per-vendor test). **Jamf:** constant-time shared-secret header. **Intune:**
  constant-time `clientState`.
- AC-4 — The Intune `validationToken` echo handshake responds 200 with the token
  echoed as `text/plain` and builds NO record (test).
- AC-5 — A verified delivery emits the SAME `endpoint.device_posture.v1` record the
  pull profile emits (reuses `devposture` + `devrecord`, slice-490 over-collection
  guard); a test asserts no field beyond the posture-summary reaches a record.
- AC-6 — Cross-profile dedup: a webhook-emitted record and a pull-emitted record for
  the SAME device + hour derive the SAME idempotency key (reuses `mdm/idem`
  unchanged; test asserts key equality).
- AC-7 — DoS-bounded (method→405, size→413, verify-first→401, gosec-G112 timeouts);
  loopback-default bind, reverse-proxy TLS.
- AC-8 — Invariant #3: push only; zero `internal/api/` / `migrations` / `proto` /
  `schemaregistry` diff; no new evidence kind; no migration. Read-only +
  webhook-receive scope only.

## Anti-criteria (P0)

- Does NOT widen the platform-side wire — push only (invariant #3). The webhook
  is received SOURCE-side by the connector, not by the platform.
- Does NOT collect anything beyond the slice-490 posture-summary field set.
- Does NOT require a write/management MDM scope (read-only + webhook-receive).

Parent: #490.
