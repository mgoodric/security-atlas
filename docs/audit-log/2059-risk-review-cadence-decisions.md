# #2059 — risk review due defaults from an organisation cadence

`review_due_at` was stored only when the caller sent it, so every register
row read "Pending evaluation" under Review due. Triage fixed the shape: an
organisation cadence (default 365 days), created + cadence for a risk made
without a date, a backfill of existing risks, the computed date on the
new-risk form, and the misleading tooltip removed. The build-time calls:

detection_tier_actual: none
detection_tier_target: none

## D1 — The default is a BEFORE INSERT trigger, not Go

Risks are inserted by the risk store (`CreateRisk`), the aggregation path
(`CreateAggregateRisk`), the MCP write-proposal applier (raw SQL) and the
demo seed. Defaulting in one Go path would leave the others writing NULL.
`risks_review_due_default` sets `NEW.created_at + cadence` when
`review_due_at` is NULL, so every path gets the same date. The repo already
keeps risk invariants in triggers (`risks_category_sync`, #1809). INSERT
only: an UPDATE never re-derives a date, which is what makes a cadence
change affect new risks only.

## D2 — The cadence is a column on `tenants`

`risk_review_cadence_days INTEGER NOT NULL DEFAULT 365`, CHECK 1..3650,
beside `bundle_gate_mode` (slice 608), written by the same
`PATCH /v1/tenants/{id}` and audited as `tenant_risk_review_cadence_update`.
A tenant with no `tenants` row (bare-UUID, pre slice 144) reads 365 in the
trigger, the backfill and the read endpoint.

## D3 — A read endpoint under /v1/risks

`GET /v1/risks/review-cadence` returns the cadence. There is no GET for a
single tenant row (slice 613 D1 records the same gap), and the new-risk form
is used by risk creators, not only admins, so the read sits with the risk
routes and is gated like them.

## D4 — The form sends a date only when the user changes it

The form shows today + cadence (UTC day) in a date input. Left untouched, it
omits `review_due_at` so the trigger stores the exact `created_at + cadence`;
a changed date is sent as midnight UTC.

## D5 — Overdue is computed in the register, by UTC day

The register marks a row Overdue when the UTC day of `review_due_at` is
before today's UTC day: a review due today is not overdue (#2075's rule for
policy reviews). A row with no date (now only possible for data that escaped
the backfill) reads "Not set", not "Pending evaluation".

## D6 — The down migration keeps the backfilled dates

Backfilled dates are indistinguishable from user-set ones after the fact, and
a schema rollback does not delete data, so the down drops the trigger, the
column and the audit action only.
