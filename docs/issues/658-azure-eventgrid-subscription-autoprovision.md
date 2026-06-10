# 658 — Azure connector: Event-Grid subscription / Activity-Log diagnostic-setting auto-provisioning

**Cluster:** Connectors
**Estimate:** M (2-3d)
**Type:** JUDGMENT (write-scope boundary + provisioning UX)
**Status:** `not-ready` (needs the write-scope-permission decision resolved first)

## Narrative

Surfaced during slice 522 (Azure Event Grid subscribe profile). Slice 522's
`eventgrid` subcommand **receives** Event Grid change events; it does NOT auto-create
the Event-Grid system topic + event subscription (or the Activity-Log diagnostic
setting) that routes events to the connector's webhook. Today the operator wires that
in the Azure portal / IaC by hand.

Auto-provisioning would let the connector create its own Event Grid subscription
pointed at its `--path`, set the delivery key, and tear it down on stop. The catch:
that requires a **write-scope** Azure permission (e.g. `Microsoft.EventGrid/*` write

- `Microsoft.Insights/diagnosticSettings` write) — which slice 522 **explicitly
  excludes** (its hard P0 is "no new Azure permission beyond the read-only set + the
  Event Grid subscription READ; NO write path"). Adding a write path is a real change to
  the connector's privilege posture and must be a **deliberate, opt-in** decision, not
  a silent escalation.

This slice is `not-ready` until that boundary is resolved: do we want the connector
to hold a write-scope credential at all, or keep provisioning operator-owned (portal
/ IaC / a separate one-shot `provision` subcommand the operator runs with elevated
creds, separate from the long-lived receiver's read-only creds)?

## Acceptance criteria (draft — pending the write-scope decision)

- [ ] **AC-1.** A documented, opt-in path to provision the Event-Grid subscription
      pointed at the connector's webhook (separate elevated credential, NOT the
      receiver's read-only credential).
- [ ] **AC-2.** Honest naming + docs: provisioning is a distinct, privileged action;
      the steady-state receiver stays read-only.
- [ ] **AC-3.** Teardown / cleanup on operator request.

## Anti-criteria

- **P0-658-1.** Does NOT grant the long-lived read-only receiver process a write
  scope; any provisioning credential is separate + short-lived.
- **P0-658-2.** Does NOT widen the platform-side wire (invariant #3).

## Dependencies

- **#522** (Azure Event Grid subscribe profile) — the receiver this would provision
  for.
- A maintainer decision on the write-scope boundary (blocks `ready`).
