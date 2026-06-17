# 658 — Azure connector: Event-Grid subscription / Activity-Log diagnostic-setting auto-provisioning

**Cluster:** Connectors
**Estimate:** M (2-3d)
**Type:** JUDGMENT (write-scope boundary + provisioning UX)
**Status:** `in-progress`

## Decision (maintainer, 2026-06-15 — RESOLVED)

The write-scope boundary is resolved: provisioning ships as a **SEPARATE,
opt-in, one-shot `provision` / `deprovision` subcommand** the operator runs with
**their own elevated, short-lived Azure credential** (its own env vars
`AZURE_PROVISION_TENANT_ID` / `AZURE_PROVISION_CLIENT_ID` /
`AZURE_PROVISION_CLIENT_SECRET`), distinct from the receiver's read-only
`AZURE_*`. The long-lived `eventgrid` receiver **never** holds a write scope
(P0-658-1). Provisioning talks to Azure's ARM management API only — it does not
widen the platform push wire (P0-658-2). Diagnostic-setting provisioning is
**included** (opt-in within the opt-in command via `--with-diagnostic`). Full
rationale + the exact RBAC actions are in
`docs/audit-log/658-azure-provisioning-decisions.md`.

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

## Acceptance criteria (finalized against the resolved decision)

- [x] **AC-1.** `atlas-azure provision` — an opt-in path that, given the
      operator's elevated credential (its own `AZURE_PROVISION_*` env vars, NOT
      the receiver's read-only `AZURE_*`), creates the Event-Grid **system topic** + **event subscription** pointed at the receiver's webhook (derived from
      `--webhook-host` + `--path`), carries the delivery key, and — with
      `--with-diagnostic` — the Activity-Log diagnostic setting. Idempotent
      (ARM PUT upsert). Implemented in
      `connectors/azure/cmd/atlas-azure/cmd_provision.go` +
      `connectors/azure/internal/provision/`.
- [x] **AC-2.** Honest naming + docs: the `provision` / `deprovision` help text
      and the README scope the "no write path" claim to the **receiver** and label
      provisioning a distinct PRIVILEGED action requiring a separate elevated
      credential; the exact RBAC actions are documented (and printable via
      `provision --print-rbac`) as operator-supplied, never connector-held.
- [x] **AC-3.** `atlas-azure deprovision` (and `provision --teardown`) DELETEs the
      event subscription, system topic, and diagnostic setting; idempotent (DELETE
      of an absent resource is a no-op).

## Anti-criteria

- **P0-658-1.** Does NOT grant the long-lived read-only receiver process a write
  scope; any provisioning credential is separate + short-lived.
- **P0-658-2.** Does NOT widen the platform-side wire (invariant #3).

## Dependencies

- **#522** (Azure Event Grid subscribe profile) — the receiver this would provision
  for.
- A maintainer decision on the write-scope boundary (blocks `ready`).
