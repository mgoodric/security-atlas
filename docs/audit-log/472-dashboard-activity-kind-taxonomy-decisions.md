# Slice 472 — Dashboard activity-kind taxonomy decisions

**Type:** JUDGMENT
**Surface:** `GET /v1/activity` dashboard activity feed
**Source issue:** OPENENGINE-472 / OE-417a

## Decision

`/v1/activity` uses the canonical slice 669 unified-ledger `kind`
vocabulary for dashboard filters, narrowed to tenant-public business
activity kinds:

- `decision`
- `evidence`
- `exception`
- `sample`
- `audit_period`
- `aggregation_rule`
- `walkthrough`

The endpoint accepts one optional `kind` query parameter with one of the
above values. Omit `kind` for the heterogeneous feed. The response keeps
the existing dashboard envelope and row fields; `event_type` is projected
as `<kind>.<action>` so existing panel consumers do not need the richer
standalone ledger wire shape.

## Rationale

Slice 667 correctly removed the dashboard chips because the endpoint only
read `evidence_audit_log`. This slice widens the endpoint to the existing
unified audit-ledger read model rather than inventing dashboard-only
categories.

The rejected dashboard-only labels were:

- `controls`: no first-class control audit-log kind exists today. Control
  activity currently appears through existing business kinds such as
  `decision` and `evidence`.
- `approvals`: approval is an action/workflow verb, not a ledger kind. It
  can appear as an action under concrete kinds such as `exception`.

Binding chips to those labels would recreate the slice 667 problem: a UI
facet whose name does not correspond to an endpoint contract. 417b should
bind chips to the real `kind` values above. A later slice can add higher
level facets such as `controls` or `approvals` only after a read model
defines their membership explicitly.

`feature_flag` and `me` remain full-ledger concepts but are not dashboard
chip kinds. `feature_flag` is admin/program-configuration telemetry, and
`me` is self-audit telemetry; both belong on the standalone ledger surface
rather than the compact dashboard panel.

## Pagination and RLS

The widened feed reuses the unified ledger query under the dashboard
store's existing tenant transaction and `tenancy.ApplyTenant` call. RLS
still fires on each source audit table.

The activity cursor changed from the evidence-only `(ts, resource_id)`
boundary to the unified ledger `(occurred_at, kind, row_id)` boundary,
encoded opaquely as the existing `cursor` parameter. This matches the
query ordering and avoids duplicates or skips when multiple kinds share a
timestamp.

## Slice 232 relationship

This backend work does not by itself finish slice 232. A public
non-admin ledger route already exists at `/v1/activity/unified`, and this
slice makes the dashboard summary use the same kind taxonomy. Slice 232's
remaining work is frontend affordance work: restore a dashboard footer
link only when the target route is usable and the panel is non-empty.
