# #2201 — vendor initial-review window settable via PATCH /v1/tenants and Settings — decisions log

**Ticket:** https://github.com/mgoodric/security-atlas/issues/2201
**Date:** 2026-11-14

Follow-up from #2061 (D1 there): `tenants.vendor_initial_review_days` could
only be changed in the database. It is now a field of `PATCH /v1/tenants/{id}`
beside `risk_review_cadence_days` (#2059), audited as
`tenant_vendor_initial_review_update`, with a Settings control under the risk
review cadence.

**Detection-tier classification (slice 353 / Q-13):**

- `detection_tier_actual`: none
- `detection_tier_target`: none

No bug surfaced during the work.

---

## D1 — same gate and shape as the risk review cadence

Admin or super_admin on the caller's own tenant, the gate every field of the
PATCH shares. The handler checks 1..365 and answers 400 outside it; the
existing `tenants_vendor_initial_review_days_chk` is the second leg. One
`me_audit_log` row per change, with `{"vendor_initial_review_days": n}` before
and after, written in the same transaction, plus the external-sink fan-out.

## D2 — the CHECK rewrite is tested against the definition it replaces

Migration `20261116000000_vendor_initial_review_audit.sql` rewrites the full
`me_audit_log_action_check` from `20261031000000_self_approval.sql` (the
latest definition on `main`) and appends the new value. The integration test
reads the value list out of the older migration file rather than restating
it, so a value dropped in the rewrite fails the test, and inserts every value
under the new CHECK. The down migration refuses while a row with the new
action exists, the same evidence rule as #2101's down.

## D3 — a read endpoint, `GET /v1/vendors/initial-review-window`

The Settings control needs the current value, and no tenant read returns it
to the web layer (the same gap slice 613 D1 recorded for the gate policy).
The risk cadence solved it with `GET /v1/risks/review-cadence`; this mirrors
that under the vendors module, readable by any role, since the value is not
sensitive and is what every vendor's first review date is computed from.

## D4 — a change moves existing never-reviewed vendors

Unlike the risk cadence (stamped into `review_due_at` at insert), the vendor
first review date is computed from the window on every read. Changing the
window therefore moves the first review date, and the overdue flag, of every
vendor not yet reviewed. That is the #2061 design, not new behaviour; the
Settings copy says so, and the integration test asserts it on a vendor
created before the change.
