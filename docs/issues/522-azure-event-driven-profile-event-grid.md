# 522 — Azure connector: event-driven (subscribe) profile via Event Grid / Activity Log

**Cluster:** Connectors
**Estimate:** L (3-5d)
**Type:** JUDGMENT (subscribe-profile shape + dedup-with-pull interaction)
**Status:** `blocked` (depends on #486 — base Azure connector — merged first)

## Narrative

Slice 486 shipped the base Azure connector on the **pull** profile only: each
invocation is one bounded read-and-push pass on an operator-scheduled cadence
(honestly named — not "continuous monitoring"). This slice adds a **subscribe**
profile: the connector subscribes to Azure change events (Activity-Log diagnostic
settings → Event Grid, or an Event Grid system topic on the subscription) so a
configuration change to an in-scope Entra role assignment / storage account
emits a fresh evidence record promptly, rather than waiting for the next pull.

The connector then registers `profiles_supported=[pull, subscribe]`. Per the
canvas, `profiles_supported` describes how the connector retrieves data **from
Azure**; the platform-side wire stays push regardless (invariant #3) — so this
slice still adds NO platform-side wire change.

**The interval-honesty discipline still holds:** even with subscribe, the docs
name what "event-driven" means concretely (Event Grid delivery latency, the
diagnostic-setting flush cadence) rather than claiming instantaneous "continuous
monitoring". The pull profile remains the reconciliation backstop (subscribe can
miss events; pull catches drift).

**The load-bearing design call (JUDGMENT):** how subscribe-emitted records dedupe
against pull-emitted records. The slice-486 idempotency key is
`sha256("<kind>|<resource_id>|<hour>")`. A subscribe event mid-hour and a pull at
the top of the next hour must not double-write — the dedup interaction is the
core design work here.

## Threat model

Inherits the slice-486 connector-family threat model, plus a new inbound-event
surface.

- **S — Spoofing.** The Event Grid delivery must be authenticated (Event Grid
  validation handshake + a shared-secret / Entra-authenticated webhook); a forged
  event must not inject a fabricated evidence record. The connector re-reads the
  changed resource via the read-only ARM/Graph path before emitting — the event is
  a _trigger_, not the _data_ (so a forged event at worst causes a redundant
  read-and-emit of real state, never a fabricated record).
- **T — Tampering.** sha256 content-hash per record; ingest validates.
- **R — Repudiation.** register-per-run + stable `actor_id` + `observed_at` from
  the re-read, not the event timestamp.
- **I — Information disclosure.** Same config-only boundary as 486 — the event
  carries only a resource id; the connector re-reads config metadata only.
- **D — Denial of service.** An event storm could overwhelm the connector; a
  bounded event queue + a coalescing window (collapse N events for one resource
  within a window into one re-read) + a backstop pull are the mitigations.
- **E — Elevation of privilege.** No new Azure permission beyond the 486
  read-only set + the Event Grid subscription read; the connector adds NO write
  path.

## Acceptance criteria

- [ ] **AC-1.** The connector subscribes to Azure change events (Event Grid /
      Activity-Log diagnostic settings) for the in-scope resource types.
- [ ] **AC-2.** On an event, the connector re-reads the changed resource via the
      existing read-only Graph/ARM path and emits a fresh record (event is a
      trigger, not the data).
- [ ] **AC-3.** `register` advertises `profiles_supported=[pull, subscribe]`; the
      pull profile remains as the reconciliation backstop.
- [ ] **AC-4.** Subscribe-emitted and pull-emitted records dedupe correctly
      (documented idempotency-key interaction; a test pins it).
- [ ] **AC-5.** The Event Grid webhook is authenticated; a forged event cannot
      fabricate a record (a test asserts the re-read path).
- [ ] **AC-6.** Docs name the event-driven latency honestly (not "continuous
      monitoring"); README + decisions log + changelog updated.

## Anti-criteria (P0 — block merge)

- **P0-522-1.** Does NOT trust the event payload as evidence data — always
  re-reads real state before emitting.
- **P0-522-2.** Does NOT accept unauthenticated Event Grid deliveries.
- **P0-522-3.** Does NOT label the subscribe profile "continuous monitoring".
- **P0-522-4.** Does NOT widen the platform-side wire (push only, invariant #3).
- **P0-522-5.** Does NOT add any write-scope Azure permission.

## Dependencies

- **#486** (base Azure connector) — `merged`. The pull profile + the read-only
  Graph/ARM collectors this slice triggers.
